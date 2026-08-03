package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// runValidateSyntax parses each file as YAML with strict key matching, so a
// typo'd or unknown field is caught here rather than silently ignored and
// producing a capability that behaves differently than the author intended.
func runValidateSyntax(files []string, report *Report) error {
	failed := false
	for _, path := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			report.Errorf(path, "validate-syntax", "read file: %v", err)
			failed = true
			continue
		}
		decoder := yaml.NewDecoder(strings.NewReader(string(body)))
		decoder.KnownFields(true)
		var capability capabilities.Capability
		if err := decoder.Decode(&capability); err != nil {
			report.Errorf(path, "validate-syntax", "%v", err)
			failed = true
			continue
		}
		fmt.Printf("OK   %s: valid capability YAML\n", path)
	}
	if failed {
		return errors.New("YAML syntax validation failed")
	}
	return nil
}

// runValidateSchema runs the exact validator the runtime loader runs. Anything
// it rejects would fail at service startup, so it must fail the PR.
func runValidateSchema(files []string, report *Report) error {
	failed := false
	for _, path := range files {
		capability, ok := decodeFile(path, "validate-schema", report)
		if !ok {
			failed = true
			continue
		}
		if err := capabilities.Validate(capability); err != nil {
			report.Errorf(path, "validate-schema", "%v", err)
			failed = true
			continue
		}
		// ToTool is what the runtime uses to register the capability as a tool;
		// a capability that validates but cannot be registered is still broken.
		if _, err := capabilities.ToTool(capability); err != nil {
			report.Errorf(path, "validate-schema", "cannot register as a tool: %v", err)
			failed = true
			continue
		}
		fmt.Printf("OK   %s: schema valid (%s, %s/%s)\n", path, capability.Name, capability.Operation, capability.Risk)
	}
	if failed {
		return errors.New("capability schema validation failed")
	}
	return nil
}

// runCheckRequired enforces conventions the runtime tolerates but that make a
// capability unusable in practice: no AI description means the planner cannot
// pick it, no examples means the operator gets no guidance, and a high-risk
// write with no rollback source is a change nobody can undo.
func runCheckRequired(files []string, report *Report) error {
	failed := false
	for _, path := range files {
		capability, ok := decodeFile(path, "check-required", report)
		if !ok {
			failed = true
			continue
		}

		if strings.TrimSpace(capability.AI.Description) == "" {
			report.Errorf(path, "check-required", "ai.description is required: the planner selects capabilities by description")
			failed = true
		} else if len([]rune(capability.AI.Description)) < 10 {
			report.Warnf(path, "check-required", "ai.description is very short; the planner may not match it reliably")
		}
		if len(capability.AI.Examples) == 0 {
			report.Warnf(path, "check-required", "ai.examples is empty; add at least one natural-language example")
		}
		if capability.Backend.TimeoutMS <= 0 {
			report.Warnf(path, "check-required", "backend.timeout_ms is unset; the adapter default will be used")
		}

		if capability.Operation == tools.Write {
			if capability.Risk == tools.High && strings.TrimSpace(capability.Governance.Rollback.Source) == "" {
				report.Errorf(path, "check-required", "high-risk write requires governance.rollback.source so the change can be undone")
				failed = true
			}
			if capability.Verify == nil {
				report.Warnf(path, "check-required", "write capability has no verify spec; the operator cannot confirm the change took effect")
			}
		}

		// Numeric inputs without bounds are the common cause of a destructive
		// write: nothing stops a retention of 0 or a replica count of 10000.
		for name, field := range capability.InputSchema {
			if field.Type == "integer" && field.Min == nil && field.Max == nil {
				report.Warnf(path, "check-required", "input %q is an unbounded integer; declare min/max if an out-of-range value would be destructive", name)
			}
		}

		if !report.HasErrors(path) {
			fmt.Printf("OK   %s: conventions satisfied\n", path)
		}
	}
	if failed {
		return errors.New("required-field check failed")
	}
	return nil
}

// secretPattern is one credential shape worth blocking in a committed file.
type secretPattern struct {
	name    string
	pattern *regexp.Regexp
}

var secretPatterns = []secretPattern{
	{"AWS access key ID", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"private key block", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	{"bearer token", regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9\-._~+/]{20,}`)},
	{"JWT", regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`)},
	{"assigned credential", regexp.MustCompile(`(?i)\b(password|passwd|secret|api[_-]?key|access[_-]?token|auth[_-]?token)\b\s*[:=]\s*\S{8,}`)},
}

// placeholderPattern matches the substitution forms a capability legitimately
// uses to reference a secret it does not contain.
var placeholderPattern = regexp.MustCompile(`\$\{[^}]*\}|\{\{[^}]*\}\}|\$[A-Z_][A-Z0-9_]*|\{[a-zA-Z0-9_]+\}`)

