package alert

import (
	"context"
	"fmt"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
)

// Diagnoser 执行自动研判：从告警提取诊断请求，调诊断服务，把结果写回告警 description。
type Diagnoser struct {
	diag     *diagnostics.Service
	alertSvc *Service
}

// NewDiagnoser 创建自动研判器。
func NewDiagnoser(diag *diagnostics.Service, alertSvc *Service) *Diagnoser {
	return &Diagnoser{diag: diag, alertSvc: alertSvc}
}

// Diagnose 对一条 firing 告警做自动研判，把诊断结果摘要写回 alert.Description。
// 研判失败只记日志，不阻断告警接入。
func (d *Diagnoser) Diagnose(ctx context.Context, alert Alert) {
	if d.diag == nil || d.alertSvc == nil {
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
		return // 没有 domain 无法诊断
	}

	env := alert.Environment
	if env == "" {
		env = "prod"
	}

	req := diagnostics.Request{
		Domain:      domain,
		Environment: env,
	}
	if alert.ResourceType != "" {
		req.ResourceType = alert.ResourceType
	}
	if alert.ResourceName != "" {
		req.ResourceName = alert.ResourceName
	}

	// 管理员身份触发诊断
	user := identity.CurrentUser{
		Subject:              "alert-autodiag",
		Roles:                []string{"admin"},
		AllowedEnvironments:  []string{"prod", "staging", "dev"},
	}

	pkg, err := d.diag.Run(ctx, user, req)
	if err != nil {
		return // 诊断失败静默
	}

	summary := buildDiagnosisSummary(alert, pkg)
	if summary == "" {
		return
	}

	// 追加到原有 description
	desc := alert.Description
	if desc != "" {
		desc += "\n\n---\n\n[自动研判]\n" + summary
	} else {
		desc = "[自动研判]\n" + summary
	}
	_ = d.alertSvc.UpdateDescription(ctx, alert.ID, desc)
}

// guessDomain 从告警 title/labels 猜测 domain。
func guessDomain(alert Alert) string {
	// 优先用告警自身 domain
	if alert.Domain != "" {
		return alert.Domain
	}
	// 从 alertname 关键词猜
	name := strings.ToLower(alert.Title + " " + alert.Labels["alertname"])
	switch {
	case strings.Contains(name, "kafka") || strings.Contains(name, "consumer"):
		return "kafka"
	case strings.Contains(name, "minio") || strings.Contains(name, "bucket"):
		return "minio"
	case strings.Contains(name, "glusterfs") || strings.Contains(name, "volume"):
		return "glusterfs"
	}
	return ""
}

// buildDiagnosisSummary 把诊断 Package 拼成可读摘要。
func buildDiagnosisSummary(alert Alert, pkg diagnostics.Package) string {
	var parts []string

	if len(pkg.Findings) > 0 {
		findings := make([]string, 0, len(pkg.Findings))
		for _, f := range pkg.Findings {
			findings = append(findings, fmt.Sprintf("- [%s] %s", f.Severity, f.Summary))
		}
		parts = append(parts, "发现：\n"+strings.Join(findings, "\n"))
	}

	if len(pkg.Recommendations) > 0 {
		recs := make([]string, 0, len(pkg.Recommendations))
		for _, r := range pkg.Recommendations {
			recs = append(recs, fmt.Sprintf("- %s (工具: %s)", r.Summary, r.ToolName))
		}
		parts = append(parts, "建议：\n"+strings.Join(recs, "\n"))
	}

	return strings.Join(parts, "\n\n")
}
