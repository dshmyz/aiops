package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// StepResult 是链式执行中单步的结果。
type StepResult struct {
	Step   int
	Tool   string
	Result map[string]any
	Err    error
	// Degraded 标记该步是降级结果（如域未接入返回通用检查框架），
	// 不是真实数据；摘要中应如实体现。
	Degraded bool
}

// ChainDiagnoser 从 AlertAction 的 ToolSequence 驱动多步执行。
// 诊断步骤（读）直接执行并收集结果；处置步骤（写）根据 ExecuteLastStep
// 决定是直接执行还是创建 PendingConfirmation plan。
type ChainDiagnoser struct {
	diag     *diagnostics.Service
	alertSvc *Service
	planExec PlanExecutor
	readTool func(ctx context.Context, user identity.CurrentUser, toolName string, input map[string]any) (map[string]any, error)
	runStore store.AlertActionRunStore
	now      func() time.Time
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
		now:      func() time.Time { return time.Now().UTC() },
	}
}

// WithRunStore 配置执行历史落库。不配置时执行链不记录历史（向后兼容）。
func (d *ChainDiagnoser) WithRunStore(s store.AlertActionRunStore) *ChainDiagnoser {
	d.runStore = s
	return d
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
		Subject: "alert-chain",
		Roles:   []string{"admin"},
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

	// 落执行历史（可选）：记录触发时间、命中告警、逐步结果与状态，供管理后台
	// 展示触发次数、成功率与最近执行快照。落库失败只记日志，不阻断响应链路。
	if d.runStore != nil {
		d.recordRun(ctx, alert, action, allResults, summary)
	}

	desc := alert.Description
	if desc != "" {
		desc += "\n\n---\n\n[链式研判:" + action.Name + "]\n" + summary
	} else {
		desc = "[链式研判:" + action.Name + "]\n" + summary
	}
	_ = d.alertSvc.UpdateDescription(ctx, alert.ID, desc)
}

// recordRun 把一次链式执行的历史写入 runStore。
func (d *ChainDiagnoser) recordRun(ctx context.Context, alert Alert, action AlertAction, results []StepResult, summary string) {
	type runStep struct {
		Step     int    `json:"step"`
		Tool     string `json:"tool"`
		Error    string `json:"error,omitempty"`
		Degraded bool   `json:"degraded,omitempty"`
	}
	steps := make([]runStep, 0, len(results))
	status := "success"
	for _, r := range results {
		rs := runStep{Step: r.Step, Tool: r.Tool, Degraded: r.Degraded}
		if r.Err != nil {
			rs.Error = r.Err.Error()
			status = "failure"
		}
		steps = append(steps, rs)
	}
	stepsRaw, err := json.Marshal(steps)
	if err != nil {
		log.Printf("[alert-chain] marshal run steps failed: %v", err)
		stepsRaw = []byte("[]")
	}
	title := alert.Title
	if title == "" {
		title = alert.Labels["alertname"]
	}
	rec := store.AlertActionRunRecord{
		RuleName:   action.Name,
		AlertID:    alert.ID,
		AlertTitle: title,
		Status:     status,
		Steps:      stepsRaw,
		Summary:    summary,
		CreatedAt:  d.now(),
	}
	if err := d.runStore.Append(ctx, rec); err != nil {
		log.Printf("[alert-chain] record run for rule %q failed: %v", action.Name, err)
	}
}

// executeStep 执行单步：优先走 diagnostics.Service（如果是 domain 诊断），
// 否则直接调 ReadRunner。
func (d *ChainDiagnoser) executeStep(ctx context.Context, user identity.CurrentUser, toolName string, input map[string]any) StepResult {
	// 尝试走 diagnostics.Service（仅 domain 工具有效）
	domain := toolDomain(toolName)
	if domain != "" && d.diag != nil {
		pkg, err := d.diag.Run(ctx, user, diagnostics.Request{
			Domain: domain,
		})
		if err == nil && len(pkg.Observations) > 0 {
			// 域未接入能力时 diagnostics 返回"通用检查框架"包（framework=true），
			// 不是实测数据。把它标为 degraded，而不是当成一次成功诊断步骤。
			framework, _ := pkg.Observations[0].Data["framework"].(bool)
			step := StepResult{Tool: toolName, Result: map[string]any{
				"summary":  pkg.Observations[0].Summary,
				"findings": len(pkg.Findings),
				"recs":     len(pkg.Recommendations),
			}}
			if framework {
				step.Degraded = true
			}
			return step
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
// 域清单派生自工具注册表：工具名以 "<domain>." 前缀匹配。
func toolDomain(toolName string) string {
	for _, domain := range tools.KnownDomains() {
		if strings.HasPrefix(toolName, domain+".") {
			return domain
		}
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
		if r.Degraded {
			parts = append(parts, fmt.Sprintf("  步骤%d (%s): %s（该域未接入精确诊断，为通用检查框架）", r.Step+1, r.Tool, summary))
			continue
		}
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
