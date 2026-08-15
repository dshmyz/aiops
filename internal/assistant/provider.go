package assistant

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"

	"github.com/gracegaoya/ai-operations-copilot/internal/prompt"
)

const (
	envAssistantProvider  = "COPILOT_ASSISTANT_PROVIDER"
	envOpenAIAPIKey       = "COPILOT_OPENAI_API_KEY"
	envOpenAIModel        = "COPILOT_OPENAI_MODEL"
	envOpenAIBaseURL      = "COPILOT_OPENAI_BASE_URL"
	envOpenAITimeout      = "COPILOT_OPENAI_TIMEOUT"
	envOpenAIRetry        = "COPILOT_OPENAI_RETRY"
	envOpenAIRetryBackoff = "COPILOT_OPENAI_RETRY_BACKOFF"
	envPromptsDir         = "COPILOT_PROMPTS_DIR"
	envOpenAIMaxTokens    = "COPILOT_OPENAI_MAX_TOKENS"

	// 推理模型配置（可选，不设则复用主模型）
	envReasoningModel   = "COPILOT_REASONING_MODEL"
	envReasoningBaseURL = "COPILOT_REASONING_BASE_URL"
	envReasoningAPIKey  = "COPILOT_REASONING_API_KEY"

	// 备用模型配置（可选，主模型限流时自动切换）
	envFallbackModel   = "COPILOT_FALLBACK_MODEL"
	envFallbackBaseURL = "COPILOT_FALLBACK_BASE_URL"
	envFallbackAPIKey  = "COPILOT_FALLBACK_API_KEY"

	// defaultChatTimeout is the per-LLM-call timeout used for the eino-openai
	// chat model unless overridden by COPILOT_OPENAI_TIMEOUT. Reasoning models
	// (e.g. mimo) can need longer than the historical 5s to formulate a plan, so
	// the value is configurable in the environment.
	defaultChatTimeout = 5 * time.Second
	// defaultChatRetry is the default number of one-shot completion attempts
	// (1 = no retry). COPILOT_OPENAI_RETRY overrides it.
	defaultChatRetry = 2
	// defaultChatRetryBackoff is the base backoff between retry attempts;
	// COPILOT_OPENAI_RETRY_BACKOFF overrides it (Go duration).
	defaultChatRetryBackoff = 500 * time.Millisecond
)

// retryAttempts reads COPILOT_OPENAI_RETRY, defaulting to defaultChatRetry.
func retryAttempts(env map[string]string) int {
	v := strings.TrimSpace(env[envOpenAIRetry])
	if v == "" {
		return defaultChatRetry
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return defaultChatRetry
	}
	return n
}

// retryBackoff reads COPILOT_OPENAI_RETRY_BACKOFF, defaulting to
// defaultChatRetryBackoff.
func retryBackoff(env map[string]string) time.Duration {
	v := strings.TrimSpace(env[envOpenAIRetryBackoff])
	if v == "" {
		return defaultChatRetryBackoff
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return defaultChatRetryBackoff
	}
	return d
}

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
	if v := strings.TrimSpace(env[envOpenAIMaxTokens]); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxCompletionTokens = n
		}
	}
	timeout := defaultChatTimeout
	if v := strings.TrimSpace(env[envOpenAITimeout]); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			timeout = d
		}
	}
	// chat is typed as the model interface so it can be swapped for the retry
	// wrapper below while still being shared by planner/compactor/formatter.
	var chat model.BaseChatModel
	var err error
	chat, err = einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		APIKey:              apiKey,
		BaseURL:             strings.TrimSpace(env[envOpenAIBaseURL]),
		Model:               modelName,
		Timeout:             timeout,
		Temperature:         &temperature,
		MaxCompletionTokens: &maxCompletionTokens,
		ResponseFormat: &einoopenai.ChatCompletionResponseFormat{
			Type: einoopenai.ChatCompletionResponseFormatTypeJSONObject,
		},
	})
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("create eino openai chat model: %w", err)
	}
	// Retry transient one-shot completions (provider drops the connection or
	// stalls awaiting headers) so momentary backends don't surface as hard
	// failures. Attempts defaults to 2; both knobs are configurable via env.
	chat = withChatRetry(chat, retryAttempts(env), retryBackoff(env))
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
		envAssistantProvider:  lookup(envAssistantProvider),
		envOpenAIAPIKey:       lookup(envOpenAIAPIKey),
		envOpenAIModel:        lookup(envOpenAIModel),
		envOpenAIBaseURL:      lookup(envOpenAIBaseURL),
		envOpenAIRetry:        lookup(envOpenAIRetry),
		envOpenAIRetryBackoff: lookup(envOpenAIRetryBackoff),
		envOpenAITimeout:      lookup(envOpenAITimeout),
		envOpenAIMaxTokens:    lookup(envOpenAIMaxTokens),
		envPromptsDir:         lookup(envPromptsDir),
		envReasoningModel:     lookup(envReasoningModel),
		envReasoningBaseURL:   lookup(envReasoningBaseURL),
		envReasoningAPIKey:    lookup(envReasoningAPIKey),
		envFallbackModel:      lookup(envFallbackModel),
		envFallbackBaseURL:    lookup(envFallbackBaseURL),
		envFallbackAPIKey:     lookup(envFallbackAPIKey),
	}
}

