package memory

import "testing"

// TestRecentForSessionIsolation pins the Discord thread cross-over: short-term
// items captured under one session key must never surface in another
// session's Recent view. Sessionless items (legacy / single-session channels)
// remain visible everywhere.
func TestRecentForSessionIsolation(t *testing.T) {
	s := NewMemoryStore(50)

	s.AddShortForSession("[exec] output for thread A", "discord:threadA")
	s.AddShortForSession("[exec] output for thread B", "discord:threadB")
	s.AddShort("legacy global item")

	// Thread A's view: A's items + sessionless, never B's.
	a := s.RecentForSession(10, "discord:threadA")
	for _, it := range a {
		if it.Session == "discord:threadB" {
			t.Fatalf("thread B memory leaked into thread A's view: %s", it.Text)
		}
	}
	foundA, foundLegacy := false, false
	for _, it := range a {
		if it.Text == "[exec] output for thread A" {
			foundA = true
		}
		if it.Session == "" {
			foundLegacy = true
		}
	}
	if !foundA {
		t.Error("thread A's own memory missing from its view")
	}
	if !foundLegacy {
		t.Error("sessionless (legacy) item missing — should stay globally visible")
	}

	// Thread B's view: symmetric.
	b := s.RecentForSession(10, "discord:threadB")
	for _, it := range b {
		if it.Session == "discord:threadA" {
			t.Fatalf("thread A memory leaked into thread B's view: %s", it.Text)
		}
	}

	// Plain Recent keeps the old global semantics (used by tests/tools).
	r := s.Recent(10)
	if len(r) != 3 {
		t.Fatalf("Recent global view should see all 3 items, got %d", len(r))
	}
}
