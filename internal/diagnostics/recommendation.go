package diagnostics

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
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
	Rationale       string
	ToolName       string
	CandidateInput map[string]any
}

// RecommendationGenerator 定义建议生成器接口
type RecommendationGenerator interface {
	Generate(ctx context.Context, domain, name, environment string, severity Severity, observationData map[string]any) (RecommendationResult, error)
}

// TemplateRecommendationGenerator 使用模板生成建议（默认实现）
type TemplateRecommendationGenerator struct{}

func (g *TemplateRecommendationGenerator) Generate(_ context.Context, domain, name, environment string, severity Severity, _ map[string]any) (RecommendationResult, error) {
	summary := recommendationSummary(domain, severity, name)
	rationale := fmt.Sprintf("基于 %s 资源 %s 的诊断结果，严重级别为 %s", domain, name, severity)
	toolName, candidateInput := recommendationAction(domain, severity, environment, name)
	return RecommendationResult{
		Summary:        summary,
		Rationale:      rationale,
		ToolName:       toolName,
		CandidateInput: candidateInput,
	}, nil
}

// recommendationAction returns a candidate tool name and input for an automated
// fix based on domain and severity. The fix tool is derived from the tool
// registry (any write tool registered for the domain), never hardcoded. When no
// write tool is registered for the given domain, toolName is empty.
func recommendationAction(domain string, severity Severity, environment, name string) (string, map[string]any) {
	if severity != SeverityCritical && severity != SeverityWarning {
		return "", nil
	}
	tool, ok := tools.FindDomainWriteTool(domain)
	if !ok {
		return "", nil
	}
	input := map[string]any{"environment": environment}
	if schema, ok := tools.DynamicInputSchema(tool.Name); ok {
		fields := make([]string, 0, len(schema))
		for field := range schema {
			if field != "environment" {
				fields = append(fields, field)
			}
		}
		sort.Strings(fields)
		switch {
		case fieldInSchema(fields, tool.ResourceType):
			input[tool.ResourceType] = name
		case fieldInSchema(fields, "name"):
			input["name"] = name
		case len(fields) > 0:
			input[fields[0]] = name
		default:
			input["name"] = name
		}
	} else {
		input["name"] = name
	}
	return tool.Name, input
}

func fieldInSchema(fields []string, target string) bool {
	for _, f := range fields {
		if f == target {
			return true
		}
	}
	return false
}

// LLMRecommendationGenerator 使用 LLM 生成动态建议
type LLMRecommendationGenerator struct {
	chat model.BaseChatModel
}

func NewLLMRecommendationGenerator(chat model.BaseChatModel) *LLMRecommendationGenerator {
	return &LLMRecommendationGenerator{chat: chat}
}

func (g *LLMRecommendationGenerator) Generate(ctx context.Context, domain, name, environment string, severity Severity, observationData map[string]any) (RecommendationResult, error) {
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
	toolName, candidateInput := recommendationAction(domain, severity, environment, name)

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

func (g *HybridRecommendationGenerator) Generate(ctx context.Context, domain, name, environment string, severity Severity, observationData map[string]any) (RecommendationResult, error) {
	// 优先尝试 LLM 生成
	result, err := g.llm.Generate(ctx, domain, name, environment, severity, observationData)
	if err == nil && result.Summary != "" {
		return result, nil
	}

	// LLM 失败时回退到模板
	return g.template.Generate(ctx, domain, name, environment, severity, observationData)
}
