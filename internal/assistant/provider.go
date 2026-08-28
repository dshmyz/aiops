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

	// 意图识别专用模型配置（可选，不设则复用主模型）
	envIntentModel   = "COPILOT_INTENT_MODEL"
	envIntentBaseURL = "COPILOT_INTENT_BASE_URL"
	envIntentAPIKey  = "COPILOT_INTENT_API_KEY"

	// 统一模型配置注册表文件（models.yaml），角色由 yaml 提供、env 兜底
	envModelsConfig = "COPILOT_MODELS_CONFIG"

	// 备用模型配置（可选，主模型限流时自动切换）
	envFallbackModel   = "COPILOT_FALLBACK_MODEL"
	envFallbackBaseURL = "COPILOT_FALLBACK_BASE_URL"
	envFallbackAPIKey  = "COPILOT_FALLBACK_API_KEY"

	// defaultChatTimeout is the per-LLM-call fallback timeout for chat models
	// whose role config doesn't specify one (intent planning / compression can
	// take longer than a fast fatal timeout). Plan and reasoning roles set their
	// own defaults (30s / 60s) in the registry; this only guards the extreme case.
	defaultChatTimeout = 30 * time.Second
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
	reg, err := LoadModelRegistry(env)
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("load model registry: %w", err)
	}
	plannerCfg, ok := reg.Resolve(RolePlanner)
	if !ok {
		return nil, nil, nil, "", errors.New("COPILOT_OPENAI_MODEL (or models.yaml planner) is required for eino-openai")
	}
	if strings.TrimSpace(resolveEnvVar(plannerCfg.APIKey)) == "" || strings.TrimSpace(plannerCfg.Model) == "" {
		return nil, nil, nil, "", errors.New("COPILOT_OPENAI_API_KEY and COPILOT_OPENAI_MODEL are required for eino-openai")
	}
	// chat is typed as the model interface so it can be swapped for the retry
	// wrapper below while still being shared by planner/compactor.
	// 意图识别走独立 intent 槽位（若配置），否则复用 planner 模型。
	intentChat, intentErr := intentChatFromRegistry(ctx, reg, env)
	if intentErr != nil {
		return nil, nil, nil, "", intentErr
	}
	chat, err := chatFromConfig(ctx, plannerCfg, env, true, defaultChatTimeout)
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("create eino openai chat model: %w", err)
	}
	// 部分兼容上游（OpenRouter 上的 GLM/DeepSeek）在 json_object 模式下间歇性
	// 返回空 body——HTTP 成功、withChatRetry 不触发、planner 死于"input json is
	// empty"。用同一配置的无 json 副本兜底重发一次（planning prompt 本就要求
	// 严格 JSON，自由文本模式能稳定产出）。fallbackChat 同时被 planner 与
	// compactor 共享。
	plainChat, perr := chatFromConfig(ctx, plannerCfg, env, false, defaultChatTimeout)
	if perr != nil {
		plainChat = nil // 兜底副本建不出来时退回单模型，不阻断构建
	}
	fallbackChat := withJSONFallback(chat, plainChat, nil)
	// LLMFormatter 用独立的无 json_object chat：其 prompt 是分隔符格式的自由文本
	//（[[SUMMARY_START]]...[[SUMMARY_END]]），流式路径把 SUMMARY 区间逐 token 转发。
	// 若复用 planner 的 json_object chat，模型只回严格 JSON，没有 [[SUMMARY_START]]
	// 标记 → 0 delta，且空 summary 触发 code 兜底（工具调用路径"一波输出"的根因）。
	formatterChat, ferr := chatFromConfig(ctx, reg.MustResolve(RoleFormatter), env, false, defaultChatTimeout)
	if ferr != nil {
		// 极端回退：共享 planner chat（丧失流式，但整形功能不降级）。
		formatterChat = chat
	}
	formatter := NewChainedFormatter(NewLLMFormatter(formatterChat), NewCodeFallbackFormatter())
	planner := NewEinoPlannerWithIntent(fallbackChat, intentChat)
	return planner, NewLLMCompactor(fallbackChat), formatter, "eino-openai", nil
}

// intentChatFromRegistry 解析意图识别专用模型（RoleIntent）。未显式配置或与
// planner 相同则返回 nil（调用方复用 planner 模型）。
func intentChatFromRegistry(ctx context.Context, reg *ModelsConfig, env map[string]string) (model.BaseChatModel, error) {
	plannerCfg, ok := reg.Resolve(RolePlanner)
	if !ok {
		return nil, nil
	}
	intentCfg, hasIntent := reg.Resolve(RoleIntent)
	if !hasIntent || intentCfg.Model == "" || intentCfg.Model == plannerCfg.Model {
		return nil, nil
	}
	return chatFromConfig(ctx, intentCfg, env, true, 15*time.Second)
}

