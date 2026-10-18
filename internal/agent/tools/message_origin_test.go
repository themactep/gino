package tools

import (
	"context"
	"testing"
	"time"

	"github.com/wltechblog/gino/internal/chat"
	"github.com/wltechblog/gino/internal/mcp"
)

// TestMessageToolOriginBeatsAmbient pins the "replying in the wrong place"
// field bug: two concurrent Discord thread turns share one MessageTool
// singleton. Thread B dispatches after thread A, clobbering ambient state.
// Thread A's mid-turn message call must still land in thread A — the
// per-turn origin in the context beats the last-writer ambient state.
func TestMessageToolOriginBeatsAmbient(t *testing.T) {
	hub := chat.NewHub(16)
	hub.StartRouter(context.Background())
	mt := NewMessageTool(hub)

	// Thread A dispatches; ambient points at A.
	mt.SetContext("discord", "threadA", map[string]interface{}{"sender_id": "A"})
	// Thread B dispatches LAST — ambient now points at B (the race).
	mt.SetContext("discord", "threadB", map[string]interface{}{"sender_id": "B"})

	// Thread A's turn is still running and calls the message tool with ITS
	// origin stamped in the context (the loop does this via ctxWithOrigin).
	ctxA := mcp.WithTurnOriginSession(context.Background(), "discord", "threadA", "discord:threadA")
	_, err := mt.Execute(ctxA, map[string]interface{}{"content": "hello from A"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	select {
	case out := <-hub.Out:
		if out.ChatID != "threadA" {
			t.Fatalf("mid-turn send routed to %s — cross-over (wanted threadA)", out.ChatID)
		}
		// Ambient metadata from session B must not ride along on A's message.
		if sid, _ := out.Metadata["sender_id"].(string); sid == "B" {
			t.Fatal("metadata from session B leaked onto session A's message")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no outbound message received")
	}
}

// TestMessageToolAmbientFallbackStillWorks: contexts without origin (direct
// callers, tests) keep using the ambient destination.
func TestMessageToolAmbientFallbackStillWorks(t *testing.T) {
	hub := chat.NewHub(16)
	hub.StartRouter(context.Background())
	mt := NewMessageTool(hub)
	mt.SetContext("telegram", "42", nil)

	if _, err := mt.Execute(context.Background(), map[string]interface{}{"content": "hi"}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	select {
	case out := <-hub.Out:
		if out.Channel != "telegram" || out.ChatID != "42" {
			t.Fatalf("ambient fallback broken: %+v", out)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no outbound message received")
	}
}

// TestMessageToolNoDestinationErrors: with neither origin nor ambient set,
// the tool fails loudly instead of silently dropping or mis-routing.
func TestMessageToolNoDestinationErrors(t *testing.T) {
	hub := chat.NewHub(16)
	hub.StartRouter(context.Background())
	mt := NewMessageTool(hub)

	if _, err := mt.Execute(context.Background(), map[string]interface{}{"content": "hi"}); err == nil {
		t.Fatal("expected error with no destination available")
	}
}
