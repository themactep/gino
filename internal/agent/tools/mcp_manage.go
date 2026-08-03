package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// ---- Shared types ----

// MCPClientInfo is a lightweight snapshot of a connected MCP server.
type MCPClientInfo struct {
	Name  string
	Tools []string
}

// FormatMCPServerList builds a human-readable summary of connected MCP servers.
func FormatMCPServerList(clients []MCPClientInfo) string {
	if len(clients) == 0 {
		return "No MCP servers connected."
	}
	var sb strings.Builder
	for _, c := range clients {
		sb.WriteString(fmt.Sprintf("**%s** (%d tools): %s\n", c.Name, len(c.Tools), strings.Join(c.Tools, ", ")))
	}
	return sb.String()
}

// ---- MCPRestartTool ----

// MCPRestartTool allows the agent to restart a specific MCP server on demand.
type MCPRestartTool struct {
	mu       sync.Mutex
	callback func(serverName string) (string, error)
}

func NewMCPRestartTool() *MCPRestartTool {
	return &MCPRestartTool{}
}

func (t *MCPRestartTool) SetCallback(cb func(serverName string) (string, error)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.callback = cb
}

func (t *MCPRestartTool) Name() string { return "mcp_restart" }

func (t *MCPRestartTool) Description() string {
	return "Restart a specific MCP server by name. Useful when a server is unresponsive or needs reconnection. Use mcp_list to see available servers."
}

func (t *MCPRestartTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"server": map[string]interface{}{
				"type":        "string",
				"description": "Name of the MCP server to restart (must match config key)",
			},
		},
		"required": []string{"server"},
	}
}

func (t *MCPRestartTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	serverRaw, ok := args["server"]
	if !ok {
		return "", fmt.Errorf("mcp_restart: 'server' argument required")
	}
	server, ok := serverRaw.(string)
	if !ok {
		return "", fmt.Errorf("mcp_restart: 'server' must be a string")
	}

	t.mu.Lock()
	cb := t.callback
	t.mu.Unlock()

	if cb == nil {
		return "", fmt.Errorf("mcp_restart: not initialized (no callback)")
	}

	return cb(server)
}

// ---- MCPListTool ----

// MCPListTool lists all connected MCP servers and their tools.
type MCPListTool struct {
	mu       sync.Mutex
	callback func() string
}

func NewMCPListTool() *MCPListTool {
	return &MCPListTool{}
}

func (t *MCPListTool) SetCallback(cb func() string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.callback = cb
}

func (t *MCPListTool) Name() string { return "mcp_list" }

func (t *MCPListTool) Description() string {
	return "List all connected MCP servers and their available tools."
}

func (t *MCPListTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (t *MCPListTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	t.mu.Lock()
	cb := t.callback
	t.mu.Unlock()

	if cb == nil {
		return "No MCP servers configured.", nil
	}

	return cb(), nil
}

// ---- MCPManageTool (dynamic add/remove) ----

// MCPManageCallback is implemented by AgentLoop to connect/disconnect MCP servers at runtime.
type MCPManageCallback interface {
	AddMCPServer(name string, command string, args []string, url string, env map[string]string) error
	RemoveMCPServer(name string) error
}

// MCPManageTool provides runtime add and remove of MCP servers, enabling
// dynamic tool expansion without restarting the process.
type MCPManageTool struct {
	mu       sync.Mutex
	callback MCPManageCallback
}

func NewMCPManageTool() *MCPManageTool {
	return &MCPManageTool{}
}

func (t *MCPManageTool) SetCallback(cb MCPManageCallback) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.callback = cb
}

func (t *MCPManageTool) Name() string { return "mcp_manage" }

func (t *MCPManageTool) Description() string {
	return `Manage MCP servers at runtime.

Actions:
- "add": Connect a new MCP server. Requires "name" and either "command" (for stdio) or "url" (for HTTP/SSE).
- "remove": Disconnect an MCP server and unregister its tools. Requires "name".

Use mcp_list to see currently connected servers.`
}

func (t *MCPManageTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"add", "remove"},
				"description": "Whether to add (connect) or remove (disconnect) an MCP server",
			},
			"name": map[string]interface{}{
				"type":        "string",
				"description": "Unique name for the MCP server",
			},
			"command": map[string]interface{}{
				"type":        "string",
				"description": "For stdio servers: the command to run (e.g. \"node\", \"npx\", \"python\")",
			},
			"args": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "For stdio servers: arguments to pass to the command",
			},
			"url": map[string]interface{}{
				"type":        "string",
				"description": "For HTTP/SSE servers: the server URL",
			},
			"env": map[string]interface{}{
				"type":        "object",
				"description": "Environment variables for stdio servers",
				"additionalProperties": map[string]interface{}{"type": "string"},
			},
		},
		"required": []string{"action", "name"},
	}
}

func (t *MCPManageTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	t.mu.Lock()
	cb := t.callback
	t.mu.Unlock()

	if cb == nil {
		return "", fmt.Errorf("mcp_manage: not initialized (no callback)")
	}

	action, _ := args["action"].(string)
	name, _ := args["name"].(string)

	if name == "" {
		return "", fmt.Errorf("mcp_manage: 'name' is required")
	}

	switch action {
	case "add":
		command, _ := args["command"].(string)
		url, _ := args["url"].(string)

		if command == "" && url == "" {
			return "", fmt.Errorf("mcp_manage: either 'command' or 'url' is required for add")
		}

		var cmdArgs []string
		if argsRaw, ok := args["args"].([]interface{}); ok {
			for _, a := range argsRaw {
				if s, ok := a.(string); ok {
					cmdArgs = append(cmdArgs, s)
				}
			}
		}

		env := map[string]string{}
		if envRaw, ok := args["env"].(map[string]interface{}); ok {
			for k, v := range envRaw {
				if s, ok := v.(string); ok {
					env[k] = s
				}
			}
		}

		if err := cb.AddMCPServer(name, command, cmdArgs, url, env); err != nil {
			return "", err
		}

		return fmt.Sprintf("MCP server %q connected successfully. Use mcp_list to see its tools.", name), nil

	case "remove":
		if err := cb.RemoveMCPServer(name); err != nil {
			return "", err
		}
		return fmt.Sprintf("MCP server %q disconnected and tools removed.", name), nil

	default:
		return "", fmt.Errorf("mcp_manage: unknown action %q (use \"add\" or \"remove\")", action)
	}
}
