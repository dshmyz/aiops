package assistant_test

import (
	"context"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
)

func TestNewPlannerFromEnvDefaultsToDeterministic(t *testing.T) {
	t.Parallel()

	planner, compactor, formatter, mode, err := assistant.NewPlannerFromEnv(context.Background(), map[string]string{})
	if err != nil {
		t.Fatalf("new planner: %v", err)
	}
	if mode != "deterministic" {
		t.Fatalf("mode = %q, want deterministic", mode)
	}
	if _, ok := planner.(assistant.DeterministicPlanner); !ok {
		t.Fatalf("planner = %T, want DeterministicPlanner", planner)
	}
	if compactor != nil {
		t.Fatalf("compactor = %T, want nil for deterministic mode", compactor)
	}
	if formatter != nil {
		t.Fatalf("formatter = %T, want nil for deterministic mode", formatter)
	}
}

func TestNewPlannerFromEnvBuildsEinoOpenAIPlannerWhenConfigured(t *testing.T) {
	t.Parallel()

	planner, compactor, formatter, mode, err := assistant.NewPlannerFromEnv(context.Background(), map[string]string{
		"COPILOT_ASSISTANT_PROVIDER": "eino-openai",
		"COPILOT_OPENAI_API_KEY":     "test-key",
		"COPILOT_OPENAI_MODEL":       "gpt-test",
		"COPILOT_OPENAI_BASE_URL":    "https://example.invalid/v1",
	})
	if err != nil {
		t.Fatalf("new planner: %v", err)
	}
	if mode != "eino-openai" {
		t.Fatalf("mode = %q, want eino-openai", mode)
	}
	if _, ok := planner.(*assistant.EinoPlanner); !ok {
		t.Fatalf("planner = %T, want *EinoPlanner", planner)
	}
	if compactor == nil {
		t.Fatal("compactor = nil, want LLMCompactor for eino-openai mode")
	}
	if formatter == nil {
		t.Fatal("formatter = nil, want ChainedFormatter for eino-openai mode")
	}
}

func TestNewPlannerFromEnvRejectsIncompleteEinoConfig(t *testing.T) {
	t.Parallel()

	_, _, _, _, err := assistant.NewPlannerFromEnv(context.Background(), map[string]string{
		"COPILOT_ASSISTANT_PROVIDER": "eino-openai",
		"COPILOT_OPENAI_API_KEY":     "test-key",
	})
	if err == nil {
		t.Fatal("incomplete eino-openai config succeeded")
	}
}
