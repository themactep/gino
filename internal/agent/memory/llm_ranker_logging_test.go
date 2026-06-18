package memory

import (
	"bytes"
	"context"
	"log"
	"testing"

	"github.com/wltechblog/gino/internal/providers"
)

// provider that returns a simple content response
type loggingFakeProvider struct {
	resp string
}

func (f *loggingFakeProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, model string) (providers.LLMResponse, error) {
	return providers.LLMResponse{Content: f.resp, HasToolCalls: false}, nil
}
func (f *loggingFakeProvider) GetDefaultModel() string { return "m" }

func TestLLMRankerLogsRequestsAndResponses(t *testing.T) {
	buf := &bytes.Buffer{}
	logger := log.New(buf, "ranker: ", 0)
	p := &loggingFakeProvider{resp: "Result: [1,0]"}
	r := NewLLMRankerWithLogger(p, "m", logger)
	mems := []MemoryItem{{Kind: "short", Text: "a"}, {Kind: "short", Text: "b"}}
	_ = r.Rank("query", mems, 2)
	out := buf.String()
	if out == "" {
		t.Fatalf("expected log output, got empty")
	}
	if !bytes.Contains(buf.Bytes(), []byte("sending ranking request")) {
		t.Fatalf("expected sending log, got: %s", out)
	}
	if !bytes.Contains(buf.Bytes(), []byte("provider returned content")) {
		t.Fatalf("expected response log, got: %s", out)
	}
}
