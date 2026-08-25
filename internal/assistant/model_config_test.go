package assistant

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func envWith(overrides map[string]string) map[string]string {
	base := EnvMapFromLookup(func(string) string { return "" })
	for k, v := range overrides {
		base[k] = v
	}
	return base
}

func loadRegistry(t *testing.T, env map[string]string) *ModelsConfig {
	t.Helper()
	reg, err := LoadModelRegistry(env)
	if err != nil {
		t.Fatalf("LoadModelRegistry: %v", err)
	}
	return reg
}

// 仅配置主模型：planner 生效，其余可回退角色（intent/tool/formatter/compactor）
// 都回退到 planner，reasoning/fallback 未配置返回不可用。
func TestLoadModelRegistry_PlannerOnly(t *testing.T) {
	env := envWith(map[string]string{
		envOpenAIModel:   "gpt-4o",
		envOpenAIAPIKey:  "k-main",
		envOpenAIBaseURL: "https://api.example.com/v1",
	})
	reg, err := LoadModelRegistry(env)
	if err != nil {
		t.Fatalf("LoadModelRegistry: %v", err)
	}
	planner, ok := reg.Resolve(RolePlanner)
	if !ok || planner.Model != "gpt-4o" {
		t.Fatalf("planner = %+v, ok=%v; want gpt-4o", planner, ok)
	}
	if got, want := reg.MustResolve(RoleIntent).Model, "gpt-4o"; got != want {
		t.Fatalf("intent 应回退 planner，got %q want %q", got, want)
	}
	if got, want := reg.MustResolve(RoleTool).Model, "gpt-4o"; got != want {
		t.Fatalf("tool 应回退 planner，got %q want %q", got, want)
	}
	if _, ok := reg.Resolve(RoleReasoning); ok {
		t.Fatalf("未配置 reasoning 却可用")
	}
}

// 主模型 + 推理模型并存：两个槽位独立生效。
func TestLoadModelRegistry_PlannerAndReasoning(t *testing.T) {
	env := envWith(map[string]string{
		envOpenAIModel:       "gpt-4o",
		envReasoningModel:    "deepseek-reasoner",
		envReasoningAPIKey:   "k-r",
		envReasoningBaseURL:  "https://api.deepseek.com/v1",
	})
	reg, err := LoadModelRegistry(env)
	if err != nil {
		t.Fatalf("LoadModelRegistry: %v", err)
	}
	if got := reg.MustResolve(RolePlanner).Model; got != "gpt-4o" {
		t.Fatalf("planner = %q", got)
	}
	reasoning := reg.MustResolve(RoleReasoning)
	if reasoning.Model != "deepseek-reasoner" || reasoning.BaseURL != "https://api.deepseek.com/v1" {
		t.Fatalf("reasoning = %+v", reasoning)
	}
}

// 意图识别独立槽位：配置 COPILOT_INTENT_MODEL 后不再回退 planner。
func TestLoadModelRegistry_IntentSlot(t *testing.T) {
	env := envWith(map[string]string{
		envOpenAIModel:    "gpt-4o",
		envIntentModel:    "gpt-4o-mini",
		envIntentAPIKey:   "k-int",
		envIntentBaseURL:  "https://api.example.com/int",
	})
	reg, err := LoadModelRegistry(env)
	if err != nil {
		t.Fatalf("LoadModelRegistry: %v", err)
	}
	intent := reg.MustResolve(RoleIntent)
	if intent.Model != "gpt-4o-mini" {
		t.Fatalf("intent 未用独立模型，got %q", intent.Model)
	}
	// 意图模型缺省 key 回退主模型 key。
	if intent.APIKey != "k-int" {
		t.Fatalf("intent api key = %q, want k-int", intent.APIKey)
	}
	if reg.MustResolve(RolePlanner).Model != "gpt-4o" {
		t.Fatalf("planner 应保持 gpt-4o")
	}
}

