package alert

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
)

// StepResult 是链式执行中单步的结果。
type StepResult struct {
	Step   int
	Tool   string
	Result map[string]any
	Err    error
}

// ChainDiagnoser 从 AlertAction 的 ToolSequence 驱动多步执行。
// 诊断步骤（读）直接执行并收集结果；处置步骤（写）根据 ExecuteLastStep
// 决定是直接执行还是创建 PendingConfirmation plan。
type ChainDiagnoser struct {
	diag     *diagnostics.Service
	alertSvc *Service
	planExec PlanExecutor
	readTool func(ctx context.Context, user identity.CurrentUser, toolName string, input map[string]any) (map[string]any, error)
}

// PlanExecutor 是处置步骤的执行接口：创建 plan 或直接执行。
type PlanExecutor interface {
	CreatePlanForStep(ctx context.Context, alert Alert, action AlertAction, stepIdx int, stepResult []StepResult) error
}

// NewChainDiagnoser 创建多步链式诊断器。
func NewChainDiagnoser(
	diag *diagnostics.Service,
	alertSvc *Service,
	planExec PlanExecutor,
	readTool func(context.Context, identity.CurrentUser, string, map[string]any) (map[string]any, error),
) *ChainDiagnoser {
	return &ChainDiagnoser{
		diag:     diag,
		alertSvc: alertSvc,
		planExec: planExec,
		readTool: readTool,
	}
}

// ExecuteChain 对一条 firing 告警执行一条告警动作的完整序列。
func (d *ChainDiagnoser) ExecuteChain(ctx context.Context, alert Alert, action AlertAction) {
	if d.alertSvc == nil || d.readTool == nil {
		return
	}
	if alert.Status != StatusFiring {
		return
	}
	if len(action.ToolSequence) == 0 {
		return
	}

	user := identity.CurrentUser{
		Subject:             "alert-chain",
		Roles:               []string{"admin"},
		AllowedEnvironments: []string{"prod", "staging", "dev"},
	}

	var allResults []StepResult

	// 执行序列：前 N-1 步是诊断（只读），最后一步按配置决定
	for i, step := range action.ToolSequence {
		isLast := i == len(action.ToolSequence)-1

		input := step.RenderInput(alert)
		log.Printf("[alert-chain] step %d/%d: tool=%s input=%v", i+1, len(action.ToolSequence), step.Tool, input)

		result := d.executeStep(ctx, user, step.Tool, input)
		result.Step = i
		allResults = append(allResults, result)

		if result.Err != nil {
			log.Printf("[alert-chain] step %d failed: %v", i+1, result.Err)
			break // 步骤失败中断链
		}

		// 最后一步是处置（写）且不直接执行 → 建 plan
		if isLast && !action.ExecuteLastStep {
			if d.planExec != nil {
				if err := d.planExec.CreatePlanForStep(ctx, alert, action, i, allResults); err != nil {
					log.Printf("[alert-chain] create plan for step %d failed: %v", i+1, err)
				}
			}
		}
	}

	// 聚合诊断步骤的结果（排除最后处置步骤）写回 description
	diagResults := allResults
	if len(allResults) > 0 && !action.ExecuteLastStep {
		diagResults = allResults[:len(allResults)-1]
	}
	summary := d.buildSummary(alert, action, diagResults)
	if summary == "" {
		return
	}

	desc := alert.Description
	if desc != "" {
		desc += "\n\n---\n\n[链式研判:" + action.Name + "]\n" + summary
	} else {
		desc = "[链式研判:" + action.Name + "]\n" + summary
	}
	_ = d.alertSvc.UpdateDescription(ctx, alert.ID, desc)
}

// executeStep 执行单步：优先走 diagnostics.Service（如果是 domain 诊断），
// 否则直接调 ReadRunner。
func (d *ChainDiagnoser) executeStep(ctx context.Context, user identity.CurrentUser, toolName string, input map[string]any) StepResult {
	// 尝试走 diagnostics.Service（仅 domain 工具有效）
	domain := toolDomain(toolName)
	if domain != "" && d.diag != nil {
		pkg, err := d.diag.Run(ctx, user, diagnostics.Request{
			Domain:      domain,
			Environment: inputStr(input, "environment"),
		})
		if err == nil && len(pkg.Observations) > 0 {
			return StepResult{Tool: toolName, Result: map[string]any{
				"summary":     pkg.Observations[0].Summary,
				"findings":    len(pkg.Findings),
				"recs":        len(pkg.Recommendations),
			}}
		}
		// diagnostics 失败或不支持该 domain，回退到直接 ReadRunner
	}

	// 直接调 ReadRunner
	result, err := d.readTool(ctx, user, toolName, input)
	if err != nil {
		return StepResult{Tool: toolName, Err: err}
	}
	return StepResult{Tool: toolName, Result: result}
}

// toolDomain 判断工具名是否属于已知的 domain 诊断工具。
func toolDomain(toolName string) string {
	switch {
	case strings.HasPrefix(toolName, "kafka."):
		return "kafka"
	case strings.HasPrefix(toolName, "minio."):
		return "minio"
	case strings.HasPrefix(toolName, "glusterfs."):
		return "glusterfs"
	}
	return ""
}

func inputStr(input map[string]any, key string) string {
	if v, ok := input[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// buildSummary 把序列执行结果拼成可读摘要。
func (d *ChainDiagnoser) buildSummary(alert Alert, action AlertAction, results []StepResult) string {
	var sections []sections
	_ = sections

	var parts []string
	for _, r := range results {
		if r.Err != nil {
			parts = append(parts, fmt.Sprintf("  步骤%d (%s): 失败 - %v", r.Step+1, r.Tool, r.Err))
			continue
		}
		summary := summarizeResult(r.Result)
		parts = append(parts, fmt.Sprintf("  步骤%d (%s): %s", r.Step+1, r.Tool, summary))
	}

	if len(results) > 0 && !action.ExecuteLastStep {
		last := results[len(results)-1]
		if last.Err == nil {
			parts = append(parts, "\n  → 最后一步（处置）已创建待审批 plan，等待人工确认")
		}
	}

	return strings.Join(parts, "\n")
}

type sections = struct{}

func summarizeResult(result map[string]any) string {
	if result == nil {
		return "(无结果)"
	}
	if s, ok := result["summary"].(string); ok && s != "" {
		return s
	}
	if count, ok := result["count"].(float64); ok {
		return fmt.Sprintf("查询到 %.0f 条记录", count)
	}
	if status, ok := result["status"].(string); ok {
		return fmt.Sprintf("状态: %s", status)
	}
	return fmt.Sprintf("%v", result)
}
