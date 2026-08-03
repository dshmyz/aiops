package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
)

// Finding is one problem found in one file. Errors fail the CI job; warnings
// are advisory and only surface in the report.
type Finding struct {
	Check   string `json:"check"`
	Message string `json:"message"`
}

// FileReport accumulates every check's findings for one file.
type FileReport struct {
	Errors   []Finding `json:"errors,omitempty"`
	Warnings []Finding `json:"warnings,omitempty"`
}

// Report is the shared state across checks in one CI job. Each check runs as a
// separate process, so the report is serialized to disk between steps.
type Report struct {
	Files map[string]*FileReport `json:"files"`
}

// LoadReport reads accumulated findings, starting fresh when no state exists.
func LoadReport(path string) (*Report, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Report{Files: map[string]*FileReport{}}, nil
	}
	if err != nil {
		return nil, err
	}
	report := &Report{}
	if err := json.Unmarshal(body, report); err != nil {
		// Corrupt state should not wedge the job; start over rather than fail.
		return &Report{Files: map[string]*FileReport{}}, nil
	}
	if report.Files == nil {
		report.Files = map[string]*FileReport{}
	}
	return report, nil
}

// Save persists the report so the next check in the job can append to it.
func (r *Report) Save(path string) error {
	body, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0o600)
}

func (r *Report) file(path string) *FileReport {
	if r.Files == nil {
		r.Files = map[string]*FileReport{}
	}
	if _, ok := r.Files[path]; !ok {
		r.Files[path] = &FileReport{}
	}
	return r.Files[path]
}

// Errorf records a blocking finding and echoes it so the CI log shows the
// failure at the step that found it, not only in the final report.
func (r *Report) Errorf(path, check, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	entry := r.file(path)
	entry.Errors = append(entry.Errors, Finding{Check: check, Message: message})
	fmt.Printf("FAIL %s: %s\n", path, message)
}

// Warnf records an advisory finding.
func (r *Report) Warnf(path, check, format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	entry := r.file(path)
	entry.Warnings = append(entry.Warnings, Finding{Check: check, Message: message})
	fmt.Printf("WARN %s: %s\n", path, message)
}

// HasErrors reports whether the file already failed an earlier check. Later
// checks use this to skip work that would only produce cascading noise.
func (r *Report) HasErrors(path string) bool {
	entry, ok := r.Files[path]
	return ok && len(entry.Errors) > 0
}

func (r *Report) sortedPaths() []string {
	paths := make([]string, 0, len(r.Files))
	for path := range r.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths
}

// writeReport renders the accumulated findings as markdown for a PR comment.
func writeReport(report *Report, output string) error {
	paths := report.sortedPaths()
	passed, failed, warnings := 0, 0, 0
	for _, path := range paths {
		entry := report.Files[path]
		if len(entry.Errors) > 0 {
			failed++
		} else {
			passed++
		}
		warnings += len(entry.Warnings)
	}

	var body []byte
	appendf := func(format string, args ...any) {
		body = append(body, fmt.Sprintf(format, args...)...)
	}

	appendf("### Summary\n\n")
	appendf("- Passed: **%d**\n", passed)
	appendf("- Failed: **%d**\n", failed)
	appendf("- Warnings: **%d**\n\n", warnings)

	if failed > 0 {
		appendf("### Failures\n\n")
		for _, path := range paths {
			entry := report.Files[path]
			if len(entry.Errors) == 0 {
				continue
			}
			appendf("#### `%s`\n\n", path)
			for _, finding := range entry.Errors {
				appendf("- **%s**: %s\n", finding.Check, finding.Message)
			}
			appendf("\n")
		}
	}

	if warnings > 0 {
		appendf("### Warnings\n\n")
		for _, path := range paths {
			entry := report.Files[path]
			if len(entry.Warnings) == 0 {
				continue
			}
			appendf("#### `%s`\n\n", path)
			for _, finding := range entry.Warnings {
				appendf("- **%s**: %s\n", finding.Check, finding.Message)
			}
			appendf("\n")
		}
	}

	if failed == 0 {
		appendf("All capability files passed validation.\n")
	}

	if err := os.WriteFile(output, body, 0o644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	fmt.Printf("report written to %s\n", output)
	return nil
}
