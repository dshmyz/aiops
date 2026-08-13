package assistant

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"gopkg.in/yaml.v3"
)

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

// LoadModelsConfigFromEnv 尝试从 COPILOT_MODELS_CONFIG 加载，未设置则构建默认配置
func LoadModelsConfigFromEnv(env map[string]string) *ModelsConfig {
	configPath := strings.TrimSpace(env["COPILOT_MODELS_CONFIG"])
	if configPath != "" {
		cfg, err := LoadModelsConfig(configPath)
		if err == nil {
			return cfg
		}
	}
	// fallback：从 env vars 构建默认配置
	cfg := &ModelsConfig{Models: map[string]ModelConfig{}}
	if model := strings.TrimSpace(env[envOpenAIModel]); model != "" {
		cfg.Models["planner"] = ModelConfig{
			Provider:  "openai",
			Model:     model,
			BaseURL:   strings.TrimSpace(env[envOpenAIBaseURL]),
			APIKey:    strings.TrimSpace(env[envOpenAIAPIKey]),
			Timeout:   "30s",
			MaxTokens: 2048,
		}
	}
	if model := strings.TrimSpace(env[envReasoningModel]); model != "" {
		cfg.Models["reasoning"] = ModelConfig{
			Provider:  "openai",
			Model:     model,
			BaseURL:   strings.TrimSpace(env[envReasoningBaseURL]),
			APIKey:    strings.TrimSpace(env[envReasoningAPIKey]),
			Timeout:   "60s",
			MaxTokens: 4096,
		}
	}
	return cfg
}

// CreateChatModel 根据配置创建 chat model
func CreateChatModel(ctx context.Context, cfg ModelConfig) (model.BaseChatModel, error) {
	if cfg.Provider != "openai" {
		return nil, fmt.Errorf("unsupported provider %q", cfg.Provider)
	}
	timeout := 30 * time.Second
	if cfg.Timeout != "" {
		if d, err := time.ParseDuration(cfg.Timeout); err == nil {
			timeout = d
		}
	}
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 2048
	}
	// 解析 ${ENV_VAR} 语法
	apiKey := resolveEnvVar(cfg.APIKey)
	baseURL := resolveEnvVar(cfg.BaseURL)
	temperature := float32(0)

	chat, err := einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		APIKey:              apiKey,
		BaseURL:             baseURL,
		Model:               cfg.Model,
		Timeout:             timeout,
		Temperature:         &temperature,
		MaxCompletionTokens: &maxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("create chat model %q: %w", cfg.Model, err)
	}
	return chat, nil
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
