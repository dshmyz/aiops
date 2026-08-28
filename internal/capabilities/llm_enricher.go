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
	Description  string                   `json:"description"`
	Summary      string                   `json:"summary,omitempty"`
	Risk         string                   `json:"risk,omitempty"`
	InputSchema  map[string]enrichedField `json:"input_schema"`
	OutputFields map[string]string        `json:"output_fields,omitempty"`
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

// probeResponseShape 是一次真实试调后交给 LLM 的上下文：草稿 + 脱敏后的
// 实际响应样本。LLM 据此生成与真实响应匹配的输出映射，而不是对着 Swagger
// 声明猜——后者常与实际响应（信封、字段命名）不一致。
type probeResponseShape struct {
	SummaryTemplate string            `json:"summary_template"`
	SeverityPath    string            `json:"severity_path,omitempty"`
	StatusMapping   map[string]string `json:"status_mapping,omitempty"`
	OutputFields    map[string]string `json:"output_fields"`
}

// InferOutputFromSample 用一次真实后端响应样本让 LLM 推断输出映射。
// 返回的映射经 extractPath 校验：路径取不到值的字段会被丢弃（LLM 幻觉防御），
// 全部无效或 LLM 失败时返回错误，由调用方回退到规则推断。
func InferOutputFromSample(ctx context.Context, chat ChatCompleter, draft Capability, sampleBody []byte) (OutputSpec, error) {
	out := draft.Output
	if chat == nil {
		return out, fmt.Errorf("no LLM configured")
	}
	// 响应样本截断，控制 prompt 体积
	sample := string(sampleBody)
	if len(sample) > 4096 {
		sample = sample[:4096] + "\n...（已截断）"
	}
	var inputDesc []string
	for name, f := range draft.InputSchema {
		inputDesc = append(inputDesc, fmt.Sprintf("%s(%s)", name, f.Type))
	}
	user := fmt.Sprintf(
		"接口: %s %s\n用途: %s\n输入参数: %s\n\n真实响应样本(JSON):\n%s\n\n"+
			"请基于真实响应样本推断输出映射，返回 JSON:\n"+
			"{\"summary_template\":\"中文摘要模板，可用 {字段名} 引用 output_fields 中的字段或输入参数\",\n"+
			" \"severity_path\":\"严重级别字段的 JSONPath（如 $.status），没有则留空\",\n"+
			" \"status_mapping\":{\"原始值\":\"ok/warning/critical/info\"}(可选，把非标准状态值归一),\n"+
			" \"output_fields\":{\"字段名\":\"$.JSONPath\"}}\n\n"+
			"规则：路径必须能在样本上取到值；优先 status/health/name/id/count/total/message 等诊断关键字段；最多 10 个。",
		strings.ToUpper(draft.Backend.Method), draft.Backend.Path,
		strings.TrimSpace(draft.AI.Description),
		strings.Join(inputDesc, "; "), sample,
	)
	response, err := chat.Complete(ctx, probeSystemPrompt, user)
	if err != nil {
		return out, err
	}
	jsonStr := extractEnrichJSON(response)
	var shape probeResponseShape
	if err := json.Unmarshal([]byte(jsonStr), &shape); err != nil {
		return out, err
	}
	// 幻觉防御：LLM 给的路径逐个在真实样本上验证，取不到值的丢弃
	var raw map[string]any
	if err := json.Unmarshal(sampleBody, &raw); err == nil {
		validated := make(map[string]string, len(shape.OutputFields))
		for name, path := range shape.OutputFields {
			if _, ok := extractPath(raw, path); ok {
				validated[name] = path
			}
		}
		shape.OutputFields = validated
	}
	if len(shape.OutputFields) == 0 {
		return out, fmt.Errorf("LLM 推断的输出映射在真实响应上全部无效")
	}
	if template := strings.TrimSpace(shape.SummaryTemplate); template != "" {
		out.SummaryTemplate = template
	}
	if path := strings.TrimSpace(shape.SeverityPath); path != "" {
		out.SeverityPath = path
	}
	if len(shape.StatusMapping) > 0 {
		out.StatusMapping = shape.StatusMapping
	}
	out.Fields = shape.OutputFields
	return out, nil
}

const probeSystemPrompt = `你是 API 能力映射专家。给你一个接口定义和一次真实响应样本，推断让运维 AI 能读懂该接口输出的字段映射。只返回合法 JSON，不要解释。
JSONPath 使用 $.前缀支持点路径，如 $.data.items[0].status。summary_template 用花括号 {字段名} 引用字段。`
