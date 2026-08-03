// Command capability-validator gates capability YAML in CI. It runs the same
// capabilities.Validate the runtime loader runs, so a file that passes here
// cannot be rejected at startup, plus CI-only checks (secret scanning,
// cross-file dependency resolution, dry-run) that the loader does not do.
//
// Usage:
//
//	capability-validator <check> --files <newline-separated paths>
//
// Checks: validate-syntax, validate-schema, check-required, scan-secrets,
// dry-run, report, all.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

// check is one named validation stage. Every stage reads the same file list and
// records its findings into the shared report so a single `report` run at the
// end can summarize everything.
type check struct {
	name    string
	summary string
	run     func(files []string, report *Report) error
}

func checks() []check {
	return []check{
		{"validate-syntax", "parse each file as YAML", runValidateSyntax},
		{"validate-schema", "run the runtime capability validator", runValidateSchema},
		{"check-required", "check fields the runtime allows but operators should not omit", runCheckRequired},
		{"scan-secrets", "look for credentials committed into capability YAML", runScanSecrets},
		{"dry-run", "resolve dependencies and render the backend request without calling it", runDryRun},
	}
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	name := os.Args[1]

	flags := flag.NewFlagSet(name, flag.ExitOnError)
	filesFlag := flags.String("files", "", "newline- or comma-separated list of capability files to validate")
	outputFlag := flags.String("output", "validation-report.md", "output path for the markdown report")
	stateFlag := flags.String("state", ".capability-validation.json", "path used to carry findings between checks in one CI job")
	if err := flags.Parse(os.Args[2:]); err != nil {
		os.Exit(2)
	}

	files := parseFilesList(*filesFlag)
	if len(files) == 0 {
		fmt.Println("no capability files to validate")
		return
	}

	report, err := LoadReport(*stateFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load validation state: %v\n", err)
		os.Exit(1)
	}

	var runErr error
	switch name {
	case "report":
		runErr = writeReport(report, *outputFlag)
	case "all":
		for _, c := range checks() {
			if err := c.run(files, report); err != nil {
				runErr = err
			}
		}
		if err := writeReport(report, *outputFlag); err != nil && runErr == nil {
			runErr = err
		}
	default:
		found := false
		for _, c := range checks() {
			if c.name == name {
				found = true
				runErr = c.run(files, report)
				break
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "unknown check %q\n\n", name)
			usage()
			os.Exit(2)
		}
	}

	// Persist findings even on failure: the report step runs with `if: always()`
	// and needs the errors from the step that failed.
	if err := report.Save(*stateFlag); err != nil {
		fmt.Fprintf(os.Stderr, "save validation state: %v\n", err)
		os.Exit(1)
	}
	if runErr != nil {
		fmt.Fprintln(os.Stderr, runErr)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: capability-validator <check> --files <paths>")
	fmt.Fprintln(os.Stderr, "\nchecks:")
	for _, c := range checks() {
		fmt.Fprintf(os.Stderr, "  %-16s %s\n", c.name, c.summary)
	}
	fmt.Fprintf(os.Stderr, "  %-16s %s\n", "report", "write the accumulated findings as markdown")
	fmt.Fprintf(os.Stderr, "  %-16s %s\n", "all", "run every check, then write the report")
}

// parseFilesList accepts newline- or comma-separated paths so the same flag
// works with a `git diff` output block and with a hand-typed local invocation.
func parseFilesList(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == '\n' || r == '\r' || r == ',' || r == ' ' || r == '\t'
	})
	files := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, field := range fields {
		path := strings.TrimSpace(field)
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		files = append(files, path)
	}
	return files
}
