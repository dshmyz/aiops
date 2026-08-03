package prompt_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/prompt"
)

func TestRegistryLoadAllAndGet(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writePromptFile(t, dir, "planning.md", `---
version: 3
description: test planning prompt
---
You are a planner.`)
	writePromptFile(t, dir, "summarizer.md", `---
version: 1
---
Summarize the conversation.`)

	reg := prompt.NewRegistry(dir)
	count, err := reg.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if count != 2 {
		t.Fatalf("loaded %d prompts, want 2", count)
	}

	p, ok := reg.Get("planning")
	if !ok {
		t.Fatal("Get(planning) not found")
	}
	if p.Version != 3 {
		t.Errorf("version = %d, want 3", p.Version)
	}
	if p.Description != "test planning prompt" {
		t.Errorf("description = %q", p.Description)
	}
	if p.Content != "You are a planner." {
		t.Errorf("content = %q", p.Content)
	}
	if p.Name != "planning" {
		t.Errorf("name = %q, want planning", p.Name)
	}
}

func TestRegistryGetNoFrontmatter(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writePromptFile(t, dir, "raw.md", "Just plain prompt text without frontmatter.")

	reg := prompt.NewRegistry(dir)
	p, ok := reg.Get("raw")
	if !ok {
		t.Fatal("Get(raw) not found")
	}
	if p.Version != 1 {
		t.Errorf("version = %d, want default 1", p.Version)
	}
	if p.Content != "Just plain prompt text without frontmatter." {
		t.Errorf("content = %q", p.Content)
	}
}

func TestRegistryHotReload(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "planning.md")
	writePromptFile(t, dir, "planning.md", `---
version: 1
---
Old content.`)

	reg := prompt.NewRegistry(dir)
	p, _ := reg.Get("planning")
	if p.Content != "Old content." {
		t.Fatalf("initial content = %q", p.Content)
	}

	// Ensure mtime advances (some filesystems have 1s granularity).
	time.Sleep(50 * time.Millisecond)
	if err := os.WriteFile(path, []byte("---\nversion: 2\n---\nNew content."), 0o644); err != nil {
		t.Fatal(err)
	}
	// Force mtime difference.
	future := time.Now().Add(2 * time.Second)
	_ = os.Chtimes(path, future, future)

	p, ok := reg.Get("planning")
	if !ok {
		t.Fatal("Get(planning) not found after update")
	}
	if p.Version != 2 {
		t.Errorf("version = %d, want 2 after hot-reload", p.Version)
	}
	if p.Content != "New content." {
		t.Errorf("content = %q, want hot-reloaded", p.Content)
	}
}

func TestRegistrySave(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	reg := prompt.NewRegistry(dir)

	err := reg.Save(prompt.Prompt{
		Name:        "new-prompt",
		Version:     5,
		Description: "saved via API",
		Content:     "Hello from admin.",
	})
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify file was written.
	data, err := os.ReadFile(filepath.Join(dir, "new-prompt.md"))
	if err != nil {
		t.Fatalf("read saved file: %v", err)
	}
	content := string(data)
	if !contains(content, "version: 5") || !contains(content, "Hello from admin.") {
		t.Fatalf("saved file = %q", content)
	}

	// Verify it's retrievable.
	p, ok := reg.Get("new-prompt")
	if !ok {
		t.Fatal("Get(new-prompt) not found after Save")
	}
	if p.Version != 5 || p.Content != "Hello from admin." {
		t.Fatalf("p = %+v", p)
	}
}

func TestRegistryList(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writePromptFile(t, dir, "beta.md", "---\nversion: 1\n---\nB")
	writePromptFile(t, dir, "alpha.md", "---\nversion: 2\n---\nA")

	reg := prompt.NewRegistry(dir)
	_, _ = reg.LoadAll()

	list := reg.List()
	if len(list) != 2 {
		t.Fatalf("list len = %d, want 2", len(list))
	}
	if list[0].Name != "alpha" || list[1].Name != "beta" {
		t.Fatalf("list order = [%s, %s], want [alpha, beta]", list[0].Name, list[1].Name)
	}
}

func TestRegistryGetMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	reg := prompt.NewRegistry(dir)
	_, ok := reg.Get("nonexistent")
	if ok {
		t.Fatal("Get(nonexistent) should return false")
	}
}

func writePromptFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
