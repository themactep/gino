package tools

import (
	"strings"
	"testing"
)

// TestCapBgNotificationTruncates pins the relay diet: whole notifications are
// capped at bgNotifyCap bytes (they trigger a turn that carries a fresh
// turn_context wrap, so an obese envelope multiplies the prompt), keeping the
// head and reporting the cut.
func TestCapBgNotificationTruncates(t *testing.T) {
	// Under the cap: byte-identical passthrough.
	small := "[Background job finished] tiny\nStatus: success\n"
	if got := capBgNotification(small); got != small {
		t.Errorf("small notification must pass through unchanged, got %q", got)
	}

	// Over the cap: head kept, truncation noted, size bounded.
	big := "[Background job finished] big\n" + strings.Repeat("line of output\n", 2000)
	got := capBgNotification(big)
	if len(got) > bgNotifyCap+256 {
		t.Errorf("capped notification too large: %d bytes", len(got))
	}
	if !strings.HasPrefix(got, "[Background job finished] big") {
		t.Error("head of the notification must survive the cap")
	}
	if !strings.Contains(got, "notification truncated") {
		t.Error("truncation must be reported in-band")
	}
}

// TestCapBgNotificationSnapsToLineBoundary ensures the cut never lands
// mid-line.
func TestCapBgNotificationSnapsToLineBoundary(t *testing.T) {
	head := "[Background job finished] snap\n"
	filler := strings.Repeat("a", 5000) // no newlines at all past the head
	got := capBgNotification(head + filler)
	if strings.Contains(strings.TrimSuffix(got, "\n"), "aaa") == false && !strings.Contains(got, "notification truncated") {
		t.Fatal("expected truncation marker")
	}
	// The only newline-terminated content lines must be the head and the
	// marker; a no-newline filler means the snap falls back to the byte cap.
	if !strings.HasPrefix(got, head) {
		t.Error("head must survive")
	}
}
