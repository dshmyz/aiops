package assistant

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"

	"github.com/gracegaoya/ai-operations-copilot/internal/prompt"
)

const (
	envAssistantProvider = "COPILOT_ASSISTANT_PROVIDER"
	envOpenAIAPIKey      = "COPILOT_OPENAI_API_KEY"
	envOpenAIModel       = "COPILOT_OPENAI_MODEL"
	envOpenAIBaseURL     = "COPILOT_OPENAI_BASE_URL"
	envPromptsDir        = "COPILOT_PROMPTS_DIR"
)

// NewPlannerFromEnv builds the assistant planner from environment configuration.
// When the provider is eino-openai, it also returns an LLMCompactor that shares
// the same chat model so rolling summarization reuses the planner's LLM, and a
// ChainedFormatter[LLM, Code] that reuses the same chat model for the
// second-stage response formatting. For the deterministic provider the
// compactor and formatter are nil (no LLM available); the caller falls back to
// CodeFallbackFormatter for deterministic mode.
func NewPlannerFromEnv(ctx context.Context, env map[string]string) (Planner, Compactor, ResponseFormatter, string, error) {
	provider := strings.TrimSpace(env[envAssistantProvider])
	if provider == "" || provider == "deterministic" {
		return DeterministicPlanner{}, nil, nil, "deterministic", nil
	}
	if provider != "eino-openai" {
		return nil, nil, nil, "", fmt.Errorf("unsupported assistant provider %q", provider)
	}
	apiKey := strings.TrimSpace(env[envOpenAIAPIKey])
	modelName := strings.TrimSpace(env[envOpenAIModel])
	if apiKey == "" || modelName == "" {
		return nil, nil, nil, "", errors.New("COPILOT_OPENAI_API_KEY and COPILOT_OPENAI_MODEL are required for eino-openai")
	}
	temperature := float32(0)
	maxCompletionTokens := 256
	chat, err := einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		APIKey:              apiKey,
		BaseURL:             strings.TrimSpace(env[envOpenAIBaseURL]),
		Model:               modelName,
		Timeout:             5 * time.Second,
		Temperature:         &temperature,
		MaxCompletionTokens: &maxCompletionTokens,
		ResponseFormat: &einoopenai.ChatCompletionResponseFormat{
			Type: einoopenai.ChatCompletionResponseFormatTypeJSONObject,
		},
	})
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("create eino openai chat model: %w", err)
	}
	// LLMFormatter 复用同一个 chat model，失败时由 CodeFallbackFormatter 兜底。
	formatter := NewChainedFormatter(NewLLMFormatter(chat), NewCodeFallbackFormatter())
	return NewEinoPlanner(chat), NewLLMCompactor(chat), formatter, "eino-openai", nil
}

// NewPlannerFromEnvWithPrompts extends NewPlannerFromEnv with a prompt
// registry. When the registry contains a "planning" prompt, the EinoPlanner
// uses it (with hot-reload) instead of the hardcoded constant. The registry
// is also returned so the caller can wire admin API endpoints.
func NewPlannerFromEnvWithPrompts(ctx context.Context, env map[string]string) (Planner, Compactor, ResponseFormatter, *prompt.Registry, string, error) {
	planner, compactor, formatter, mode, err := NewPlannerFromEnv(ctx, env)
	if err != nil {
		return nil, nil, nil, nil, "", err
	}
	promptsDir := strings.TrimSpace(env[envPromptsDir])
	if promptsDir == "" {
		return planner, compactor, formatter, nil, mode, nil
	}
	registry := prompt.NewRegistry(promptsDir)
	if _, loadErr := registry.LoadAll(); loadErr != nil {
		// Non-fatal: fall back to hardcoded prompts.
		return planner, compactor, formatter, registry, mode, nil
	}
	// If the planner is an EinoPlanner, wire the prompt source.
	if ep, ok := planner.(*EinoPlanner); ok {
		ep.systemPrompt = func() string {
			if p, found := registry.Get("planning"); found {
				return p.Content
			}
			return ""
		}
	}
	return planner, compactor, formatter, registry, mode, nil
}

func EnvMapFromLookup(lookup func(string) string) map[string]string {
	return map[string]string{
		envAssistantProvider: lookup(envAssistantProvider),
		envOpenAIAPIKey:      lookup(envOpenAIAPIKey),
		envOpenAIModel:       lookup(envOpenAIModel),
		envOpenAIBaseURL:     lookup(envOpenAIBaseURL),
		envPromptsDir:        lookup(envPromptsDir),
	}
}
