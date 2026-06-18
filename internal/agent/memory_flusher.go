package agent

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/wltechblog/gino/internal/agent/memory"
	"github.com/wltechblog/gino/internal/providers"
)

// memoryFlusher extracts important facts from conversation messages and saves
// them to the MemoryStore before compaction discards the original messages.
type memoryFlusher struct {
	provider providers.LLMProvider
	model    string
	store    *memory.MemoryStore
}

// newMemoryFlusher creates a flusher that uses the LLM to extract facts and the
// MemoryStore to persist them.
func newMemoryFlusher(provider providers.LLMProvider, model string, store *memory.MemoryStore) *memoryFlusher {
	return &memoryFlusher{
		provider: provider,
		model:    model,
		store:    store,
	}
}

// FlushToMemory extracts key facts from messages using a lightweight LLM call
// and appends them to today's memory notes.
func (f *memoryFlusher) FlushToMemory(ctx context.Context, messages []providers.Message) error {
	if len(messages) == 0 {
		return nil
	}

	facts, err := f.extractFacts(ctx, messages)
	if err != nil {
		return fmt.Errorf("fact extraction failed: %w", err)
	}

	if facts == "" {
		return nil
	}

	// Save extracted facts to today's notes with a compaction marker
	entry := fmt.Sprintf("[compaction-flush] %s", facts)
	if err := f.store.AppendToday(entry); err != nil {
		return fmt.Errorf("failed to save facts: %w", err)
	}

	log.Printf("Memory flush: saved extracted facts (%d chars)", len(facts))
	return nil
}

// extractFacts calls the LLM to extract facts worth remembering from a message chain.
func (f *memoryFlusher) extractFacts(ctx context.Context, messages []providers.Message) (string, error) {
	// Build a compact text representation of the messages
	var sb strings.Builder
	sb.WriteString("Conversation segment being compacted:\n\n")
	for i, m := range messages {
		content := m.Content
		if content == "" && len(m.ToolCalls) > 0 {
			var names []string
			for _, tc := range m.ToolCalls {
				names = append(names, tc.Name)
			}
			content = fmt.Sprintf("[Called tools: %s]", strings.Join(names, ", "))
		}
		// Truncate long messages to keep the extraction prompt small
		if len(content) > 1500 {
			content = content[:1500] + "..."
		}
		sb.WriteString(fmt.Sprintf("[%d] %s: %s\n", i, m.Role, content))
	}

	prompt := `You are a memory extraction system. Your job is to identify facts from the conversation that are worth remembering long-term.

Extract ONLY facts that are:
1. User preferences or explicit instructions
2. Important decisions or conclusions reached
3. Key file paths, URLs, or identifiers mentioned
4. Project details or configuration values
5. Error patterns discovered and their fixes
6. Dates, versions, or specific numbers that matter

Do NOT extract:
- Conversational filler or greetings
- Tool call details that are routine (file reads, etc.)
- Information that would be obvious from the project structure
- Anything redundant with what's already in memory

Output format: one fact per line, each starting with "- ". If there are no facts worth extracting, output exactly "NONE". Be concise — each fact should be a single line.`

	extractMessages := []providers.Message{
		{Role: "system", Content: prompt},
		{Role: "user", Content: sb.String()},
	}

	resp, err := f.provider.Chat(ctx, extractMessages, nil, f.model)
	if err != nil {
		return "", err
	}

	result := strings.TrimSpace(resp.Content)
	if result == "NONE" || result == "" {
		return "", nil
	}

	return result, nil
}
