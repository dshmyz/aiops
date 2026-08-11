package capabilities

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ChatCompleter 是 llm_enricher 依赖的最小聊天接口。main 侧用 assistant 的
// eino chat model 适配，使 capabilities 包不依赖具体 LLM provider。
type ChatCompleter interface {
	// Complete 发送 system+user 消息，返回模型文本响应。
	Complete(ctx context.Context, system, user string) (string, error)
}

// LLMImportEnricher 用 LLM 对导入的能力草稿做富化：为 input_schema 字段补中文
// description/examples/enum、优化 ai.description 与领域/资源类型。全程容错：
// LLM 失败或返回非法 JSON 时回退为原始草稿，绝不让导入因富化失败而中断。
type LLMImportEnricher struct {
	chat ChatCompleter
}

// NewLLMImportEnricher 创建基于 LLM 的导入富化器。
func NewLLMImportEnricher(chat ChatCompleter) *LLMImportEnricher {
	return &LLMImportEnricher{chat: chat}
}

// enrichedDraftShape 描述 LLM 需要填写的字段。字段尽量精简，减小 token 与出错率。
type enrichedDraftShape struct {
	Description    string                      `json:"description"`
	InputSchema    map[string]enrichedField    `json:"input_schema"`
}

type enrichedField struct {
	Description string   `json:"description,omitempty"`
	Examples    []string `json:"examples,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

func (e *LLMImportEnricher) Enrich(ctx context.Context, drafts []Capability) ([]Capability, error) {
	out := make([]Capability, len(drafts))
	copy(out, drafts)
	if e == nil || e.chat == nil {
		return out, nil
	}
	// 逐草稿调用 LLM（并发由上层控制），每个失败独立回退，互不影响。
	for i := range out {
		enriched, err := e.enrichOne(ctx, out[i])
		if err != nil {
			continue // 保留原始草稿
		}
		out[i] = enriched
	}
	return out, nil
}

func (e *LLMImportEnricher) enrichOne(ctx context.Context, draft Capability) (Capability, error) {
	user := buildEnrichPrompt(draft)
	response, err := e.chat.Complete(ctx, enrichmentSystemPrompt, user)
	if err != nil {
		return draft, err
	}
	jsonStr := extractEnrichJSON(response)
	var shape enrichedDraftShape
	if err := json.Unmarshal([]byte(jsonStr), &shape); err != nil {
		return draft, err
	}
	// 有选择地回填：只在 LLM 给出有价值内容时覆盖，避免劣化已存在的字段。
	if desc := strings.TrimSpace(shape.Description); desc != "" {
		draft.AI.Description = desc
	}
	for name, field := range shape.InputSchema {
		existing, ok := draft.InputSchema[name]
		if !ok {
			continue
		}
		if f := strings.TrimSpace(field.Description); f != "" {
			existing.Description = f
		}
		if len(field.Examples) > 0 {
			existing.Examples = field.Examples
		}
		if len(field.Enum) > 0 {
			existing.Enum = field.Enum
		}
		draft.InputSchema[name] = existing
	}
	return draft, nil
}

const enrichmentSystemPrompt = "你是能力接口文档的助手。根据给定能力的信息，用简洁中文补全参数说明，只返回合法 JSON。"

// buildEnrichPrompt 把草稿的能力信息压缩进 prompt，让 LLM 补全参数元数据。
func buildEnrichPrompt(draft Capability) string {
	var fields []string
	for name, f := range draft.InputSchema {
		fields = append(fields, fmt.Sprintf("%s(type=%s, required=%v)", name, f.Type, f.Required))
	}
	return fmt.Sprintf(
		"能力: name=%s domain=%s resource_type=%s operation=%s\n摘要: %s\n参数: %s\n\n"+
			"请返回 JSON: {\"description\":\"能力的中文一句描述\",\"input_schema\":{"+
			"\"参数名\":{\"description\":\"中文说明\",\"examples\":[\"示例值\"],\"enum\":[\"合法取值\"]}}}",
		draft.Name, draft.Domain, draft.ResourceType, draft.Operation,
		strings.TrimSpace(draft.AI.Description),
		strings.Join(fields, "; "),
	)
}

func extractEnrichJSON(s string) string {
	// 兼容 ```json ... ``` 包裹
	if i := strings.Index(s, "```"); i >= 0 {
		rest := s[i+3:]
		if strings.HasPrefix(rest, "json\n") {
			rest = rest[5:]
		} else if strings.HasPrefix(rest, "json") {
			rest = rest[4:]
		}
		if j := strings.Index(rest, "```"); j >= 0 {
			return strings.TrimSpace(rest[:j])
		}
	}
	// 取首个 { 到末尾（宽松）
	if i := strings.Index(s, "{"); i >= 0 {
		return s[i:]
	}
	return strings.TrimSpace(s)
}