// chatFromConfig 依据统一注册表中的 ModelConfig 创建带重试的 eino-openai chat。
// cfg.Timeout 留空时使用 defaultTimeout；maxTokens 缺省回退 2048。
func chatFromConfig(ctx context.Context, cfg ModelConfig, env map[string]string, jsonMode bool, defaultTimeout time.Duration) (model.BaseChatModel, error) {
	apiKey := resolveEnvVar(cfg.APIKey)
	baseURL := resolveEnvVar(cfg.BaseURL)
	timeout := defaultTimeout
	if cfg.Timeout != "" {
		if d, err := time.ParseDuration(cfg.Timeout); err == nil && d > 0 {
			timeout = d
		}
	}
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
	}
	chat, err := newOpenAIChat(ctx, apiKey, baseURL, cfg.Model, 0, maxTokens, timeout, jsonMode)
	if err != nil {
		return nil, err
	}
	return withChatRetry(chat, retryAttempts(env), retryBackoff(env)), nil
}

// newOpenAIChat 创建 eino-openai 聊天模型。jsonMode 为 true 时强制
// response_format=json_object（planner/compactor 的 prompt 输出严格 JSON）；为
// false 时模型可输出自由文本（formatter 的分隔符格式、agent 的 tool call 都用它）。
func newOpenAIChat(ctx context.Context, apiKey, baseURL, model string, temperature float32, maxTokens int, timeout time.Duration, jsonMode bool) (model.BaseChatModel, error) {
	cfg := &einoopenai.ChatModelConfig{
		APIKey:              apiKey,
		BaseURL:             baseURL,
		Model:               model,
		Timeout:             timeout,
		Temperature:         &temperature,
		MaxCompletionTokens: &maxTokens,
	}
	if jsonMode {
		cfg.ResponseFormat = &einoopenai.ChatCompletionResponseFormat{
			Type: einoopenai.ChatCompletionResponseFormatTypeJSONObject,
		}
	}
	return einoopenai.NewChatModel(ctx, cfg)
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
		envIntentModel:        lookup(envIntentModel),
		envIntentBaseURL:      lookup(envIntentBaseURL),
		envIntentAPIKey:       lookup(envIntentAPIKey),
		envModelsConfig:       lookup(envModelsConfig),
		envFallbackModel:      lookup(envFallbackModel),
		envFallbackBaseURL:    lookup(envFallbackBaseURL),
		envFallbackAPIKey:     lookup(envFallbackAPIKey),
	}
}

// NewFallbackChatModel 创建带降级的 chat model。
// 如果 COPILOT_FALLBACK_MODEL 未设置，返回 nil（不降级）。
func NewFallbackChatModel(ctx context.Context, primary model.BaseChatModel, env map[string]string) model.BaseChatModel {
	reg, err := LoadModelRegistry(env)
	if err != nil {
		return nil
	}
	cfg, ok := reg.Resolve(RoleFallback)
	if !ok || cfg.Model == "" {
		return nil // 未配置备用模型，不降级
	}
	fallbackWrapped, err := chatFromConfig(ctx, cfg, env, false, 30*time.Second)
	if err != nil {
		return nil
	}
	// 3 次连续 429 后切换到备用模型
	return newFallbackChat(primary, fallbackWrapped, 3)
}

// NewReasoningModelFromEnv 创建推理模型。如果未配置 RoleReasoning（models.yaml
// 或 COPILOT_REASONING_MODEL），返回 nil（表示复用主模型）。
func NewReasoningModelFromEnv(ctx context.Context, env map[string]string) model.BaseChatModel {
	reg, err := LoadModelRegistry(env)
	if err != nil {
		return nil
	}
	cfg, ok := reg.Resolve(RoleReasoning)
	if !ok || cfg.Model == "" {
		return nil // 未配置推理模型，复用主模型
	}
	chat, err := chatFromConfig(ctx, cfg, env, false, 60*time.Second)
	if err != nil {
		return nil
	}
	return chat
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
	reg, err := LoadModelRegistry(env)
	if err != nil {
		return nil
	}
	cfg, ok := reg.Resolve(RoleTool)
	if !ok || cfg.Model == "" {
		return nil
	}
	chat, err := chatFromConfig(ctx, cfg, env, false, 60*time.Second)
	if err != nil {
		return nil
	}
	return chat
}