// 意图独立槽位缺省 key/url 时回退主模型 key/url。
func TestLoadModelRegistry_IntentFallsBackKey(t *testing.T) {
	env := envWith(map[string]string{
		envOpenAIModel:  "gpt-4o",
		envOpenAIAPIKey: "k-main",
		envOpenAIBaseURL: "https://api.example.com/v1",
		envIntentModel:   "gpt-4o-mini",
		// 无 intent key/url，应回退主模型
	})
	reg, err := LoadModelRegistry(env)
	if err != nil {
		t.Fatalf("LoadModelRegistry: %v", err)
	}
	intent := reg.MustResolve(RoleIntent)
	if intent.APIKey != "k-main" {
		t.Fatalf("intent api key 未回退主模型，got %q", intent.APIKey)
	}
	if intent.BaseURL != "https://api.example.com/v1" {
		t.Fatalf("intent base url 未回退主模型，got %q", intent.BaseURL)
	}
}

// 无任何配置：注册表可用但无生效槽位（所有 Resolve 均不可用）。
func TestLoadModelRegistry_Empty(t *testing.T) {
	reg, err := LoadModelRegistry(envWith(nil))
	if err != nil {
		t.Fatalf("LoadModelRegistry: %v", err)
	}
	if _, ok := reg.Resolve(RolePlanner); ok {
		t.Fatalf("空配置下 planner 不应可用")
	}
	if _, ok := reg.Resolve(RoleIntent); ok {
		t.Fatalf("空配置下 intent 不应可用")
	}
}

// models.yaml 优先：yaml 里定义的角色覆盖 env 兜底；未在 yaml 里定义的角色
// 仍走 env 兜底（逐角色合并）。
func TestLoadModelRegistry_YamlOverEnvPerRole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.yaml")
	yaml := `
models:
  planner:
    provider: openai
    model: mimo-v2.5
    base_url: https://token-plan-cn.xiaomimimo.com/v1
    api_key: ${COPILOT_OPENAI_API_KEY}
    timeout: 30s
    max_tokens: 2048
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	env := envWith(map[string]string{
		envModelsConfig:       path,
		envOpenAIModel:        "gpt-4o", // 应被 yaml 的 planner 覆盖
		envOpenAIAPIKey:       "k-main",
		envReasoningModel:     "deepseek-reasoner", // yaml 未定义 reasoning → 走 env
		envReasoningAPIKey:    "k-r",
	})
	reg, err := LoadModelRegistry(env)
	if err != nil {
		t.Fatalf("LoadModelRegistry: %v", err)
	}
	planner := reg.MustResolve(RolePlanner)
	if planner.Model != "mimo-v2.5" {
		t.Fatalf("planner 应取 yaml 值，got %q", planner.Model)
	}
	if d, err := time.ParseDuration(planner.Timeout); err != nil || d != 30*time.Second {
		t.Fatalf("planner timeout = %q, want 30s", planner.Timeout)
	}
	if got := reg.MustResolve(RoleReasoning).Model; got != "deepseek-reasoner" {
		t.Fatalf("reasoning 应取 env 兜底，got %q", got)
	}
}

// models.yaml 定义 intent/formatter 等独立角色时按角色解析。
func TestLoadModelRegistry_YamlIntentRole(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "models.yaml")
	yaml := `
models:
  planner:
    provider: openai
    model: gpt-4o
    api_key: ${COPILOT_OPENAI_API_KEY}
    timeout: 5s
  intent:
    provider: openai
    model: gpt-4o-mini
    api_key: ${COPILOT_INTENT_API_KEY}
    timeout: 10s
    max_tokens: 1024
`
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	reg, err := LoadModelRegistry(envWith(map[string]string{envModelsConfig: path}))
	if err != nil {
		t.Fatalf("LoadModelRegistry: %v", err)
	}
	intent := reg.MustResolve(RoleIntent)
	if intent.Model != "gpt-4o-mini" || intent.MaxTokens != 1024 {
		t.Fatalf("intent = %+v", intent)
	}
}