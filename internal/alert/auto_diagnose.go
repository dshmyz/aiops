package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
)

// Diagnoser 执行自动研判：从告警提取诊断请求，调诊断服务，把结果写回告警 description。
type Diagnoser struct {
	diag     *diagnostics.Service
	alertSvc *Service
	chat     model.BaseChatModel // 可选：LLM 用于智能 domain 推断
}

// NewDiagnoser 创建自动研判器。
func NewDiagnoser(diag *diagnostics.Service, alertSvc *Service) *Diagnoser {
	return &Diagnoser{diag: diag, alertSvc: alertSvc}
}

// WithChatModel 注入 LLM 模型，用于智能 domain 推断。不注入则走关键词匹配。
func (d *Diagnoser) WithChatModel(chat model.BaseChatModel) *Diagnoser {
	d.chat = chat
	return d
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
		domain = d.inferDomain(ctx, alert)
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

// inferDomain 智能推断告警所属 domain。有 LLM 时用 LLM，否则走关键词匹配。
func (d *Diagnoser) inferDomain(ctx context.Context, alert Alert) string {
	// 优先用 LLM 推断
	if d.chat != nil {
		if domain := d.llmInferDomain(ctx, alert); domain != "" {
			return domain
		}
	}
	// fallback 到关键词匹配
	return guessDomain(alert)
}

// llmInferDomain 用 LLM 从告警上下文推断 domain。
func (d *Diagnoser) llmInferDomain(ctx context.Context, alert Alert) string {
	prompt := fmt.Sprintf(`分析以下告警，判断它属于哪个运维领域。只返回 JSON：
{
  "domain": "kafka|minio|glusterfs|moonlightbox|unknown",
  "confidence": 0.0-1.0,
  "reasoning": "判断依据"
}

可用领域：
- kafka：消息队列相关（消费者延迟、Topic、Broker）
- minio：对象存储相关（Bucket、存储容量）
- glusterfs：分布式文件系统（卷、存储）
- moonlightbox：制品仓库管理（包、代理、缓存）
- unknown：无法判断

告警信息：
标题: %s
描述: %s
严重级别: %s
标签: %v`,
		alert.Title, alert.Description, alert.Severity, alert.Labels)

	resp, err := d.chat.Generate(ctx, []*schema.Message{
		schema.SystemMessage("你是运维领域分类专家，只返回 JSON。"),
		schema.UserMessage(prompt),
	})
	if err != nil {
		return ""
	}

	var result struct {
		Domain     string  `json:"domain"`
		Confidence float64 `json:"confidence"`
	}
	if err := json.Unmarshal([]byte(resp.Content), &result); err != nil {
		return ""
	}
	if result.Domain == "" || result.Domain == "unknown" || result.Confidence < 0.5 {
		return ""
	}
	return result.Domain
}

// guessDomain 从告警 title/labels 关键词猜测 domain（LLM fallback）。
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
