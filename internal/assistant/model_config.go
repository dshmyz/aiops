package assistant

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ModelRole 表示一个可独立配置的模型角色槽位。
type ModelRole string

const (
	// RolePlanner 主模型：意图规划 + 历史压缩 + 回复整形兜底。
	RolePlanner ModelRole = "planner"
	// RoleIntent 意图识别专用模型（可选，缺省回退 planner）。
	RoleIntent ModelRole = "intent"
	// RoleTool agent function calling 用的模型（缺省回退 planner）。
	RoleTool ModelRole = "tool"
	// RoleReasoning 深度分析/报告模型（可选，缺省回退 planner）。
	RoleReasoning ModelRole = "reasoning"
	// RoleFormatter 回复整形模型（可选，缺省回退 planner）。
	RoleFormatter ModelRole = "formatter"
	// RoleCompactor 对话历史压缩模型（可选，缺省回退 planner）。
	RoleCompactor ModelRole = "compactor"
	// RoleFallback 主模型限流时降级的备用模型（可选）。
	RoleFallback ModelRole = "fallback"
)

// plannerFallbackRoles 列出在未显式配置时可透明回退到 planner 的角色。
// reasoning/fallback 不在此列：它们"未配置=复用主模型"由调用方决定
// （检查 Resolve 的 ok 后再决定是否复用主模型 / 关闭降级）。
var plannerFallbackRoles = map[ModelRole]bool{
	RoleIntent:    true,
	RoleTool:      true,
	RoleFormatter: true,
	RoleCompactor: true,
}

// ModelConfig 模型配置
type ModelConfig struct {
	Provider  string `yaml:"provider"`
	Model     string `yaml:"model"`
	BaseURL   string `yaml:"base_url"`
	APIKey    string `yaml:"api_key"`
	Timeout   string `yaml:"timeout"`
	MaxTokens int    `yaml:"max_tokens"`
}

// ModelsConfig models.yaml 的顶层结构
type ModelsConfig struct {
	Models map[string]ModelConfig `yaml:"models"`
}

// LoadModelsConfig 从文件加载模型配置
func LoadModelsConfig(path string) (*ModelsConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read models config: %w", err)
	}
	var cfg ModelsConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse models config: %w", err)
	}
	return &cfg, nil
}

// LoadModelRegistry 构建统一的模型配置注册表：models.yaml（COPILOT_MODELS_CONFIG）
// 为唯一事实来源，逐角色用对应环境变量兜底合并——yaml 里已定义的角色不被 env 覆盖，
// yaml 未定义的角色从 env 补齐。
//
// 显式指定了 COPILOT_MODELS_CONFIG 但读取失败时返回错误（而不是静默回退到 env），
// 避免操作者以为配置已生效而实际没有。
func LoadModelRegistry(env map[string]string) (*ModelsConfig, error) {
	cfg := &ModelsConfig{Models: map[string]ModelConfig{}}
	if configPath := strings.TrimSpace(env[envModelsConfig]); configPath != "" {
		fileCfg, err := LoadModelsConfig(configPath)
		if err != nil {
			return nil, err
		}
		cfg.Models = fileCfg.Models
	}
	mergePlannerFromEnv(cfg, env)
	mergeReasoningFromEnv(cfg, env)
	mergeIntentFromEnv(cfg, env)
	mergeFallbackFromEnv(cfg, env)
	return cfg, nil
}

func mergePlannerFromEnv(cfg *ModelsConfig, env map[string]string) {
	if _, ok := cfg.Models[string(RolePlanner)]; ok {
		return
	}
	if model := strings.TrimSpace(env[envOpenAIModel]); model != "" {
		cfg.Models[string(RolePlanner)] = ModelConfig{
			Provider:  "openai",
			Model:     model,
			BaseURL:   strings.TrimSpace(env[envOpenAIBaseURL]),
			APIKey:    strings.TrimSpace(env[envOpenAIAPIKey]),
			Timeout:   envOrDefault(env[envOpenAITimeout], "30s"),
			MaxTokens: intEnvOrDefault(env[envOpenAIMaxTokens], 2048),
		}
	}
}

func mergeReasoningFromEnv(cfg *ModelsConfig, env map[string]string) {
	if _, ok := cfg.Models[string(RoleReasoning)]; ok {
		return
	}
	model := strings.TrimSpace(env[envReasoningModel])
	if model == "" {
		return
	}
	cfg.Models[string(RoleReasoning)] = ModelConfig{
		Provider:  "openai",
		Model:     model,
		BaseURL:   strings.TrimSpace(envOrDefault(env[envReasoningBaseURL], env[envOpenAIBaseURL])),
		APIKey:    strings.TrimSpace(envOrDefault(env[envReasoningAPIKey], env[envOpenAIAPIKey])),
		Timeout:   "60s",
		MaxTokens: 4096,
	}
}

func mergeIntentFromEnv(cfg *ModelsConfig, env map[string]string) {
	if _, ok := cfg.Models[string(RoleIntent)]; ok {
		return
	}
	model := strings.TrimSpace(env[envIntentModel])
	if model == "" {
		return
	}
	cfg.Models[string(RoleIntent)] = ModelConfig{
		Provider:  "openai",
		Model:     model,
		BaseURL:   strings.TrimSpace(envOrDefault(env[envIntentBaseURL], env[envOpenAIBaseURL])),
		APIKey:    strings.TrimSpace(envOrDefault(env[envIntentAPIKey], env[envOpenAIAPIKey])),
		Timeout:   "15s",
		MaxTokens: 512,
	}
}

func mergeFallbackFromEnv(cfg *ModelsConfig, env map[string]string) {
	if _, ok := cfg.Models[string(RoleFallback)]; ok {
		return
	}
	model := strings.TrimSpace(env[envFallbackModel])
	if model == "" {
		return
	}
	cfg.Models[string(RoleFallback)] = ModelConfig{
		Provider:  "openai",
		Model:     model,
		BaseURL:   strings.TrimSpace(envOrDefault(env[envFallbackBaseURL], env[envOpenAIBaseURL])),
		APIKey:    strings.TrimSpace(envOrDefault(env[envFallbackAPIKey], env[envOpenAIAPIKey])),
		Timeout:   "30s",
		MaxTokens: 2048,
	}
}

// Resolve 返回指定角色的最终配置。未显式配置且属于可回退角色时回退到 planner；
// 完全不存在时第二个返回值 false。reasoning/fallback 在没有显式配置时返回不可用
// （调用方据此复用主模型或不启用降级）。
func (c *ModelsConfig) Resolve(role ModelRole) (ModelConfig, bool) {
	if cfg, ok := c.Models[string(role)]; ok {
		return cfg, true
	}
	if plannerFallbackRoles[role] {
		if p, ok := c.Models[string(RolePlanner)]; ok {
			return p, true
		}
	}
	return ModelConfig{}, false
}

// MustResolve 返回指定角色配置；不存在时返回零值（调用方应按 Resolve 语义处理）。
func (c *ModelsConfig) MustResolve(role ModelRole) ModelConfig {
	cfg, _ := c.Resolve(role)
	return cfg
}

func envOrDefault(value, def string) string {
	if v := strings.TrimSpace(value); v != "" {
		return v
	}
	return def
}

func intEnvOrDefault(value string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && n > 0 {
		return n
	}
	return def
}

// resolveEnvVar 解析 ${ENV_VAR} 语法
func resolveEnvVar(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "${") && strings.HasSuffix(value, "}") && len(value) > 4 {
		envName := value[2 : len(value)-1]
		return os.Getenv(envName)
	}
	return value
}
