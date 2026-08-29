package diagnostics

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// RecommendationResult carries the output of a recommendation generator. The
// Summary and Rationale fields are human-readable text; ToolName and
// CandidateInput, when non-empty, allow the caller to construct an action plan
// automatically.
type RecommendationResult struct {
	Summary        string
	Rationale      string
	ToolName       string
	CandidateInput map[string]any
}

// RecommendationGenerator 定义建议生成器接口
type RecommendationGenerator interface {
	Generate(ctx context.Context, domain, name string, severity Severity, observationData map[string]any) (RecommendationResult, error)
}

// TemplateRecommendationGenerator 使用模板生成建议（默认实现）
type TemplateRecommendationGenerator struct{}

func (g *TemplateRecommendationGenerator) Generate(_ context.Context, domain, name string, severity Severity, observationData map[string]any) (RecommendationResult, error) {
	summary := recommendationSummary(domain, severity, name)
	rationale := fmt.Sprintf("基于 %s 资源 %s 的诊断结果，严重级别为 %s", domain, name, severity)
	toolName, candidateInput := recommendationAction(domain, severity, name, observationData)
	return RecommendationResult{
		Summary:        summary,
		Rationale:      rationale,
		ToolName:       toolName,
		CandidateInput: candidateInput,
	}, nil
}

