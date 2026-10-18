package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/wltechblog/gino/internal/chat"
	"github.com/wltechblog/gino/internal/mcp"
)

// MessageTool sends messages to the user's current channel/chat via the hub.
//
// Destination resolution: Execute reads the per-turn origin from the context
// FIRST (mcp.OriginFromContext — the agent loop stamps it before every tool
// call) and falls back to the ambient state set by SetContext. The tool is a
// singleton shared by all concurrent turns: with two Discord threads live,
// last-writer-wins ambient state alone routes mid-turn sends (interim
// replies, file attachments) to whichever thread dispatched most recently —
// the "replying in the wrong place" field bug. Per-turn origin makes the
// destination an immutable property of the calling turn.
type MessageTool struct {
	hub *chat.Hub

	mu       sync.RWMutex
	channel  string
	chatID   string
	metadata map[string]interface{}
}

func NewMessageTool(b *chat.Hub) *MessageTool {
	return &MessageTool{hub: b}
}

func (m *MessageTool) Name() string { return "message" }
func (m *MessageTool) Description() string {
	return "Send a message (with optional file attachments) to the current channel/chat"
}

func (m *MessageTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"content": map[string]interface{}{
				"type":        "string",
				"description": "The message content to send",
			},
			"files": map[string]interface{}{
				"type":        "array",
				"items":       map[string]interface{}{"type": "string"},
				"description": "Absolute file paths to send as attachments. On Telegram these are sent as documents.",
			},
		},
		"required": []string{"content"},
	}
}

// SetContext records the ambient destination for outgoing messages: the
// fallback used when a calling context carries no turn origin. It reflects
// only the most recent dispatch, so concurrent turns must not rely on it —
// they carry their own origin in the context.
func (m *MessageTool) SetContext(channel, chatID string, metadata map[string]interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.channel = channel
	m.chatID = chatID
	m.metadata = metadata
}

// Expected args: {"content": "..."}
func (m *MessageTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	content := ""
	if c, ok := args["content"]; ok {
		switch v := c.(type) {
		case string:
			content = v
		default:
			b, _ := json.Marshal(v)
			content = string(b)
		}
	}
	if content == "" {
		return "", fmt.Errorf("message tool: 'content' argument required")
	}

	var media []string
	if filesRaw, ok := args["files"]; ok {
		if arr, ok := filesRaw.([]interface{}); ok {
			for _, f := range arr {
				if s, ok := f.(string); ok && s != "" {
					media = append(media, s)
				}
			}
		}
	}

	m.mu.RLock()
	channel, chatID := m.channel, m.chatID
	metadata := m.metadata
	m.mu.RUnlock()

	// Per-turn origin beats ambient singleton state: concurrent sessions
	// each carry their own destination in the context (race-free), while
	// ambient reflects only the most recent dispatch. Ambient metadata is
	// kept only when it agrees with the origin — metadata from a different
	// session (e.g. sender_id used for DM fallback) is worse than none.
	if o, ok := mcp.OriginFromContext(ctx); ok && o.Channel != "" && o.ChatID != "" {
		if channel != o.Channel || chatID != o.ChatID {
			metadata = nil
		}
		channel, chatID = o.Channel, o.ChatID
	}

	if channel == "" || chatID == "" {
		return "", fmt.Errorf("message tool: no destination — no turn origin in context and no ambient context set")
	}

	out := chat.Outbound{
		Channel:  channel,
		ChatID:   chatID,
		Content:  content,
		Media:    media,
		Metadata: metadata,
	}
	select {
	case m.hub.Out <- out:
		result := "sent"
		if len(media) > 0 {
			result = fmt.Sprintf("sent with %d file(s)", len(media))
		}
		return result, nil
	default:
		return "", fmt.Errorf("outbound channel full")
	}
}
