package tools

import (
	"context"
	"testing"
	"time"

	"github.com/wltechblog/gino/internal/chat"
)

func TestMessageToolPublishesOutbound(t *testing.T) {
	b := chat.NewHub(10)
	mt := NewMessageTool(b)
	mt.SetContext("cli", "test-chat")

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	res, err := mt.Execute(ctx, map[string]interface{}{"content": "hello world"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != "sent" {
		t.Fatalf("expected 'sent' result, got: %s", res)
	}

	select {
	case out := <-b.Out:
		if out.Content != "hello world" {
			t.Fatalf("unexpected content: %s", out.Content)
		}
	default:
		t.Fatalf("no outbound message published")
	}
}
