package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	memory "github.com/wltechblog/gino/internal/agent/memory"
)

// writeTestSkill writes a SKILL.md into the profile workspace so the loader
// picks it up.
func writeTestSkill(t *testing.T, dir, name, description, content string) {
	t.Helper()
	skillDir := filepath.Join(dir, "skills", name)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\nname: " + name + "\ndescription: " + description + "\n---\n\n" + content
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestSkillsIndexedNotInlined pins the prompt diet: the stable system prompt
// carries only a skill INDEX (name + description + read_skill pointer).
// Full SKILL.md bodies must never be inlined - with 15 skills that added
// ~56KB to every call.
func TestSkillsIndexedNotInlined(t *testing.T) {
	dir := t.TempDir()
	writeTestSkill(t, dir, "alpha", "does alpha things", "SECRET-ALPHA-BODY "+strings.Repeat("x", 400))
	writeTestSkill(t, dir, "beta", "does beta things", "SECRET-BETA-BODY "+strings.Repeat("y", 400))

	cb := NewContextBuilderWithProfile(".", dir, memory.NewSimpleRanker(), 5)
	msgs := cb.BuildMessages(nil, "hello", "telegram", "123", "", "", nil, nil)

	var system string
	for _, m := range msgs {
		if m.Role == "system" {
			system = m.Content
			break
		}
	}
	idx := strings.Index(system, "Available Skills")
	if idx < 0 {
		t.Fatalf("skills index missing from system prompt:\n%.300s", system)
	}
	block := system[idx:]
	if !strings.Contains(block, "read_skill") {
		t.Error("index must point at the read_skill tool")
	}
	for _, needle := range []string{"alpha", "beta"} {
		if !strings.Contains(block, needle) {
			t.Errorf("index missing skill %q", needle)
		}
		if !strings.Contains(block, needle+" things") {
			t.Errorf("index missing description for %q", needle)
		}
	}
	for _, secret := range []string{"SECRET-ALPHA-BODY", "SECRET-BETA-BODY"} {
		if strings.Contains(system, secret) {
			t.Errorf("skill body leaked into system prompt: %s", secret)
		}
	}
}
