package alert

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// StepResult 是链式诊断中单步的结果。
type StepResult struct {
	Tool   string
	Result map[string]any
	Err    error
}

// ChainDiagnoser 执行多步链式诊断：alert.query → event.query → domain.read。
// 前两步通过 ReadRunner 直接调用（alert/event 不是 domain 诊断工具），
// 第三步通过 diagnostics.Service.Run() 走域诊断链。
type ChainDiagnoser struct {
	diag     *diagnostics.Service
	alertSvc *Service
	readTool func(ctx context.Context, user identity.CurrentUser, toolName string, input map[string]any) (map[string]any, error)
}

// NewChainDiagnoser 创建多步链式诊断器。readTool 是从外部注入的只读执行函数，
// 通常由 ReadOnlyService.ExecuteRead 提供。
func NewChainDiagnoser(diag *diagnostics.Service, alertSvc *Service, readTool func(context.Context, identity.CurrentUser, string, map[string]any) (map[string]any, error)) *ChainDiagnoser {
	return &ChainDiagnoser{diag: diag, alertSvc: alertSvc, readTool: readTool}
}

// ChainDiagnose 对一条 firing 告警执行多步链式诊断，把结果写回 alert.Description。
func (d *ChainDiagnoser) ChainDiagnose(ctx context.Context, alert Alert) {
	if d.diag == nil || d.alertSvc == nil || d.readTool == nil {
		return
	}
	if alert.Status != StatusFiring {
		return
	}

	domain := alert.Domain
	if domain == "" {
		domain = guessDomain(alert)
	}
	if domain == "" {
		return
	}
	env := alert.Environment
	if env == "" {
		env = "prod"
	}

	user := identity.CurrentUser{
		Subject:             "alert-chain-diag",
		Roles:               []string{"admin"},
		AllowedEnvironments: []string{"prod", "staging", "dev"},
	}

	// 步骤 1：alert.query — 查当前相关告警
	alertResult := d.runReadStep(ctx, user, "alert.query", map[string]any{
		"environment": env,
		"severity":    "critical",
	})

	// 步骤 2：event.query — 查审计事件历史
	eventResult := d.runReadStep(ctx, user, "event.query", map[string]any{
		"environment": env,
	})

	// 步骤 3：domain.read — 采集域健康数据（走诊断链）
	domainPkg := d.runDomainStep(ctx, user, domain, env)

	// 聚合
	summary := d.buildChainSummary(alert, alertResult, eventResult, domainPkg)
	if summary == "" {
		return
	}

	desc := alert.Description
	if desc != "" {
		desc += "\n\n---\n\n[多步链式研判]\n" + summary
	} else {
		desc = "[多步链式研判]\n" + summary
	}
	_ = d.alertSvc.UpdateDescription(ctx, alert.ID, desc)
}

// runReadStep 通过 ReadRunner 直接调用只读工具。
func (d *ChainDiagnoser) runReadStep(ctx context.Context, user identity.CurrentUser, toolName string, input map[string]any) StepResult {
	result, err := d.readTool(ctx, user, toolName, input)
	if err != nil {
		log.Printf("[chain-diagnose] read step %q failed: %v", toolName, err)
		return StepResult{Tool: toolName, Err: err}
	}
	return StepResult{Tool: toolName, Result: result}
}

// runDomainStep 通过 diagnostics.Service.Run() 走域诊断链。
func (d *ChainDiagnoser) runDomainStep(ctx context.Context, user identity.CurrentUser, domain, env string) diagnostics.Package {
	pkg, err := d.diag.Run(ctx, user, diagnostics.Request{
		Domain:      domain,
		Environment: env,
	})
	if err != nil {
		log.Printf("[chain-diagnose] domain step %q failed: %v", domain, err)
		return diagnostics.Package{}
	}
	return pkg
}

// buildChainSummary 把三步诊断结果拼成可读摘要。
func (d *ChainDiagnoser) buildChainSummary(alert Alert, alertResult, eventResult StepResult, domainPkg diagnostics.Package) string {
	var sections []string

	// 告警关联
	if alertResult.Err == nil && alertResult.Result != nil {
		summary := summarizeToolResult("alert.query", alertResult.Result)
		if summary != "" {
			sections = append(sections, fmt.Sprintf("【告警关联】%s", summary))
		}
	} else if alertResult.Err != nil {
		sections = append(sections, fmt.Sprintf("【告警关联】查询失败: %v", alertResult.Err))
	}

	// 审计事件
	if eventResult.Err == nil && eventResult.Result != nil {
		summary := summarizeToolResult("event.query", eventResult.Result)
		if summary != "" {
			sections = append(sections, fmt.Sprintf("【审计事件】%s", summary))
		}
	} else if eventResult.Err != nil {
		sections = append(sections, fmt.Sprintf("【审计事件】查询失败: %v", eventResult.Err))
	}

	// 域健康
	if len(domainPkg.Observations) > 0 {
		sections = append(sections, fmt.Sprintf("【域健康】%s", domainPkg.Observations[0].Summary))
	}
	if len(domainPkg.Findings) > 0 {
		for _, f := range domainPkg.Findings {
			sections = append(sections, fmt.Sprintf("  发现: [%s] %s", f.Severity, f.Summary))
		}
	}
	if len(domainPkg.Recommendations) > 0 {
		for _, r := range domainPkg.Recommendations {
			riskTag := ""
			if r.Risk == tools.Medium {
				riskTag = " ⚠️"
			}
			sections = append(sections, fmt.Sprintf("  建议: %s (工具: %s)%s", r.Summary, r.ToolName, riskTag))
		}
	}

	return strings.Join(sections, "\n")
}

// summarizeToolResult 把工具返回的 map 拼成一句摘要。
func summarizeToolResult(toolName string, result map[string]any) string {
	if result == nil {
		return ""
	}
	// 优先用 result_summary
	if summary, ok := result["result_summary"].(string); ok && summary != "" {
		return summary
	}
	// 用 count 兜底
	if count, ok := result["count"].(float64); ok && count > 0 {
		return fmt.Sprintf("查询到 %.0f 条记录", count)
	}
	// 用 status 兜底
	if status, ok := result["status"].(string); ok && status != "" {
		return fmt.Sprintf("状态: %s", status)
	}
	return ""
}
