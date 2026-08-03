// Package prompt provides versioned, hot-reloadable system prompt management.
// Prompts are stored as files in a configurable directory with YAML
// frontmatter carrying metadata (version, description). The Registry watches
// file modification times and transparently reloads changed prompts without
// requiring a process restart.
package prompt

import "time"

// Prompt is a versioned system prompt loaded from the registry.
type Prompt struct {
	// Name is the unique identifier (derived from the filename without
	// extension, e.g. "planning" from "planning.md").
	Name string `json:"name"`
	// Version is a monotonically increasing integer declared in the file's
	// frontmatter. Consumers can compare versions to detect updates.
	Version int `json:"version"`
	// Description is a human-readable summary of the prompt's purpose.
	Description string `json:"description,omitempty"`
	// Content is the raw prompt text (everything after the frontmatter block).
	Content string `json:"content"`
	// UpdatedAt is the file's last modification time, used for hot-reload
	// detection.
	UpdatedAt time.Time `json:"updated_at"`
}
