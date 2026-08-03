package audit

import (
	"testing"
	"time"
)

func TestStoreRecordAndQueryMessages(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{Enabled: true, DBPath: dir + "/test.db", MessageRetentionDays: 7, MaxContentLen: 4096}

	store, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer store.Close()

	// Record some messages
	store.RecordMessage(MessageRecord{
		UserID:   "user1",
		Channel:  "api",
		Direction: "inbound",
		Content:  "Hello world",
	}, cfg)

	store.RecordMessage(MessageRecord{
		UserID:   "user1",
		Channel:  "api",
		Direction: "outbound",
		Content:  "Hi there! How can I help?",
		TokensIn: 100,
		TokensOut: 20,
	}, cfg)

	// Query messages
	msgs, err := store.QueryMessages("user1", time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Fatalf("QueryMessages failed: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
}

func TestStoreRecordAndQueryUsage(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{Enabled: true, DBPath: dir + "/test.db"}

	store, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer store.Close()

	// Record usage
	store.RecordUsage(UsageRecord{
		UserID:    "user1",
		Model:     "gpt-4",
		TokensIn:  500,
		TokensOut: 100,
		CostIn:    0.0075,
		CostOut:   0.002,
		TurnID:    "turn-001",
		Channel:   "api",
	})

	store.RecordUsage(UsageRecord{
		UserID:    "user1",
		Model:     "gpt-4",
		TokensIn:  300,
		TokensOut: 80,
		CostIn:    0.0045,
		CostOut:   0.0016,
		TurnID:    "turn-002",
		Channel:   "api",
	})

	// Query usage records
	records, err := store.QueryUsage("user1", time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Fatalf("QueryUsage failed: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("expected 2 usage records, got %d", len(records))
	}

	// Query usage summary
	summary, err := store.QueryUsageSummary("user1", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("QueryUsageSummary failed: %v", err)
	}
	if summary.Count != 2 {
		t.Errorf("expected count 2, got %d", summary.Count)
	}
	if summary.TotalIn != 800 {
		t.Errorf("expected totalIn 800, got %d", summary.TotalIn)
	}
	if summary.TotalOut != 180 {
		t.Errorf("expected totalOut 180, got %d", summary.TotalOut)
	}
	expectedCost := 0.0075 + 0.002 + 0.0045 + 0.0016
	if summary.TotalCost < expectedCost-0.0001 || summary.TotalCost > expectedCost+0.0001 {
		t.Errorf("expected totalCost ~%.4f, got %.4f", expectedCost, summary.TotalCost)
	}
}

func TestStorePurgeOld(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{Enabled: true, DBPath: dir + "/test.db"}

	store, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %0v", err)
	}
	defer store.Close()

	// Insert a record directly with old timestamp
	store.mu.Lock()
	_, _ = store.db.Exec(
		`INSERT INTO messages (user_id, channel, direction, content, timestamp) VALUES (?, ?, ?, ?, ?)`,
		"user1", "api", "inbound", "old message",
		time.Now().UTC().Add(-48*time.Hour),
	)
	store.mu.Unlock()

	// Purge with 1-day retention
	store.PurgeOld(1, 365)

	// Should be gone
	msgs, err := store.QueryMessages("user1", time.Now().Add(-72*time.Hour), 100)
	if err != nil {
		t.Fatalf("QueryMessages failed: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages after purge, got %d", len(msgs))
	}
}

func TestStoreDisabled(t *testing.T) {
	// When disabled, New should return a store that doesn't crash on any operation
	store, err := New(Config{Enabled: false})
	if err != nil {
		t.Fatalf("New with disabled config failed: %v", err)
	}
	defer store.Close()

	// These should all be no-ops without panicking
	store.RecordMessage(MessageRecord{UserID: "user1"}, Config{Enabled: false})
	store.RecordUsage(UsageRecord{UserID: "user1"})

	msgs, err := store.QueryMessages("user1", time.Now().Add(-time.Hour), 100)
	if err != nil {
		t.Errorf("QueryMessages on disabled store failed: %v", err)
	}
	if msgs != nil {
		t.Errorf("expected nil messages from disabled store")
	}
}

func TestTruncate(t *testing.T) {
	result := truncate("hello world", 5)
	if result != "hello..." {
		t.Errorf("truncate('hello world', 5) = %q, want %q", result, "hello...")
	}

	// No truncation needed
	result = truncate("hi", 10)
	if result != "hi" {
		t.Errorf("truncate('hi', 10) = %q, want %q", result, "hi")
	}

	// No limit
	result = truncate("hello", 0)
	if result != "hello" {
		t.Errorf("truncate('hello', 0) = %q, want %q", result, "hello")
	}
}

func TestExportMessages(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{Enabled: true, DBPath: dir + "/test.db"}

	store, err := New(cfg)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}
	defer store.Close()

	store.RecordMessage(MessageRecord{
		UserID:    "user1",
		Channel:   "api",
		Direction: "inbound",
		Content:   "test",
	}, cfg)

	data, err := store.ExportMessages("user1", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("ExportMessages failed: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty JSON export")
	}

	// Verify it doesn't contain other users' data
	store.RecordMessage(MessageRecord{
		UserID:    "user2",
		Channel:   "api",
		Direction: "inbound",
		Content:   "secret",
	}, cfg)

	data2, err := store.ExportMessages("user1", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("ExportMessages failed: %v", err)
	}
	// user1 export should not contain user2 data
	if string(data2) == "" || contains(string(data2), "secret") {
		t.Error("export for user1 should not contain user2's data")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && (s[0:len(substr)] == substr || contains(s[1:], substr)))
}
