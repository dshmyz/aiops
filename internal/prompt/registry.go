package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Registry loads and caches prompts from a directory. It supports hot-reload:
// on each Get call the file's modification time is checked and the prompt is
// re-parsed if the file changed on disk. This allows operators to update
// prompts without restarting the service.
//
// File format: Markdown with a simple frontmatter block delimited by "---":
//
//	---
//	version: 2
//	description: Eino planner system prompt
//	---
//	<prompt content here>
//
// The filename (without .md extension) becomes the prompt Name.
type Registry struct {
	dir string

	mu      sync.RWMutex
	prompts map[string]*cachedPrompt
}

type cachedPrompt struct {
	prompt  Prompt
	modTime time.Time
}

// NewRegistry creates a registry backed by the given directory. It performs an
// initial load of all .md files; individual parse errors are skipped (logged
// by the caller via LoadAll's return value).
func NewRegistry(dir string) *Registry {
	return &Registry{
		dir:     dir,
		prompts: make(map[string]*cachedPrompt),
	}
}

// LoadAll scans the directory for .md files and loads them into the cache.
// Returns the number of prompts successfully loaded and any error that
// prevented reading the directory itself.
func (r *Registry) LoadAll() (int, error) {
	entries, err := os.ReadDir(r.dir)
	if err != nil {
		return 0, fmt.Errorf("read prompt directory: %w", err)
	}
	count := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".md")
		if _, err := r.loadFile(name); err != nil {
			continue // skip unparseable files
		}
		count++
	}
	return count, nil
}

// Get returns the prompt with the given name. If the underlying file has been
// modified since the last load, it is transparently re-parsed (hot-reload).
// Returns ok=false when the prompt does not exist.
func (r *Registry) Get(name string) (Prompt, bool) {
	r.mu.RLock()
	cached, exists := r.prompts[name]
	r.mu.RUnlock()

	if !exists {
		// Try loading from disk (first access or file created after startup).
		p, err := r.loadFile(name)
		if err != nil {
			return Prompt{}, false
		}
		return p, true
	}

	// Check mtime for hot-reload.
	path := r.pathFor(name)
	info, err := os.Stat(path)
	if err != nil {
		// File deleted: return cached version.
		return cached.prompt, true
	}
	if info.ModTime().After(cached.modTime) {
		p, err := r.loadFile(name)
		if err != nil {
			return cached.prompt, true // parse error: keep old version
		}
		return p, true
	}
	return cached.prompt, true
}

// List returns all currently cached prompts sorted by name.
func (r *Registry) List() []Prompt {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Prompt, 0, len(r.prompts))
	for _, c := range r.prompts {
		out = append(out, c.prompt)
	}
	// Simple insertion sort (few prompts expected).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].Name < out[j-1].Name; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// Save writes a prompt to disk in the frontmatter format and updates the
// cache. Used by the admin API to create or update prompts at runtime.
func (r *Registry) Save(p Prompt) error {
	if p.Name == "" {
		return fmt.Errorf("prompt name is required")
	}
	if p.Version <= 0 {
		p.Version = 1
	}
	var sb strings.Builder
	sb.WriteString("---\n")
	sb.WriteString("version: " + strconv.Itoa(p.Version) + "\n")
	if p.Description != "" {
		sb.WriteString("description: " + p.Description + "\n")
	}
	sb.WriteString("---\n")
	sb.WriteString(p.Content)

	path := r.pathFor(p.Name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create prompt directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(sb.String()), 0o644); err != nil {
		return fmt.Errorf("write prompt file: %w", err)
	}
	// Reload from disk to get accurate mtime.
	_, err := r.loadFile(p.Name)
	return err
}

func (r *Registry) pathFor(name string) string {
	return filepath.Join(r.dir, name+".md")
}

func (r *Registry) loadFile(name string) (Prompt, error) {
	path := r.pathFor(name)
	data, err := os.ReadFile(path)
	if err != nil {
		return Prompt{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return Prompt{}, err
	}
	p, err := parsePrompt(name, string(data))
	if err != nil {
		return Prompt{}, err
	}
	p.UpdatedAt = info.ModTime()

	r.mu.Lock()
	r.prompts[name] = &cachedPrompt{prompt: p, modTime: info.ModTime()}
	r.mu.Unlock()
	return p, nil
}

// parsePrompt extracts frontmatter metadata and content from raw file text.
func parsePrompt(name, raw string) (Prompt, error) {
	p := Prompt{Name: name, Version: 1}
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "---\n") {
		// No frontmatter: entire file is content.
		p.Content = raw
		return p, nil
	}
	// Find closing "---".
	rest := trimmed[4:] // skip opening "---\n"
	endIdx := strings.Index(rest, "\n---\n")
	if endIdx < 0 {
		// Malformed: treat entire file as content.
		p.Content = raw
		return p, nil
	}
	frontmatter := rest[:endIdx]
	p.Content = strings.TrimLeft(rest[endIdx+5:], "\n")

	// Parse simple key: value pairs from frontmatter.
	for _, line := range strings.Split(frontmatter, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "version":
			if v, err := strconv.Atoi(value); err == nil && v > 0 {
				p.Version = v
			}
		case "description":
			p.Description = value
		}
	}
	return p, nil
}
