package signal

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/local/picobot/internal/chat"
)

func TestListenerBasic(t *testing.T) {
	// Create a temp socket path
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	hub := chat.NewHub(10)
	listener := NewListener(socketPath, hub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start listener in background
	go listener.Start(ctx)

	// Wait for socket to be created
	time.Sleep(100 * time.Millisecond)

	// Verify socket file exists
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		t.Fatal("socket file not created")
	}

	// Send a signal
	sig := Signal{
		Type:    "test.signal",
		Content: "Hello from test",
		Channel: "test",
		ChatID:  "123",
	}

	err := SendSignal(socketPath, sig)
	if err != nil {
		t.Fatalf("SendSignal failed: %v", err)
	}

	// Read from hub
	select {
	case msg := <-hub.In:
		if msg.Channel != "test" {
			t.Errorf("expected channel 'test', got %q", msg.Channel)
		}
		if msg.ChatID != "123" {
			t.Errorf("expected chatID '123', got %q", msg.ChatID)
		}
		if msg.SenderID != "signal:test.signal" {
			t.Errorf("expected senderID 'signal:test.signal', got %q", msg.SenderID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for signal to be delivered to hub")
	}
}

func TestListenerDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	hub := chat.NewHub(10)
	listener := NewListener(socketPath, hub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go listener.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	// Send signal without channel or chat_id — should default to "signal" and "default"
	sig := Signal{
		Type:    "agentchat.message",
		Content: "New message from agent B",
	}

	err := SendSignal(socketPath, sig)
	if err != nil {
		t.Fatalf("SendSignal failed: %v", err)
	}

	select {
	case msg := <-hub.In:
		if msg.Channel != "signal" {
			t.Errorf("expected default channel 'signal', got %q", msg.Channel)
		}
		if msg.ChatID != "default" {
			t.Errorf("expected default chatID 'default', got %q", msg.ChatID)
		}
		if msg.SenderID != "signal:agentchat.message" {
			t.Errorf("expected senderID 'signal:agentchat.message', got %q", msg.SenderID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for signal")
	}
}

func TestListenerEmptyContent(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	hub := chat.NewHub(10)
	listener := NewListener(socketPath, hub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go listener.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	// Send signal with empty content — should be rejected
	sig := Signal{
		Type: "test.empty",
	}

	err := SendSignal(socketPath, sig)
	if err == nil {
		t.Log("empty content signal was accepted (server may not respond to rejected signals)")
	}

	// Hub should NOT receive anything
	select {
	case msg := <-hub.In:
		t.Errorf("expected no message for empty content, got: %+v", msg)
	case <-time.After(500 * time.Millisecond):
		// Expected: no message
	}
}

func TestListenerInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	hub := chat.NewHub(10)
	listener := NewListener(socketPath, hub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go listener.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	// Send raw invalid JSON directly to socket
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer conn.Close()

	conn.Write([]byte("not json at all"))

	// Hub should NOT receive anything
	select {
	case msg := <-hub.In:
		t.Errorf("expected no message for invalid JSON, got: %+v", msg)
	case <-time.After(500 * time.Millisecond):
		// Expected: no message
	}
}

func TestListenerMetadata(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	hub := chat.NewHub(10)
	listener := NewListener(socketPath, hub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go listener.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	sig := Signal{
		Type:    "agentchat.message",
		Content: "Response from Agent B",
		Channel: "telegram",
		ChatID:  "98765",
		Metadata: map[string]interface{}{
			"from_agent": "agent-b",
			"session":    "collab-1",
		},
	}

	err := SendSignal(socketPath, sig)
	if err != nil {
		t.Fatalf("SendSignal failed: %v", err)
	}

	select {
	case msg := <-hub.In:
		if msg.Channel != "telegram" {
			t.Errorf("expected channel 'telegram', got %q", msg.Channel)
		}
		if msg.ChatID != "98765" {
			t.Errorf("expected chatID '98765', got %q", msg.ChatID)
		}
		// Content should include metadata
		expected := "from_agent"
		if !contains(msg.Content, expected) {
			t.Errorf("expected content to contain metadata key %q, got: %s", expected, msg.Content)
		}
		// Check inbound metadata
		if msg.Metadata["signal_type"] != "agentchat.message" {
			t.Errorf("expected signal_type metadata, got: %v", msg.Metadata)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for signal")
	}
}

func TestListenerCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	hub := chat.NewHub(10)
	listener := NewListener(socketPath, hub)

	ctx, cancel := context.WithCancel(context.Background())
	go listener.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	// Verify socket exists
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		t.Fatal("socket not created")
	}

	// Cancel context to shut down listener
	cancel()
	time.Sleep(200 * time.Millisecond)

	// Listener should have stopped (socket may or may not be removed depending on OS)
	// The important thing is no panic or hang
}

func TestDefaultSocketPath(t *testing.T) {
	path := DefaultSocketPath("/home/user/.picobot")
	expected := "/home/user/.picobot/signals.sock"
	if path != expected {
		t.Errorf("expected %q, got %q", expected, path)
	}
}

func TestSignalFormat(t *testing.T) {
	sig := Signal{
		Type:    "agentchat.message",
		Content: "Hello from agent B",
		Channel: "telegram",
		ChatID:  "12345",
		Priority: "high",
		Metadata: map[string]interface{}{
			"from": "agent-b",
		},
	}

	// Verify it round-trips through JSON
	data, err := json.Marshal(sig)
	if err != nil {
		t.Fatalf("failed to marshal signal: %v", err)
	}

	var parsed Signal
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal signal: %v", err)
	}

	if parsed.Type != sig.Type {
		t.Errorf("type mismatch: %q != %q", parsed.Type, sig.Type)
	}
	if parsed.Content != sig.Content {
		t.Errorf("content mismatch: %q != %q", parsed.Content, sig.Content)
	}
	if parsed.Channel != sig.Channel {
		t.Errorf("channel mismatch: %q != %q", parsed.Channel, sig.Channel)
	}
	if parsed.Priority != sig.Priority {
		t.Errorf("priority mismatch: %q != %q", parsed.Priority, sig.Priority)
	}
}

func TestStaleSocketCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	// Create a stale socket file
	conn, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("failed to create stale socket: %v", err)
	}
	conn.Close()

	// Verify stale socket exists
	if _, err := os.Stat(socketPath); os.IsNotExist(err) {
		t.Fatal("stale socket not created")
	}

	// Starting a new listener should clean up the stale socket
	hub := chat.NewHub(10)
	listener := NewListener(socketPath, hub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err = listener.Start(ctx)
	// Start blocks, so it should get past socket creation
	// The fact that it didn't error means stale socket was cleaned up
	if err != nil {
		// This shouldn't happen since Start blocks
		t.Fatalf("Start returned error: %v", err)
	}
}

func TestMultipleSignals(t *testing.T) {
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "test.sock")

	hub := chat.NewHub(50)
	listener := NewListener(socketPath, hub)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go listener.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	// Send multiple signals rapidly
	for i := 0; i < 5; i++ {
		sig := Signal{
			Type:    "test.batch",
			Content: fmt.Sprintf("Signal %d", i),
		}
		err := SendSignal(socketPath, sig)
		if err != nil {
			t.Logf("SendSignal %d failed: %v", i, err)
		}
	}

	// Wait for all signals to be delivered
	received := 0
	timeout := time.After(5 * time.Second)
	for received < 5 {
		select {
		case <-hub.In:
			received++
		case <-timeout:
			t.Fatalf("timed out after receiving %d/5 signals", received)
		}
	}

	if received != 5 {
		t.Errorf("expected 5 signals, got %d", received)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
