package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
)

func main() {
	if err := run(os.Args); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 5 || args[1] != "import" || args[2] != "openapi" {
		return fmt.Errorf("usage: %s import openapi <openapi.yaml> <output-dir>", filepath.Base(args[0]))
	}
	body, err := os.ReadFile(args[3])
	if err != nil {
		return err
	}
	drafts, err := capabilities.ImportOpenAPI(body)
	if err != nil {
		return err
	}
	return capabilities.WriteDrafts(args[4], drafts)
}
