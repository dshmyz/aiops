package capabilities

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// ChatCompleter 是 llm_enricher 依赖的最小聊天接口。main 侧用 assistant 的
// eino chat model 适配，使 capabilities 包不依赖具体 LLM provider。
type ChatCompleter interface {
	// Complete 发送 system+user 消息，返回模型文本响应。
	Complete(ctx context.Context, system, user string) (string, error)
}

// LLMImportEnricher 用 LLM 对导入的能力草稿做富化：
// - 为 input_schema 字段补中文 description/examples/enum
// - 优化 ai.description
// - 优化 output.fields（在自动推断基础上微调）
// - 优化 summary_template
// - 推断 risk 等级
// 全程容错：LLM 失败或返回非法 JSON 时回退为原始草稿，绝不让导入因富化失败而中断。
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
	Summary        string                      `json:"summary,omitempty"`
	Risk           string                      `json:"risk,omitempty"`
	InputSchema    map[string]enrichedField    `json:"input_schema"`
	OutputFields   map[string]string           `json:"output_fields,omitempty"`
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
	if summary := strings.TrimSpace(shape.Summary); summary != "" {
		draft.Output.SummaryTemplate = summary
	}
	if risk := strings.TrimSpace(shape.Risk); risk != "" {
		if isValidRiskLevel(risk) {
			draft.Risk = tools.RiskLevel(risk)
		}
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
	// 输出字段优化：只在已有自动推断字段的基础上优化，不凭空生成
	if len(shape.OutputFields) > 0 && draft.Output.Fields != nil {
		for name, path := range shape.OutputFields {
			if strings.TrimSpace(name) == "" || strings.TrimSpace(path) == "" {
				continue
			}
			// 只添加新字段或修正已有字段的路径，不删除自动推断的字段
			if _, exists := draft.Output.Fields[name]; !exists {
				if len(draft.Output.Fields) < 15 { // 上限保护
					draft.Output.Fields[name] = path
				}
			} else {
				// 已有字段，修正路径（如果 LLM 给出了更准确的路径）
				draft.Output.Fields[name] = path
			}
		}
	}
	return draft, nil
}

// isValidRiskLevel 校验风险等级是否合法。
func isValidRiskLevel(risk string) bool {
	switch risk {
	case "low", "medium", "high":
		return true
	default:
		return false
	}
}

const enrichmentSystemPrompt = `你是能力接口文档的助手。根据给定能力的信息，用简洁中文补全参数说明和输出配置，只返回合法 JSON。

输出字段要求：
- output_fields 是 "字段名": "JSONPath路径" 的映射
- JSONPath 使用 $.前缀，如 $.data.status
- 只保留对诊断/操作结果最有价值的字段（最多10个）
- 优先保留 status/name/id/code/result/message/error 等关键字段

风险等级：
- low: 纯查询、只读、无副作用
- medium: 配置变更、重启、扩容等有一定影响但可恢复
- high: 删除、强制操作、数据变更等不可逆或影响大的操作

summary 要求：
- 一句话中文描述操作结果
- 可用 {{字段名}} 引用输出字段，如 "状态: {{status}}"
`

// buildEnrichPrompt 把草稿的能力信息压缩进 prompt，让 LLM 补全元数据。
func buildEnrichPrompt(draft Capability) string {
	var fields []string
	for name, f := range draft.InputSchema {
		if name == "environment" {
			continue
		}
		fields = append(fields, fmt.Sprintf("%s(type=%s, required=%v)", name, f.Type, f.Required))
	}
	var outputFields []string
	for name, path := range draft.Output.Fields {
		outputFields = append(outputFields, fmt.Sprintf("%s=%s", name, path))
	}
	return fmt.Sprintf(
		"能力: name=%s domain=%s resource_type=%s operation=%s risk=%s\n"+
			"摘要: %s\n"+
			"输入参数: %s\n"+
			"自动推断的输出字段: %s\n\n"+
			"请返回 JSON: {\n"+
			"  \"description\":\"能力的中文一句描述\",\n"+
			"  \"summary\":\"结果摘要模板，可用 {{字段名}} 引用输出字段\",\n"+
			"  \"risk\":\"low/medium/high\",\n"+
			"  \"input_schema\":{\"参数名\":{\"description\":\"中文说明\",\"examples\":[\"示例值\"],\"enum\":[\"合法取值\"]}},\n"+
			"  \"output_fields\":{\"字段名\":\"$.JSONPath路径\"}\n"+
			"}",
		draft.Name, draft.Domain, draft.ResourceType, draft.Operation, draft.Risk,
		strings.TrimSpace(draft.AI.Description),
		strings.Join(fields, "; "),
		strings.Join(outputFields, "; "),
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