// runScanSecrets fails on credentials committed into capability YAML. Unlike the
// other checks this is a hard failure, not a warning: a leaked credential in
// git history cannot be walked back by editing the file.
func runScanSecrets(files []string, report *Report) error {
	failed := false
	for _, path := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			report.Errorf(path, "scan-secrets", "read file: %v", err)
			failed = true
			continue
		}
		clean := true
		for i, line := range strings.Split(string(body), "\n") {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			// A line whose value is entirely a placeholder references a secret
			// rather than containing one.
			if placeholderPattern.MatchString(valueOf(trimmed)) {
				continue
			}
			for _, sp := range secretPatterns {
				if sp.pattern.MatchString(line) {
					report.Errorf(path, "scan-secrets", "line %d: possible hardcoded %s; reference an environment variable instead", i+1, sp.name)
					failed = true
					clean = false
				}
			}
		}
		if clean {
			fmt.Printf("OK   %s: no hardcoded credentials detected\n", path)
		}
	}
	if failed {
		return errors.New("secret scan failed")
	}
	return nil
}

// valueOf returns the part of a `key: value` line after the first colon, so the
// placeholder check tests the value rather than the key name.
func valueOf(line string) string {
	if index := strings.Index(line, ":"); index >= 0 {
		return strings.TrimSpace(line[index+1:])
	}
	return line
}

// runDryRun exercises everything that can be checked without calling the
// backend: the base URL parses, path variables resolve against a synthetic
// input, and the declared dependency graph is resolvable across the whole
// change set.
func runDryRun(files []string, report *Report) error {
	failed := false
	loaded := make([]capabilities.Capability, 0, len(files))
	for _, path := range files {
		capability, ok := decodeFile(path, "dry-run", report)
		if !ok {
			failed = true
			continue
		}
		loaded = append(loaded, capability)

		if raw := strings.TrimSpace(capability.Backend.BaseURL); raw != "" {
			parsed, err := url.Parse(raw)
			if err != nil || !parsed.IsAbs() || parsed.Host == "" {
				report.Errorf(path, "dry-run", "backend.base_url %q is not an absolute URL", raw)
				failed = true
			}
		} else if capability.Status == capabilities.StatusPublished {
			report.Errorf(path, "dry-run", "published capability requires backend.base_url")
			failed = true
		}

		// Render the backend path with a synthetic input built from the schema.
		// A path variable that the schema declares but cannot satisfy fails here.
		input := syntheticInput(capability)
		if rendered, err := renderPath(capability.Backend.Path, input); err != nil {
			report.Errorf(path, "dry-run", "%v", err)
			failed = true
		} else {
			fmt.Printf("OK   %s: %s %s\n", path, capability.Backend.Method, rendered)
		}
	}

	// Dependency edges cross files, so the graph is only checkable once every
	// file in the change set is parsed. A dependency on a capability outside the
	// change set cannot be resolved here, so it degrades to a warning.
	if len(loaded) > 0 {
		if err := capabilities.ValidateDependencies(loaded); err != nil {
			if names := unregisteredDependency(err); names != "" {
				fmt.Printf("WARN dependency %s is not in this change set; skipping graph check\n", names)
			} else {
				report.Errorf(loaded[0].Name, "dry-run", "dependency graph: %v", err)
				failed = true
			}
		}
	}

	if failed {
		return errors.New("dry-run failed")
	}
	return nil
}

// unregisteredDependency reports the dependency name when the error is a
// missing-capability error, which in CI usually means the dependency lives in a
// file the PR did not touch rather than that it does not exist.
func unregisteredDependency(err error) string {
	const marker = "unregistered capability "
	message := err.Error()
	index := strings.Index(message, marker)
	if index < 0 {
		return ""
	}
	return strings.TrimSpace(message[index+len(marker):])
}

// syntheticInput builds a placeholder value for every declared input so path
// rendering can be exercised without real operator input.
func syntheticInput(capability capabilities.Capability) map[string]any {
	input := make(map[string]any, len(capability.InputSchema))
	for name, field := range capability.InputSchema {
		switch field.Type {
		case "integer":
			value := 1
			if field.Min != nil {
				value = int(*field.Min)
			}
			input[name] = value
		case "boolean":
			input[name] = false
		default:
			input[name] = "dry-run-" + name
		}
	}
	if _, ok := input["environment"]; !ok {
		input["environment"] = "staging"
	}
	return input
}

var pathVariable = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)

// renderPath substitutes {name} path variables from input, mirroring what the
// HTTP adapter does at execution time.
func renderPath(path string, input map[string]any) (string, error) {
	var missing []string
	rendered := pathVariable.ReplaceAllStringFunc(path, func(match string) string {
		name := strings.Trim(match, "{}")
		value, ok := input[name]
		if !ok {
			missing = append(missing, name)
			return match
		}
		return fmt.Sprintf("%v", value)
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("backend.path variables %s are not declared in input_schema", strings.Join(missing, ", "))
	}
	return rendered, nil
}

// decodeFile reads and decodes one capability, recording a finding on failure.
func decodeFile(path, check string, report *Report) (capabilities.Capability, bool) {
	body, err := os.ReadFile(path)
	if err != nil {
		report.Errorf(path, check, "read file: %v", err)
		return capabilities.Capability{}, false
	}
	var capability capabilities.Capability
	if err := yaml.Unmarshal(body, &capability); err != nil {
		report.Errorf(path, check, "parse YAML: %v", err)
		return capabilities.Capability{}, false
	}
	return capability, true
}
