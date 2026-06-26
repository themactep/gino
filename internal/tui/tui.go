// Package tui implements a terminal-based chat interface for Gino.
// It uses the same hub/agent-loop infrastructure as the gateway, so the
// interactive CLI session has full tool access, memory, and session continuity.
package tui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/wltechblog/gino/internal/agent"
	"github.com/wltechblog/gino/internal/chat"
	"github.com/wltechblog/gino/internal/config"
	"github.com/wltechblog/gino/internal/providers"
)

// ANSI color codes.
const (
	reset   = "\033[0m"
	bold    = "\033[1m"
	dim     = "\033[2m"
	red     = "\033[31m"
	green   = "\033[32m"
	yellow  = "\033[33m"
	blue    = "\033[34m"
	magenta = "\033[35m"
	cyan    = "\033[36m"
	gray    = "\033[90m"
)

// ChatSession holds the state for a terminal chat session.
type ChatSession struct {
	hub      *chat.Hub
	agent    *agent.AgentLoop
	provider providers.LLMProvider
	cfg      config.Config
	model    string
	homeDir  string
	ws       string

	scanner *bufio.Scanner
	out     io.Writer
	chatID  string // unique session ID for hub routing

	// State
	multiLine bool
}

// New creates a new TUI chat session.
func New(cfg config.Config, provider providers.LLMProvider, homeDir, ws string) *ChatSession {
	scanner := bufio.NewScanner(os.Stdin)
	// Allow lines up to 1 MB (default is 64 KB, which truncates long pastes).
	scanner.Buffer(make([]byte, 0, 1024), 1024*1024)
	return &ChatSession{
		cfg:      cfg,
		provider: provider,
		homeDir:  homeDir,
		ws:       ws,
		scanner:  scanner,
		out:      os.Stdout,
		chatID:   "tui-" + fmt.Sprintf("%d", time.Now().UnixNano()),
	}
}

// Run starts the interactive chat loop. Blocks until the user exits.
func (s *ChatSession) Run(modelOverride string) error {
	s.model = modelOverride
	if s.model == "" && s.cfg.Agents.Defaults.Model != "" {
		s.model = s.cfg.Agents.Defaults.Model
	}
	if s.model == "" {
		s.model = s.provider.GetDefaultModel()
	}

	// Set up hub and agent loop — same as gateway but CLI-only.
	s.hub = chat.NewHub(100)

	maxIter := s.cfg.Agents.Defaults.MaxToolIterations
	if maxIter <= 0 {
		maxIter = 100
	}

	s.agent = agent.NewAgentLoop(
		s.hub, s.provider, s.model, maxIter, s.ws,
		nil, // scheduler — cron not active in TUI
		s.cfg.MCPServers, s.cfg.Agents.Defaults.AllowedDirs,
		s.cfg.Agents.Defaults.DisableTools,
		s.cfg.Brain, s.homeDir,
		s.cfg.Agents.Defaults.Sandbox,
		"", // signal socket — not active in TUI
		s.cfg.Agents.Defaults.MaxTurnMessages,
		s.cfg.Agents.Defaults.MaxToolResultChars,
		s.cfg.Agents.Defaults.Compaction,
		s.cfg.Agents.Defaults.Web,
	)
	defer s.agent.Close()

	if s.cfg.Agents.Defaults.EnableToolActivityIndicator != nil {
		s.agent.SetToolActivityIndicator(*s.cfg.Agents.Defaults.EnableToolActivityIndicator)
	}
	if s.cfg.Agents.Defaults.EnableToolCallMessages != nil {
		s.agent.SetToolCallMessages(*s.cfg.Agents.Defaults.EnableToolCallMessages)
	}

	// Subscribe to the "cli" channel for outbound messages.
	cliOut := s.hub.Subscribe("cli")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start agent loop.
	go s.agent.Run(ctx)

	// Start router.
	s.hub.StartRouter(ctx)

	// Handle Ctrl+C gracefully.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Fprintln(s.out)
		cancel()
	}()

	s.printBanner()

	for {
		// Read input.
		prompt := fmt.Sprintf("%syou%s ❯ ", cyan+bold, reset)
		fmt.Fprint(s.out, prompt)

		if !s.scanner.Scan() {
			break // EOF (Ctrl+D)
		}
		line := strings.TrimSpace(s.scanner.Text())
		if line == "" {
			continue
		}

		// Handle slash commands.
		if strings.HasPrefix(line, "/") {
			if s.handleCommand(line) {
				continue
			}
			break // /exit returns true from handleCommand → loop breaks
		}

		// Send message to agent and wait for response.
		s.sendMessage(ctx, cliOut, line)
	}

	return nil
}

