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
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// Diagnoser 执行自动研判：从告警提取诊断请求，调诊断服务，把结果写回告警 description，
// 并把可执行的处置推荐转成待确认的 action plan（补处置闭环）。
type Diagnoser struct {
	diag     *diagnostics.Service
	alertSvc *Service
	chat     model.BaseChatModel // 可选：LLM 用于智能 domain 推断
	planCreator RecommendationPlanCreator // 可选：处置闭环，未注入则不建 plan
}

// RecommendationPlanCreator 把诊断产出的可执行推荐转成待确认的 action plan。
// 由 main 装配实现（内部复用 tools.Lookup → policy.Evaluate → plans.CreatePlan），
// alert 包不直接依赖 plans，保持分层。
type RecommendationPlanCreator interface {
	CreateRecommendationPlan(ctx context.Context, user identity.CurrentUser, rec diagnostics.Recommendation) (planID string, err error)
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

// WithRecommendationPlanCreator 注入处置闭环：诊断产出的可执行推荐自动建 plan
// 等待人工确认。不注入则只出诊断结论，不建 plan（旧行为）。
func (d *Diagnoser) WithRecommendationPlanCreator(creator RecommendationPlanCreator) *Diagnoser {
	d.planCreator = creator
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

	req := diagnostics.Request{
		Domain: domain,
		Labels: alert.Labels,
	}
	if alert.ResourceType != "" {
		req.ResourceType = alert.ResourceType
	}
	if alert.ResourceName != "" {
		req.ResourceName = alert.ResourceName
	}

	// 内部可信身份触发诊断（只读取证 + 建待确认 plan）。
	user := d.autodiagUser()

	pkg, err := d.diag.Run(ctx, user, req)
	if err != nil {
		return // 诊断失败静默
	}

	summary := buildDiagnosisSummary(alert, pkg)
	if summary == "" {
		return
	}

	// 处置闭环：把诊断产出的可执行推荐转成待确认的 action plan（等待人工确认，
	// 不自动执行写操作）。每条推荐建 plan 的结果（planID 或失败原因）追加进
	// summary，让告警上能看到"建议了什么、落地成 plan 没有、为什么没落地"。
	if d.planCreator != nil {
		var created []string
		var failed []string
		for _, rec := range pkg.Recommendations {
			if rec.ToolName == "" {
				continue
			}
			planID, err := d.planCreator.CreateRecommendationPlan(ctx, d.autodiagUser(), rec)
			if err != nil {
				failed = append(failed, fmt.Sprintf("%s: %v", rec.ToolName, err))
				continue
			}
			created = append(created, fmt.Sprintf("%s (plan %s)", rec.ToolName, planID))
		}
		if len(created) > 0 {
			summary += "\n\n**建议处置（待确认）**：" + strings.Join(created, "、")
		}
		if len(failed) > 0 {
			summary += "\n**建议处置未落地**：" + strings.Join(failed, "；")
		}
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

// autodiagUser 返回自动研判触发的系统身份。内部可信链路（读取证 + 建待确认
// plan，写操作仍需人工确认），用系统身份而非借用真实用户身份。
func (d *Diagnoser) autodiagUser() identity.CurrentUser {
	return identity.CurrentUser{
		Subject: "alert-autodiag",
		Roles:   []string{"admin"},
	}
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
  "domain": "%s",
  "confidence": 0.0-1.0,
  "reasoning": "判断依据"
}

可用领域（来自系统已注册能力）：
%s
- unknown：无法判断

告警信息：
标题: %s
描述: %s
严重级别: %s
标签: %v`,
		strings.Join(append(tools.KnownDomains(), "unknown"), "|"),
		domainPromptList(),
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
// 关键词派生自工具注册表：域名本身 + 已注册工具的 Name/ResourceType，
// 不再硬编码具体中间件。未注册任何域时返回空串。
func guessDomain(alert Alert) string {
	// 优先用告警自身 domain
	if alert.Domain != "" {
		return alert.Domain
	}
	name := strings.ToLower(alert.Title + " " + alert.Labels["alertname"])
	for _, tool := range tools.All() {
		domain := tool.Domain
		if domain == "" {
			continue
		}
		if strings.Contains(name, domain) ||
			strings.Contains(name, strings.ToLower(tool.ResourceType)) ||
			strings.Contains(name, strings.ToLower(tool.Name)) {
			return domain
		}
	}
	return ""
}

// domainPromptList 生成 LLM 提示中的可用领域清单，派生自注册表。
func domainPromptList() string {
	var b strings.Builder
	for _, domain := range tools.KnownDomains() {
		b.WriteString("- ")
		b.WriteString(domain)
		b.WriteString("：系统已注册能力对应的运维领域\n")
	}
	b.WriteString("- unknown：无法判断\n")
	return b.String()
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
