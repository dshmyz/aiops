package capabilities

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
	"gopkg.in/yaml.v3"
)

func LoadPublished(root string) ([]Capability, error) {
	publishedDir := filepath.Join(root, "published")
	info, err := os.Stat(publishedDir)
	if err != nil {
		return nil, fmt.Errorf("stat published directory %s: %w", publishedDir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("published directory %s is not a directory", publishedDir)
	}

	paths, err := filepath.Glob(filepath.Join(publishedDir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	seen := map[string]string{}
	loaded := make([]Capability, 0, len(paths))
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read capability %s: %w", path, err)
		}
		var capability Capability
		if err := yaml.Unmarshal(body, &capability); err != nil {
			return nil, fmt.Errorf("parse capability %s: %w", path, err)
		}
		if capability.Status != StatusPublished {
			return nil, fmt.Errorf("published file %s has status %q", path, capability.Status)
		}
		if err := Validate(capability); err != nil {
			return nil, fmt.Errorf("validate capability %s: %w", path, err)
		}
		if _, exists := tools.Lookup(capability.Name); exists {
			return nil, fmt.Errorf("published capability %q conflicts with an existing tool", capability.Name)
		}
		if previous, ok := seen[capability.Name]; ok {
			return nil, fmt.Errorf("duplicate capability %q in %s and %s", capability.Name, previous, path)
		}
		seen[capability.Name] = path
		loaded = append(loaded, capability)
	}
	return loaded, nil
}