// sendMessage sends a message to the agent loop and waits for the response.
func (s *ChatSession) sendMessage(ctx context.Context, cliOut <-chan chat.Outbound, text string) {
	msg := chat.Inbound{
		Channel:   "cli",
		SenderID:  "tui-user",
		ChatID:    s.chatID,
		Content:   text,
		Timestamp: time.Now(),
	}

	// Inject into hub.
	s.hub.In <- msg

	// Wait for response(s). The agent may send tool activity notifications
	// before the final response. We collect messages until we get the final
	// response (the one without an activity-indicator prefix).
	spinnerCtx, spinnerCancel := context.WithCancel(context.Background())
	go s.spinner(spinnerCtx)

	// Collect responses until timeout or we get the final answer.
	for {
		select {
		case <-ctx.Done():
			spinnerCancel()
			fmt.Fprintln(s.out)
			return

		case out, ok := <-cliOut:
			if !ok {
				spinnerCancel()
				fmt.Fprintln(s.out)
				return
			}

			// Tool activity notifications come first.
			if isActivityNotification(out.Content) {
				spinnerCancel()
				fmt.Fprintf(s.out, "\r%s%s%s\r", dim, out.Content, reset)
				continue
			}

			// Final response.
			spinnerCancel()
			fmt.Fprint(s.out, "\r")
			s.printResponse(out.Content)
			return

		case <-time.After(5 * time.Minute):
			spinnerCancel()
			fmt.Fprint(s.out, "\r")
			fmt.Fprintf(s.out, "%stimeout waiting for response%s\n", red, reset)
			return
		}
	}
}

// isActivityNotification returns true if the content is a tool activity message
// rather than a final response.
func isActivityNotification(content string) bool {
	prefixes := []string{"⏳", "🤖", "📢", "⛔", "🔄", "🗑️"}
	for _, p := range prefixes {
		if strings.HasPrefix(content, p) {
			return true
		}
	}
	return false
}

// spinner prints an animated spinner while waiting for a response.
func (s *ChatSession) spinner(ctx context.Context) {
	chars := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	i := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
			fmt.Fprintf(s.out, "\r%s%s thinking...%s", gray, chars[i%len(chars)], reset)
			i++
		}
	}
}

// printResponse formats and prints the agent's response.
func (s *ChatSession) printResponse(content string) {
	fmt.Fprintf(s.out, "%sgino%s ❯ %s\n", magenta+bold, reset, content)
	fmt.Fprintln(s.out)
}

// handleCommand processes slash commands. Returns true to continue the loop,
// false to exit.
func (s *ChatSession) handleCommand(line string) bool {
	parts := strings.Fields(line)
	cmd := parts[0]

	switch cmd {
	case "/exit", "/quit", "/q":
		return false

	case "/help", "/h", "/?":
		s.printHelp()

	case "/clear":
		fmt.Fprint(s.out, "\033[2J\033[H") // clear screen
		s.printBanner()

	case "/model":
		if len(parts) > 1 {
			s.model = parts[1]
			fmt.Fprintf(s.out, "%sModel set to: %s%s\n", green, s.model, reset)
		} else {
			fmt.Fprintf(s.out, "%sCurrent model: %s%s\n", dim, s.model, reset)
		}

	case "/multiline", "/multi":
		s.multiLine = !s.multiLine
		state := "off"
		if s.multiLine {
			state = "on"
		}
		fmt.Fprintf(s.out, "%sMulti-line mode: %s%s\n", dim, state, reset)

	default:
		fmt.Fprintf(s.out, "%sUnknown command: %s%s\n", yellow, cmd, reset)
		s.printHelp()
	}
	return true
}

// printBanner shows the startup banner.
func (s *ChatSession) printBanner() {
	fmt.Fprintln(s.out)
	fmt.Fprintf(s.out, "%s╔══════════════════════════════════════════╗\n", cyan)
	fmt.Fprintf(s.out, "║  🤖 Gino Chat %sv0.4.0%s                     ║\n", dim, cyan)
	fmt.Fprintf(s.out, "║  Model: %-34s║\n", truncForBox(s.model, 34)+" ")
	fmt.Fprintf(s.out, "║  Type %s/help%s for commands               ║\n", bold, cyan)
	fmt.Fprintf(s.out, "%s╚══════════════════════════════════════════╝\n%s", cyan, reset)
	fmt.Fprintln(s.out)
}

// printHelp shows available commands.
func (s *ChatSession) printHelp() {
	fmt.Fprintf(s.out, "\n%sCommands:%s\n", bold, reset)
	fmt.Fprintf(s.out, "  %s/help%s      Show this help\n", cyan, reset)
	fmt.Fprintf(s.out, "  %s/clear%s     Clear screen\n", cyan, reset)
	fmt.Fprintf(s.out, "  %s/model%s     Show or set model (/model gpt-4o)\n", cyan, reset)
	fmt.Fprintf(s.out, "  %s/multiline%s Toggle multi-line input mode\n", cyan, reset)
	fmt.Fprintf(s.out, "  %s/exit%s     Exit chat\n", cyan, reset)
	fmt.Fprintln(s.out)
}

// truncForBox truncates a string to fit in a box of the given width.
func truncForBox(s string, max int) string {
	if len(s) > max {
		return s[:max]
	}
	return s
}
