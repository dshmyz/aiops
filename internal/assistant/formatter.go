package assistant

import (
	"context"
	"fmt"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// ToolFact 记录一次工具调用的事实（缺口-4: 事实集聚合）。
// 多工具场景（诊断 + 读 + 推荐）下，FormatRequest.FactSet 收集每个工具
// 的 name/input/result，让 CodeFallbackFormatter 遍历生成完整草稿，
// 而非只看单个 Answer。
type ToolFact struct {
	Tool   string
	Input  map[string]any
	Result map[string]any
}

// FormatRequest 是二阶段整形的输入，承载一阶段取证的事实和上下文。
type FormatRequest struct {
	// UserMessage 是用户原始问题
	UserMessage string
	// Tool 是一阶段调用的工具名
	Tool string
	// Answer 是一阶段工具返回的事实
	Answer map[string]any
	// FactSet 是多工具场景下的事实集（缺口-4）。为空时走单工具路径
	// （Tool + Answer），向后兼容；非空时 CodeFallbackFormatter 遍历
	// FactSet 生成每个工具的 tool_trace block，Summary 聚合所有事实。
	FactSet []ToolFact
	// SkillContents 是加载的 Skill SOP 内容（用于约束整形格式）
	SkillContents []string
}

// FormatResult 是二阶整形的输出，包含自然语言摘要和结构化 block。
type FormatResult struct {
	Summary string
	Blocks  []Block
}

// ResponseFormatter 是二阶段整形器接口。一阶段取证后调用 Format，
// 把事实转成面向用户的 Summary + 结构化 Blocks。
//
// 对齐 SxDevOps 的双阶段应答：一阶段 LLM 取证 → 二阶段 LLM + Skill 模板整形。
// 当 LLM 整形失败时，由代码兜底 Formatter 从 Answer 提取关键字段生成基础 Blocks。
type ResponseFormatter interface {
	Format(ctx context.Context, req FormatRequest) (FormatResult, error)
}

// CodeFallbackFormatter 是代码兜底整形器，不依赖 LLM。
// 从 Answer map 中提取关键字段（status/message/summary 等）生成 Summary，
// 并附带一个 tool_trace block 记录工具调用。
//
// 当 LLM 整形器失败或未配置时使用，保证用户始终能拿到结构化回复。
type CodeFallbackFormatter struct{}

// NewCodeFallbackFormatter 创建一个代码兜底整形器。
func NewCodeFallbackFormatter() *CodeFallbackFormatter {
	return &CodeFallbackFormatter{}
}

// Format 从 Answer 提取关键字段生成 Summary 和基础 Blocks。
// 永不返回 error（兜底必须成功），即使 Answer 为 nil 也返回默认 Summary。
//
// 当 FormatRequest.FactSet 非空时（缺口-4: 事实集聚合），遍历 FactSet
// 为每个 fact 生成一个 tool_trace block，Summary 聚合所有事实；FactSet
// 为空时走旧的单工具路径（Tool + Answer），向后兼容。
func (f *CodeFallbackFormatter) Format(_ context.Context, req FormatRequest) (FormatResult, error) {
	summary := extractSummary(req)
	var blocks []Block

	// 数据源查询工具（event.query/task.query/alert.query）的 answer 是大数组，
	// 前端用 ToolAnswerView 直接渲染 answer，无需重复塞进 tool_trace block，
	// 否则 answer + block payload 双份导致响应超 10KB 上限。
	if isDataSourceQueryTool(req.Tool) {
		return FormatResult{Summary: summary, Blocks: nil}, nil
	}

	if len(req.FactSet) > 0 {
		// 多工具事实集路径：为每个 fact 生成 tool_trace block
		for _, fact := range req.FactSet {
			blocks = append(blocks, Block{
				Type:    BlockToolTrace,
				Title:   "工具调用",
				Content: fact.Tool,
				Payload: map[string]any{
					"tool":   fact.Tool,
					"input":  fact.Input,
					"answer": fact.Result,
				},
			})
		}
		// 额外为含 status 的事实生成 incident_card
		for _, fact := range req.FactSet {
			if status, ok := fact.Result["status"]; ok {
				blocks = append([]Block{{
					Type:    BlockIncidentCard,
					Title:   "诊断结果",
					Content: fmt.Sprintf("%s: %v", fact.Tool, status),
					Payload: fact.Result,
				}}, blocks...)
			}
		}
	} else {
		// 单工具路径（向后兼容）
		blocks = []Block{
			{
				Type:    BlockToolTrace,
				Title:   "工具调用",
				Content: req.Tool,
				Payload: map[string]any{
					"tool":   req.Tool,
					"answer": req.Answer,
				},
			},
		}
		if status, ok := req.Answer["status"]; ok {
			blocks = append([]Block{{
				Type:    BlockIncidentCard,
				Title:   "诊断结果",
				Content: fmt.Sprintf("%v", status),
				Payload: req.Answer,
			}}, blocks...)
		}
	}

	return FormatResult{Summary: summary, Blocks: blocks}, nil
}

// extractSummary 从 Answer 或 FactSet 中按优先级提取关键字段生成 Summary。
// 当 FactSet 非空时，聚合多个事实的状态；否则走单工具 Answer 路径。
func extractSummary(req FormatRequest) string {
	// 优先从 FactSet 聚合（缺口-4）
	if len(req.FactSet) > 0 {
		parts := make([]string, 0, len(req.FactSet))
		for _, fact := range req.FactSet {
			part := extractFactSummary(fact)
			if part != "" {
				parts = append(parts, part)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "；")
		}
		// FactSet 都提取不到时，回退到单工具路径
	}

	if req.Answer == nil {
		return fmt.Sprintf("已执行 %s，未返回结构化结果。", req.Tool)
	}

	// 按优先级尝试常见字段
	for _, key := range []string{"summary", "message", "status", "result", "output"} {
		if v, ok := req.Answer[key]; ok {
			if s := strings.TrimSpace(fmt.Sprintf("%v", v)); s != "" {
				return s
			}
		}
	}

	// 所有字段都提取不到时，返回工具名 + 字段列表
	keys := make([]string, 0, len(req.Answer))
	for k := range req.Answer {
		keys = append(keys, k)
	}
	return fmt.Sprintf("已执行 %s，返回字段：%s", req.Tool, strings.Join(keys, ", "))
}

// extractFactSummary 从单个 ToolFact 提取摘要。
func extractFactSummary(fact ToolFact) string {
	if fact.Result == nil {
		return ""
	}
	for _, key := range []string{"summary", "message", "status", "result", "output"} {
		if v, ok := fact.Result[key]; ok {
			if s := strings.TrimSpace(fmt.Sprintf("%v", v)); s != "" {
				return fmt.Sprintf("%s: %s", fact.Tool, s)
			}
		}
	}
	return ""
}

// ChainedFormatter 串联两个 ResponseFormatter：primary 和 fallback。
// primary 成功（无 error 且 Summary 非空）时返回 primary 结果；
// primary 失败或返回空 Summary 时回退到 fallback。
// 对齐 SxDevOps 的"LLM 整形 → 失败回退代码兜底"链路。
type ChainedFormatter struct {
	primary  ResponseFormatter
	fallback ResponseFormatter
}

// NewChainedFormatter 创建一个串联整形器。primary 和 fallback 都不能为 nil。
func NewChainedFormatter(primary, fallback ResponseFormatter) *ChainedFormatter {
	if primary == nil {
		primary = NewCodeFallbackFormatter()
	}
	if fallback == nil {
		fallback = NewCodeFallbackFormatter()
	}
	return &ChainedFormatter{primary: primary, fallback: fallback}
}

// Format 先调 primary，成功则返回；失败或空结果时调 fallback。
func (c *ChainedFormatter) Format(ctx context.Context, req FormatRequest) (FormatResult, error) {
	result, err := c.primary.Format(ctx, req)
	if err == nil && strings.TrimSpace(result.Summary) != "" {
		return result, nil
	}
	// primary 失败或空，回退到 fallback
	return c.fallback.Format(ctx, req)
}

// WithLLMAudit 把 LLM 调用审计透传给 primary（若 primary 是 LLMFormatter）。
// 缺口-5 / R1：审计服务注入。
func (c *ChainedFormatter) WithLLMAudit(auditSvc *audit.Service, model string) {
	if inner, ok := c.primary.(*LLMFormatter); ok {
		inner.WithLLMAudit(auditSvc, model)
	}
}

// isDataSourceQueryTool 判断工具是否为数据源查询工具（返回大数组 answer，
// 前端用 ToolAnswerView 渲染，不应被 code fallback 重复塞进 tool_trace block）。
func isDataSourceQueryTool(toolName string) bool {
	switch toolName {
	case tools.AlertQuery, tools.EventQuery, tools.TaskQuery:
		return true
	default:
		return false
	}
}
