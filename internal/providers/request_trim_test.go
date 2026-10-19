package providers

import (
	"strings"
	"testing"
)

func TestEmergencyTrimTrimsFatHistoryPreservingSystem(t *testing.T) {
	sys := Message{Role: "system", Content: strings.Repeat("S", 5000)}
	fat := Message{Role: "user", Content: strings.Repeat("F", 300_000)}
	recent := Message{Role: "user", Content: strings.Repeat("R", 2000)}
	msgs := []Message{sys, fat, recent}

	out := emergencyTrimMessages(msgs)

	total := 0
	for i := range out {
		total += msgChars(&out[i])
	}
	if total > maxRequestChars {
		t.Fatalf("total=%d still over cap %d", total, maxRequestChars)
	}
	if out[0].Content != sys.Content {
		t.Fatalf("system message not preserved (len %d)", len(out[0].Content))
	}
	if out[len(out)-1].Content != recent.Content {
		t.Fatalf("recent tail not preserved")
	}
	foundStub := false
	for _, m := range out {
		if strings.Contains(m.Content, "trimmed automatically") {
			foundStub = true
		}
	}
	if !foundStub {
		t.Fatalf("elided-history stub note missing")
	}
}

func TestEmergencyTrimPreservesToolPairing(t *testing.T) {
	sys := Message{Role: "system", Content: strings.Repeat("S", 3000)}
	tc := Message{Role: "assistant", ToolCalls: []ToolCall{{
		ID: "call-1", Name: "web",
		Arguments: map[string]interface{}{"url": "http://x/" + strings.Repeat("a", 500)},
	}}}
	res := Message{Role: "tool", ToolCallID: "call-1", Content: strings.Repeat("T", 100_000)}
	pair2 := Message{Role: "assistant", ToolCalls: []ToolCall{{
		ID: "call-2", Name: "exec",
		Arguments: map[string]interface{}{"cmd": "ls"},
	}}}
	res2 := Message{Role: "tool", ToolCallID: "call-2", Content: strings.Repeat("U", 150_000)}
	tail := Message{Role: "user", Content: "go on"}
	msgs := []Message{sys, tc, res, pair2, res2, tail}

	out := emergencyTrimMessages(msgs)
	// Every tool result must still be preceded by its calling assistant.
	for i, m := range out {
		if m.Role == "tool" {
			ok := false
			for j := i - 1; j >= 0; j-- {
				if out[j].Role == "assistant" && len(out[j].ToolCalls) > 0 {
					for _, c := range out[j].ToolCalls {
						if c.ID == m.ToolCallID {
							ok = true
						}
					}
					break
				}
			}
			if !ok {
				t.Fatalf("orphaned tool result id=%s", m.ToolCallID)
			}
		}
	}
	total := 0
	for i := range out {
		total += msgChars(&out[i])
	}
	if total > maxRequestChars {
		t.Fatalf("total=%d over cap", total)
	}
}

func TestEmergencyTrimNoopOnHealthyRequest(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: strings.Repeat("S", 1000)},
		{Role: "user", Content: strings.Repeat("U", 5000)},
	}
	orig := make([]string, len(msgs))
	for i, m := range msgs {
		orig[i] = m.Content
	}
	out := emergencyTrimMessages(msgs)
	if len(out) != len(msgs) {
		t.Fatalf("healthy request changed message count: %d -> %d", len(msgs), len(out))
	}
	for i := range out {
		if out[i].Content != orig[i] {
			t.Fatalf("healthy request message %d modified", i)
		}
	}
}

func TestEmergencyTrimClampsSingleHugeTail(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: strings.Repeat("S", 1000)},
		{Role: "user", Content: strings.Repeat("X", 400_000)},
	}
	out := emergencyTrimMessages(msgs)
	total := 0
	for i := range out {
		total += msgChars(&out[i])
	}
	if total > maxRequestChars {
		t.Fatalf("single-huge-tail total=%d over cap", total)
	}
	if len(out[1].Content) >= 400_000 {
		t.Fatalf("fat tail not clamped (len=%d)", len(out[1].Content))
	}
}

func TestEmergencyTrimStripsOversizedImages(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: strings.Repeat("S", 1000)},
		{Role: "user", Content: "look at this", Images: []string{"data:image/png;base64," + strings.Repeat("A", 400_000)}},
	}
	out := emergencyTrimMessages(msgs)
	total := 0
	imgLeft := 0
	for i := range out {
		total += msgChars(&out[i])
		imgLeft += len(out[i].Images)
	}
	if total > maxRequestChars {
		t.Fatalf("image-bloat total=%d over cap", total)
	}
	if imgLeft != 0 {
		t.Fatalf("oversized images not stripped (%d bytes left)", imgLeft)
	}
}
