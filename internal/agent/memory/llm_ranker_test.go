package memory

import (
	"context"
	"testing"

	"github.com/wltechblog/gino/internal/providers"
)

// fake provider that returns the content it was asked to return
type fakeProvider struct {
	resp  string
	calls []providers.ToolCall
}

func (f *fakeProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string) (providers.LLMResponse, error) {
	if len(f.calls) > 0 {
		return providers.LLMResponse{Content: "", HasToolCalls: true, ToolCalls: f.calls}, nil
	}
	return providers.LLMResponse{Content: f.resp, HasToolCalls: false}, nil
}
func (f *fakeProvider) GetDefaultModel() string { return "test-model" }

func TestLLMRankerUsesProvider(t *testing.T) {
	mems := []MemoryItem{{Kind: "short", Text: "buy milk"}, {Kind: "short", Text: "call mom"}}
	p := &fakeProvider{calls: []providers.ToolCall{{ID: "1", Name: "rank_memories", Arguments: map[string]interface{}{"indices": []int{1, 0}}}}}
	r := NewLLMRanker(p, "test-model")
	res := r.Rank("milk", mems, 2)
	if len(res) != 2 {
		t.Fatalf("expected 2 results, got %d", len(res))
	}
	if res[0].Text != "call mom" {
		t.Fatalf("expected first result to be 'call mom', got %q", res[0].Text)
	}
}

func TestLLMRankerFallsBackOnBadResponse(t *testing.T) {
	mems := []MemoryItem{{Kind: "short", Text: "buy milk"}, {Kind: "short", Text: "call mom"}}
	p := &fakeProvider{resp: "no-json-here"}
	r := NewLLMRanker(p, "test-model")
	res := r.Rank("milk", mems, 2)
	// fallback should return most recent-first by default (SimpleRanker behavior)
	if len(res) != 2 {
		t.Fatalf("expected 2 results, got %d", len(res))
	}
}

func TestLLMRankerParsesFloatIndicesFromToolCall(t *testing.T) {
	mems := []MemoryItem{{Kind: "short", Text: "buy milk"}, {Kind: "short", Text: "call mom"}, {Kind: "long", Text: "big fact"}}
	// provider returns indices as []float64 (common when unmarshalling JSON numbers)
	p := &fakeProvider{calls: []providers.ToolCall{{ID: "1", Name: "rank_memories", Arguments: map[string]interface{}{"indices": []float64{2, 0}}}}}
	r := NewLLMRanker(p, "test-model")
	res := r.Rank("milk", mems, 2)
	if len(res) != 2 {
		t.Fatalf("expected 2 results, got %d", len(res))
	}
	if res[0].Text != "big fact" {
		t.Fatalf("expected first result to be 'big fact', got %q", res[0].Text)
	}
}

func TestLLMRankerParsesArrayFromContentText(t *testing.T) {
	mems := []MemoryItem{{Kind: "short", Text: "buy milk"}, {Kind: "short", Text: "call mom"}}
	p := &fakeProvider{resp: "Result: [1,0]"}
	r := NewLLMRanker(p, "test-model")
	res := r.Rank("milk", mems, 2)
	if len(res) != 2 {
		t.Fatalf("expected 2 results, got %d", len(res))
	}
	if res[0].Text != "call mom" {
		t.Fatalf("expected first result to be 'call mom', got %q", res[0].Text)
	}
}
