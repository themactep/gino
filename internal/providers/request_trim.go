package providers

import (
	"fmt"
)

// maxRequestChars is the hard cap on the serialized request size (all
// message contents combined, in characters). Requests above this get an
// emergency structural trim before being sent. This guards the
// provider-layer retry path, which replays the SAME message slice on
// every attempt and on fallback recovery — replaying a request that
// already blew the model's context window can never succeed, and each
// retry re-ships the full oversized payload (observed: 118k-token
// requests retried 10x against a 131k-token server).
//
// 240k chars ~= 60-80k real tokens for JSON-dense tool output (tool
// output tokenizes ~2.5-3 chars/token; the agent-side chars/4 estimate
// undercounts). Well under any sane context window.
const maxRequestChars = 240_000

// stubNote is spliced in where history was elided.
const stubNote = "[Earlier conversation trimmed automatically to keep the request within the model's context window. Recent messages below carry the current state.]"

// msgChars estimates a message's serialized size: content plus tool-call
// arguments, which ride along in the JSON body.
func msgChars(m *Message) int {
	n := len(m.Content) + 16
	for _, img := range m.Images {
		n += len(img) + 32
	}
	for _, tc := range m.ToolCalls {
		n += len(tc.Name) + 16
		for _, v := range tc.Arguments {
			if s, ok := v.(string); ok {
				n += len(s)
			} else {
				n += 64
			}
		}
	}
	return n
}

// clampContent hard-trims a single message's content.
func clampContent(s string, limit int) string {
	if limit <= 0 {
		limit = 2000
	}
	if len(s) <= limit {
		return s
	}
	return s[:limit] + fmt.Sprintf("\n... [trimmed %d chars]", len(s)-limit)
}

// emergencyTrimMessages structurally trims an oversized message slice so
// the request fits within maxRequestChars:
//   - preserves ALL system messages (identity/instructions)
//   - preserves the trailing conversational tail (head+tail keep window)
//   - preserves tool-call/tool-result pairing (an assistant tool_calls
//     message and the tool results that answer it are kept or dropped
//     together — never orphaned)
//   - clamps individual fat messages (never below minClamp chars)
//   - replaces elided history with a short stub note
//
// It is intentionally structural (no LLM summarization): cheap,
// deterministic, and safe to run on every retry.
func emergencyTrimMessages(messages []Message) []Message {
	total := 0
	for i := range messages {
		total += msgChars(&messages[i])
	}
	if total <= maxRequestChars {
		return messages // healthy request, untouched
	}

	n := len(messages)
	head, tail := 0, n-1
	for head < n && messages[head].Role == "system" {
		head++
	}

	keep := make([]bool, n)
	for i := 0; i < head; i++ {
		keep[i] = true
	}
	budget := maxRequestChars
	for i := 0; i < head; i++ {
		budget -= msgChars(&messages[i])
	}

	// Head+tail keep window (grow from both ends while budget allows).
	for head <= tail && budget > 0 {
		progress := false
		if msgChars(&messages[head]) <= budget {
			keep[head] = true
			budget -= msgChars(&messages[head])
			head++
			progress = true
		}
		if head <= tail && msgChars(&messages[tail]) <= budget {
			keep[tail] = true
			budget -= msgChars(&messages[tail])
			tail--
			progress = true
		}
		if !progress {
			break
		}
	}

	// The live turn (last message) always ships — drop it and the bot
	// would respond to nothing. The clamp pass below shrinks it if huge.
	keep[n-1] = true

	// Tool pairing: an assistant message with tool_calls drags its
	// following tool results; a tool result drags its calling assistant
	// message. Run to fixpoint so chains keep their heads.
	for {
		changed := false
		for i := 0; i < n; i++ {
			if messages[i].Role == "assistant" && len(messages[i].ToolCalls) > 0 && keep[i] {
				for j := i + 1; j < n && messages[j].Role == "tool"; j++ {
					if !keep[j] {
						keep[j] = true
						changed = true
					}
				}
			}
			if messages[i].Role == "tool" && keep[i] {
				for j := i - 1; j >= 0 && messages[j].Role != "user"; j-- {
					if messages[j].Role == "assistant" && len(messages[j].ToolCalls) > 0 && !keep[j] {
						keep[j] = true
						changed = true
						break
					}
				}
			}
		}
		if !changed {
			break
		}
	}

	out := make([]Message, 0, n)
	stubbed := false
	for i := 0; i < n; i++ {
		if keep[i] {
			if stubbed {
				out = append(out, Message{Role: "user", Content: stubNote})
				stubbed = false
			}
			m := messages[i]
			// Orphan-call guard: a kept assistant tool_call whose result
			// was trimmed would 400 on strict servers ("tool_calls must
			// be followed by tool messages"). Downgrade to plain text.
			if m.Role == "assistant" && len(m.ToolCalls) > 0 {
				hasResult := i+1 < n && keep[i+1] && messages[i+1].Role == "tool"
				if !hasResult {
					if m.Content == "" {
						m.Content = "[tool call omitted — result trimmed for size]"
					}
					m.ToolCalls = nil
				}
			}
			out = append(out, m)
			continue
		}
		stubbed = true
	}
	if stubbed {
		out = append(out, Message{Role: "user", Content: stubNote})
	}

	// Final safety: clamp any remaining fat message (kept tail can still
	// exceed the cap on its own).
	const minClamp = 2000
	total = 0
	for i := range out {
		total += msgChars(&out[i])
	}
	if total > maxRequestChars {
		per := maxRequestChars / (len(out) + 1)
		if per < minClamp {
			per = minClamp
		}
		for i := range out {
			if len(out[i].Images) > 0 && msgChars(&out[i]) > per {
				out[i].Images = nil
				out[i].Content = clampContent(out[i].Content, per) + "\n[image(s) omitted for size]"
			} else {
				out[i].Content = clampContent(out[i].Content, per)
			}
		}
	}

	return out
}