// recommendationAction returns a candidate tool name and input for an automated
// fix based on domain and severity. The fix tool is derived from the tool
// registry (never hardcoded): retention 语义的写工具优先（保留调整是诊断最常见
// 的"推荐修复"语义），否则回退该域第一个写工具。输入按 schema 填充所有必填
// 字段——资源标识字段用资源名，其余必填字段（如 retention_hours）从观察数据
// 同名键提取；无来源的必填字段留空，让建 plan 前的 ValidateInput 明确报缺参，
// 而不是生成一个确认后必然失败的残缺输入。When no write tool is registered
// for the given domain, toolName is empty.
func recommendationAction(domain string, severity Severity, name string, observationData map[string]any) (string, map[string]any) {
	if severity != SeverityCritical && severity != SeverityWarning {
		return "", nil
	}
	tool, ok := fixWriteToolForDomain(domain)
	if !ok {
		return "", nil
	}
	input := map[string]any{}
	if schema, ok := tools.DynamicInputSchema(tool.Name); ok {
		fields := make([]string, 0, len(schema))
		for field := range schema {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		for _, field := range fields {
			if !schema[field].Required {
				continue
			}
			// 资源标识字段：优先 tool.ResourceType 语义字段，回退 name。
			if field == tool.ResourceType || field == "name" {
				input[field] = name
				continue
			}
			// 其余必填字段：从观察数据同名键提取。适配器字段提取产出的是
			// 字符串（展示取向），按 schema 声明类型矫正为数值/布尔，否则
			// ValidateInput 的 integer/number 校验必拒。不可解析时原样保留，
			// 由 ValidateInput 明确报错（fail-loud 设计不变）。
			if v, present := observationData[field]; present {
				input[field] = coerceObservationValue(schema[field].Type, v)
			}
		}
	} else {
		input["name"] = name
	}
	return tool.Name, input
}

// fixWriteToolForDomain 返回域下最适合自动修复的写工具：优先 retention 语义
// 的保留类工具，否则回退该域第一个写工具。多个写工具注册时避免错绑无关操作。
func fixWriteToolForDomain(domain string) (tools.Tool, bool) {
	if strings.TrimSpace(domain) == "" {
		return tools.Tool{}, false
	}
	var fallback tools.Tool
	haveFallback := false
	for _, tool := range tools.All() {
		if tool.Domain != domain || tool.Operation != tools.Write {
			continue
		}
		if strings.Contains(strings.ToLower(tool.Name), "retention") {
			return tool, true
		}
		if !haveFallback {
			fallback = tool
			haveFallback = true
		}
	}
	return fallback, haveFallback
}

// LLMRecommendationGenerator 使用 LLM 生成动态建议
type LLMRecommendationGenerator struct {
	chat model.BaseChatModel
}

func NewLLMRecommendationGenerator(chat model.BaseChatModel) *LLMRecommendationGenerator {
	return &LLMRecommendationGenerator{chat: chat}
}

func (g *LLMRecommendationGenerator) Generate(ctx context.Context, domain, name string, severity Severity, observationData map[string]any) (RecommendationResult, error) {
	if g.chat == nil {
		return RecommendationResult{}, fmt.Errorf("LLM 模型未配置")
	}

	// 构建观察数据摘要
	dataJSON, _ := json.MarshalIndent(observationData, "", "  ")
	if len(dataJSON) > 1000 {
		dataJSON = dataJSON[:1000]
		dataJSON = append(dataJSON, []byte("...")...)
	}

	prompt := fmt.Sprintf(`你是一个中间件运维专家。根据诊断结果，生成简短的运维建议。

## 诊断信息
- 领域: %s
- 资源名称: %s
- 严重级别: %s
- 观察数据:
%s

## 输出要求
只返回JSON格式，不要包含其他文本：
{
  "summary": "一句话建议，不超过50字",
  "rationale": "建议理由，不超过100字"
}`, domain, name, severity, string(dataJSON))

	messages := []*schema.Message{
		schema.SystemMessage("你是一个专业的中间件运维助手，负责生成简洁、可操作的运维建议。"),
		schema.UserMessage(prompt),
	}

	response, err := g.chat.Generate(ctx, messages)
	if err != nil {
		return RecommendationResult{}, fmt.Errorf("LLM 生成建议失败: %w", err)
	}

	// 解析 LLM 返回的 JSON
	var result struct {
		Summary   string `json:"summary"`
		Rationale string `json:"rationale"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(response.Content)), &result); err != nil {
		return RecommendationResult{}, fmt.Errorf("解析 LLM 响应失败: %w", err)
	}

	if strings.TrimSpace(result.Summary) == "" {
		return RecommendationResult{}, fmt.Errorf("LLM 返回的建议为空")
	}

	// The LLM focuses on summary/rationale; the candidate tool/input come
	// from the deterministic template so the recommendation stays actionable.
	toolName, candidateInput := recommendationAction(domain, severity, name, observationData)

	return RecommendationResult{
		Summary:        result.Summary,
		Rationale:      result.Rationale,
		ToolName:       toolName,
		CandidateInput: candidateInput,
	}, nil
}

// HybridRecommendationGenerator 混合方案：优先使用 LLM，失败时回退到模板
type HybridRecommendationGenerator struct {
	llm      *LLMRecommendationGenerator
	template *TemplateRecommendationGenerator
}

func NewHybridRecommendationGenerator(chat model.BaseChatModel) *HybridRecommendationGenerator {
	return &HybridRecommendationGenerator{
		llm:      NewLLMRecommendationGenerator(chat),
		template: &TemplateRecommendationGenerator{},
	}
}

func (g *HybridRecommendationGenerator) Generate(ctx context.Context, domain, name string, severity Severity, observationData map[string]any) (RecommendationResult, error) {
	// 优先尝试 LLM 生成
	result, err := g.llm.Generate(ctx, domain, name, severity, observationData)
	if err == nil && result.Summary != "" {
		return result, nil
	}

	// LLM 失败时回退到模板
	return g.template.Generate(ctx, domain, name, severity, observationData)
}

// DeriveFixAction 供 alert 包的 LLM 研判处置闭环复用：按确定性模板同一规则
// 从注册表派生候选修复工具与入参（retention 语义写工具优先）。导出包装，
// 保持 recommendationAction 私有。
func DeriveFixAction(domain string, severity Severity, name string, observationData map[string]any) (string, map[string]any) {
	return recommendationAction(domain, severity, name, observationData)
}

// coerceObservationValue 把观测数据值矫正为动态工具 schema 声明的类型。
// 仅做无损转换：不可解析的字符串原样返回，交由 ValidateInput 报缺参/类型错。
func coerceObservationValue(fieldType string, v any) any {
	switch fieldType {
	case "integer":
		switch n := v.(type) {
		case string:
			if i, err := strconv.Atoi(strings.TrimSpace(n)); err == nil {
				return i
			}
		case float64:
			if i := int64(n); float64(i) == n {
				return i
			}
		case float32:
			if i := int64(n); float64(i) == float64(n) {
				return i
			}
		}
	case "number":
		switch n := v.(type) {
		case string:
			if f, err := strconv.ParseFloat(strings.TrimSpace(n), 64); err == nil {
				return f
			}
		case float32:
			return float64(n)
		}
	case "boolean":
		if b, ok := v.(string); ok {
			switch strings.ToLower(strings.TrimSpace(b)) {
			case "true":
				return true
			case "false":
				return false
			}
		}
	}
	return v
}
