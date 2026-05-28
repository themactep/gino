package signal

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/local/picobot/internal/chat"
)

// Signal represents an external trigger received via Unix domain socket.
type Signal struct {
	// Type identifies the kind of signal (e.g., "agentchat.message", "webhook.github")
	Type string `json:"type"`

	// Channel is the chat channel to inject the message into (e.g., "telegram", "discord").
	// If empty, "signal" is used as the channel.
	Channel string `json:"channel,omitempty"`

	// ChatID is the specific conversation to target (e.g., a Telegram chat ID).
	// If empty, "default" is used.
	ChatID string `json:"chat_id,omitempty"`

	// Content is the message text to inject into the agent loop.
	Content string `json:"content"`

	// Priority can be "normal" (default) or "high".
	// High priority signals are processed before normal ones.
	Priority string `json:"priority,omitempty"`

	// Metadata holds arbitrary extra data that the agent can use.
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// Listener accepts external signals on a Unix domain socket and injects
// them as Inbound messages into the chat hub.
type Listener struct {
	socketPath string
	hub        *chat.Hub
	mu         sync.Mutex
	listener   net.Listener
	running    bool
}

// NewListener creates a new signal listener.
func NewListener(socketPath string, hub *chat.Hub) *Listener {
	return &Listener{
		socketPath: socketPath,
		hub:        hub,
	}
}

// SocketPath returns the path the listener is configured on.
func (l *Listener) SocketPath() string {
	return l.socketPath
}

// Start begins listening for signals on the Unix domain socket.
// It blocks until the context is cancelled.
func (l *Listener) Start(ctx context.Context) error {
	l.mu.Lock()
	// Ensure the directory exists
	dir := filepath.Dir(l.socketPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		l.mu.Unlock()
		return fmt.Errorf("signal: failed to create socket directory %s: %w", dir, err)
	}

	// Remove stale socket file
	os.Remove(l.socketPath)

	listener, err := net.Listen("unix", l.socketPath)
	if err != nil {
		l.mu.Unlock()
		return fmt.Errorf("signal: failed to listen on %s: %w", l.socketPath, err)
	}
	l.listener = listener
	l.running = true
	l.mu.Unlock()

	// Set socket permissions to be readable/writable by owner and group
	os.Chmod(l.socketPath, 0660)

	log.Printf("Signal: listening on %s", l.socketPath)

	// Accept connections in a goroutine, shutdown on context cancel
	go func() {
		<-ctx.Done()
		l.mu.Lock()
		l.running = false
		if l.listener != nil {
			l.listener.Close()
		}
		l.mu.Unlock()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			l.mu.Lock()
			running := l.running
			l.mu.Unlock()
			if !running {
				return nil // shutdown
			}
			log.Printf("Signal: accept error: %v", err)
			continue
		}
		go l.handleConnection(conn)
	}
}

// handleConnection reads a signal from a Unix socket connection,
// converts it to an Inbound message, and injects it into the hub.
func (l *Listener) handleConnection(conn net.Conn) {
	defer conn.Close()

	// Set a read deadline to prevent hanging connections
	conn.SetReadDeadline(time.Now().Add(10 * time.Second))

	buf := make([]byte, 65536) // 64KB max signal size
	n, err := conn.Read(buf)
	if err != nil {
		log.Printf("Signal: read error: %v", err)
		return
	}

	var sig Signal
	if err := json.Unmarshal(buf[:n], &sig); err != nil {
		log.Printf("Signal: invalid JSON: %v", err)
		conn.Write([]byte(`{"status":"error","error":"invalid JSON"}`))
		return
	}

	if sig.Content == "" {
		log.Printf("Signal: empty content, ignoring (type=%s)", sig.Type)
		conn.Write([]byte(`{"status":"error","error":"content is required"}`))
		return
	}

	// Apply defaults
	channel := sig.Channel
	if channel == "" {
		channel = "signal"
	}
	chatID := sig.ChatID
	if chatID == "" {
		chatID = "default"
	}

	// Build the inbound message
	content := sig.Content
	if sig.Type != "" && sig.Type != "generic" {
		// Prefix with signal type for context
		content = fmt.Sprintf("[External signal: %s] %s", sig.Type, sig.Content)
	}

	// Add metadata to content if present
	if len(sig.Metadata) > 0 {
		metaJSON, err := json.Marshal(sig.Metadata)
		if err == nil {
			content += fmt.Sprintf("\n[Signal metadata: %s]", string(metaJSON))
		}
	}

	inbound := chat.Inbound{
		Channel:   channel,
		SenderID:  "signal:" + sig.Type,
		ChatID:    chatID,
		Content:   content,
		Timestamp: time.Now(),
		Metadata: map[string]interface{}{
			"signal_type": sig.Type,
			"priority":    sig.Priority,
		},
	}

	// Inject into the hub
	select {
	case l.hub.In <- inbound:
		log.Printf("Signal: injected %s signal into %s:%s", sig.Type, channel, chatID)
		conn.Write([]byte(`{"status":"ok"}`))
	default:
		log.Printf("Signal: hub inbound channel full, dropping signal")
		conn.Write([]byte(`{"status":"error","error":"hub channel full"}`))
	}
}

// SendSignal is a helper that sends a signal to a Unix domain socket.
// This can be used by MCP servers or other tools to trigger picobot.
func SendSignal(socketPath string, sig Signal) error {
	conn, err := net.DialTimeout("unix", socketPath, 5*time.Second)
	if err != nil {
		return fmt.Errorf("signal: failed to connect to %s: %w", socketPath, err)
	}
	defer conn.Close()

	data, err := json.Marshal(sig)
	if err != nil {
		return fmt.Errorf("signal: failed to marshal signal: %w", err)
	}

	conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("signal: failed to write: %w", err)
	}

	// Read response
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 1024)
	n, err := conn.Read(buf)
	if err != nil {
		// Timeout is OK — server might not respond
		if !strings.Contains(err.Error(), "timeout") {
			return nil
		}
		return nil
	}

	var resp map[string]string
	if err := json.Unmarshal(buf[:n], &resp); err == nil {
		if resp["status"] != "ok" {
			return fmt.Errorf("signal: server returned %s: %s", resp["status"], resp["error"])
		}
	}

	return nil
}

// DefaultSocketPath returns the default Unix socket path for the given home directory.
func DefaultSocketPath(homeDir string) string {
	return filepath.Join(homeDir, "signals.sock")
}
