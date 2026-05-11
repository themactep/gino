package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/local/picobot/internal/chat"
)

// MessageTool sends messages to a channel via the chat Hub.
// It holds a context (channel + chatID) which should be set per-incoming-message.
type MessageTool struct {
	hub     *chat.Hub
	channel string
	chatID  string
}

func NewMessageTool(b *chat.Hub) *MessageTool {
	return &MessageTool{hub: b}
}

func (m *MessageTool) Name() string        { return "message" }
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

// SetContext sets the current channel and chat id for outgoing messages.
func (m *MessageTool) SetContext(channel, chatID string) {
	m.channel = channel
	m.chatID = chatID
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

	out := chat.Outbound{
		Channel: m.channel,
		ChatID:  m.chatID,
		Content: content,
		Media:   media,
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