// NewFallbackChatModel 创建带降级的 chat model。
// 如果 COPILOT_FALLBACK_MODEL 未设置，返回 nil（不降级）。
func NewFallbackChatModel(ctx context.Context, primary model.BaseChatModel, env map[string]string) model.BaseChatModel {
	modelName := strings.TrimSpace(env[envFallbackModel])
	if modelName == "" {
		return nil
	}
	apiKey := strings.TrimSpace(env[envFallbackAPIKey])
	if apiKey == "" {
		apiKey = strings.TrimSpace(env[envOpenAIAPIKey])
	}
	baseURL := strings.TrimSpace(env[envFallbackBaseURL])
	if baseURL == "" {
		baseURL = strings.TrimSpace(env[envOpenAIBaseURL])
	}
	temperature := float32(0)
	maxTokens := 2048
	timeout := 30 * time.Second

	chat, err := einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		APIKey:              apiKey,
		BaseURL:             baseURL,
		Model:               modelName,
		Timeout:             timeout,
		Temperature:         &temperature,
		MaxCompletionTokens: &maxTokens,
	})
	if err != nil {
		return nil
	}
	var fallbackWrapped model.BaseChatModel = chat
	fallbackWrapped = withChatRetry(fallbackWrapped, retryAttempts(env), retryBackoff(env))
	// 3 次连续 429 后切换到备用模型
	return newFallbackChat(primary, fallbackWrapped, 3)
}

// NewReasoningModelFromEnv 创建推理模型。如果 COPILOT_REASONING_MODEL 未设置，
// 返回 nil（表示复用主模型）。
func NewReasoningModelFromEnv(ctx context.Context, env map[string]string) model.BaseChatModel {
	modelName := strings.TrimSpace(env[envReasoningModel])
	if modelName == "" {
		return nil // 未配置推理模型，复用主模型
	}
	apiKey := strings.TrimSpace(env[envReasoningAPIKey])
	if apiKey == "" {
		apiKey = strings.TrimSpace(env[envOpenAIAPIKey]) // fallback 到主模型 key
	}
	baseURL := strings.TrimSpace(env[envReasoningBaseURL])
	if baseURL == "" {
		baseURL = strings.TrimSpace(env[envOpenAIBaseURL]) // fallback 到主模型 URL
	}
	temperature := float32(0)
	maxTokens := 4096           // 推理模型给更多 token
	timeout := 60 * time.Second // 推理模型需要更长时间

	chat, err := einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		APIKey:              apiKey,
		BaseURL:             baseURL,
		Model:               modelName,
		Timeout:             timeout,
		Temperature:         &temperature,
		MaxCompletionTokens: &maxTokens,
	})
	if err != nil {
		return nil
	}
	return withChatRetry(chat, retryAttempts(env), retryBackoff(env))
}

// NewToolChatModelFromEnv 创建用于 LLM function calling 的 chat model（agent
// executor 选工具执行用）。
//
// 关键区别：planner 用的 chat 带 response_format=json_object（planner 输出严格
// JSON 意图）；agent executor 不能共用它——JSON 格式下模型即使拿到 tools 也倾向
// 返回 JSON 文本而非发起 tool call，导致"说要用工具却不真调"。agent 的 chat
// 不带 response_format，让模型正常发 tool call，只在收尾时输出自然语言回答。
//
// 未配置 LLM 或非 eino-openai provider 时返回 nil（调用方回退主模型）。
func NewToolChatModelFromEnv(ctx context.Context, env map[string]string) model.BaseChatModel {
	provider := strings.TrimSpace(env[envAssistantProvider])
	if provider != "eino-openai" {
		return nil
	}
	apiKey := strings.TrimSpace(env[envOpenAIAPIKey])
	modelName := strings.TrimSpace(env[envOpenAIModel])
	if apiKey == "" || modelName == "" {
		return nil
	}
	temperature := float32(0)
	maxTokens := 1024 // agent 需要给模型输出工具调用参数/回答留余量
	if v := strings.TrimSpace(env[envOpenAIMaxTokens]); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxTokens = n
		}
	}
	timeout := 60 * time.Second // agent 循环单轮可较慢
	if v := strings.TrimSpace(env[envOpenAITimeout]); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			timeout = d
		}
	}

	chat, err := einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		APIKey:              apiKey,
		BaseURL:             strings.TrimSpace(env[envOpenAIBaseURL]),
		Model:               modelName,
		Timeout:             timeout,
		Temperature:         &temperature,
		MaxCompletionTokens: &maxTokens,
		// 无 ResponseFormat：让模型正常发起 tool call。
	})
	if err != nil {
		return nil
	}
	return withChatRetry(chat, retryAttempts(env), retryBackoff(env))
}
