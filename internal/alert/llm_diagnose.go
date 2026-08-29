package alert

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// LLMDiagnoser 用大模型做告警智能研判。
//
// 两阶段 LLM 调用：
//  1. DiagnosisPlan：分析告警上下文 → 决定查哪些 domain 的哪些 runbook
//  2. DiagnosisReport：综合诊断数据 → 生成结构化研判报告
//
// 任何阶段 LLM 失败都 fallback 到注入的确定性 Diagnoser（可 nil）。
type LLMDiagnoser struct {
	chat        model.BaseChatModel
	diag        *diagnostics.Service
	alertSvc    *Service
	fallback    *Diagnoser                // 可 nil，LLM 失败时的兜底
	audit       *diagLLMAuditRecorder     // 可 nil
	planCreator RecommendationPlanCreator // 可 nil，处置闭环：报告后自动建待确认 plan
}

// NewLLMDiagnoser 创建 LLM 智能研判器。chat 不能 nil。
func NewLLMDiagnoser(chat model.BaseChatModel, diag *diagnostics.Service, alertSvc *Service) *LLMDiagnoser {
	return &LLMDiagnoser{chat: chat, diag: diag, alertSvc: alertSvc}
}

// WithFallback 设置 LLM 失败时的确定性兜底研判器。
func (d *LLMDiagnoser) WithFallback(f *Diagnoser) *LLMDiagnoser {
	d.fallback = f
	return d
}

// WithRecommendationPlanCreator 注入处置闭环：LLM 报告产出后，把注册表派生的
// 修复候选（与确定性模板同一派生规则）转成待确认 action plan。修复前 LLM 路径
// 只写报告不建 plan——处置闭环在主研判路径上是断的。
func (d *LLMDiagnoser) WithRecommendationPlanCreator(creator RecommendationPlanCreator) *LLMDiagnoser {
	d.planCreator = creator
	return d
}

// WithAudit 启用 LLM 调用审计。
func (d *LLMDiagnoser) WithAudit(auditSvc *audit.Service, modelName string) *LLMDiagnoser {
	d.audit = newDiagLLMAuditRecorder(auditSvc, modelName)
	return d
}

// --- Prompt 1: 诊断计划 ---

// buildDiagnosisPlanPrompt 构造诊断计划提示词。可用领域清单派生自工具注册表，
// 不再硬编码具体中间件（kafka/minio/glusterfs/moonlightbox 等测试域）。
func buildDiagnosisPlanPrompt(title, desc, severity, labels, resourceType, resourceName, firedAt string, maxSteps int) string {
	return fmt.Sprintf(`你是中间件运维的告警研判助手。分析以下告警，决定需要执行哪些诊断检查。

## 可用的诊断领域
%s
## 告警信息
标题: %s
描述: %s
严重级别: %s
标签: %s
资源: %s/%s
触发时间: %s

## 输出要求
返回严格 JSON（无 markdown 代码块）:
{
  "diagnostic_steps": [
    {
      "domain": "%s",
      "runbook": "health|capacity|consumer_lag（可选，默认 health）",
      "reason": "为什么需要检查这个领域"
    }
  ],
  "confidence": 0.0-1.0,
  "reasoning": "对告警的初步分析"
}

规则：
- 最多 %d 个诊断步骤
- 只选择告警相关的领域，不要盲目全查
- 从告警标题/标签中识别涉及的中间件类型`,
		diagnosisDomainList(),
		title, desc, severity, labels, resourceType, resourceName, firedAt,
		diagnosisDomainEnum(),
		maxSteps,
	)
}

// diagnosisDomainList 生成提示中的可用领域描述清单。
func diagnosisDomainList() string {
	var b strings.Builder
	for _, domain := range tools.KnownDomains() {
		b.WriteString("- ")
		b.WriteString(domain)
		b.WriteString("：系统已注册能力对应的运维领域\n")
	}
	return b.String()
}

// diagnosisDomainEnum 生成输出 JSON 中 domain 字段的枚举值。
func diagnosisDomainEnum() string {
	domains := tools.KnownDomains()
	if len(domains) == 0 {
		return "string"
	}
	return strings.Join(domains, "|")
}

// diagnosisPlan 是 LLM 第一阶段的输出。
type diagnosisPlan struct {
	DiagnosticSteps []diagnosisStep `json:"diagnostic_steps"`
	Confidence      float64         `json:"confidence"`
	Reasoning       string          `json:"reasoning"`
}

type diagnosisStep struct {
	Domain  string `json:"domain"`
	Runbook string `json:"runbook"`
	Reason  string `json:"reason"`
}

// --- Prompt 2: 研判报告 ---

const llmDiagnosisReportPrompt = `你是中间件运维的告警研判助手。根据告警上下文和诊断数据，生成结构化的研判报告。

## 告警信息
标题: %s
描述: %s
严重级别: %s
资源: %s/%s

## 诊断计划
%s

## 诊断数据
%s

## 输出要求
返回严格 JSON（无 markdown 代码块）:
{
  "status": "ok|warning|critical",
  "summary": "一句话研判结论",
  "root_cause": "根因分析（基于数据，不要臆测）",
  "impact": "影响范围评估",
  "recommendations": [
    {
      "summary": "建议内容",
      "actionable": true/false,
      "tool_name": "可执行时填写工具名",
      "risk": "low|medium|high"
    }
  ]
}

规则：
- 所有结论必须基于诊断数据，不要编造
- 数据不足时明确说明"数据不足，建议进一步检查"
- actionable=true 的建议必须给出 tool_name
- 优先给出能帮助快速定位和止损的建议`

// diagnosisReport 是 LLM 第二阶段的输出。
type diagnosisReport struct {
	Status          string                 `json:"status"`
	Summary         string                 `json:"summary"`
	RootCause       string                 `json:"root_cause"`
	Impact          string                 `json:"impact"`
	Recommendations []reportRecommendation `json:"recommendations"`
}

type reportRecommendation struct {
	Summary    string `json:"summary"`
	Actionable bool   `json:"actionable"`
	ToolName   string `json:"tool_name"`
	Risk       string `json:"risk"`
}

// --- 主流程 ---

const (
	maxLLMDiagnosisSteps       = 3
	llmDiagnosisOverallTimeout = 120 * time.Second
)

// Diagnose 对一条 firing 告警执行 LLM 智能研判。
func (d *LLMDiagnoser) Diagnose(ctx context.Context, alert Alert) {
	if d.chat == nil {
		return
	}
	if alert.Status != StatusFiring {
		return
	}

	// 全流程超时保护
	ctx, cancel := context.WithTimeout(ctx, llmDiagnosisOverallTimeout)
	defer cancel()

	// 阶段 1：LLM 分析告警 → 诊断计划
	plan, err := d.planDiagnosis(ctx, alert)
	if err != nil {
		log.Printf("[alert-llm] plan diagnosis failed, fallback: %v", err)
		d.fallbackDiagnose(ctx, alert)
		return
	}
	if len(plan.DiagnosticSteps) == 0 {
		log.Printf("[alert-llm] LLM returned empty plan, fallback")
		d.fallbackDiagnose(ctx, alert)
		return
	}

	// 阶段 2：执行诊断步骤
	observations := d.executePlan(ctx, alert, plan)
	if len(observations) == 0 {
		log.Printf("[alert-llm] no observations collected, fallback")
		d.fallbackDiagnose(ctx, alert)
		return
	}

	// 阶段 3：LLM 综合分析 → 研判报告
	report, err := d.generateReport(ctx, alert, plan, observations)
	if err != nil {
		log.Printf("[alert-llm] generate report failed, fallback: %v", err)
		d.fallbackDiagnose(ctx, alert)
		return
	}

	// 写回告警；处置闭环结果并入描述（建议了什么、落地成 plan 没有、为什么没落地）。
	desc := d.formatReport(alert, report)
	if desc == "" {
		return
	}
	if d.planCreator != nil && d.alertSvc != nil {
		desc += "\n\n" + d.dispositionSummary(ctx, alert, report, observations)
	}
	if d.alertSvc != nil {
		_ = d.alertSvc.UpdateDescription(ctx, alert.ID, desc)
	}
}

// dispositionSummary 把报告结论经注册表派生的修复候选转成待确认 plan，
// 追加到研判描述。候选工具与确定性模板同源（recommendationAction，注册表
// 派生），LLM 报告里的工具名不采信——它可能编造注册表外工具。severity 取
// 报告结论状态；候选入参从观测数据同名键提取，缺必填时由建 plan 的
// ValidateInput 明确报错，不会带着残缺输入确认执行。
func (d *LLMDiagnoser) dispositionSummary(ctx context.Context, alert Alert, report diagnosisReport, observations []diagnostics.Observation) string {
	user := d.autodiagUser()
	severity := diagnostics.SeverityInfo
	switch strings.ToLower(strings.TrimSpace(report.Status)) {
	case "critical", "red":
		severity = diagnostics.SeverityCritical
	case "warning", "yellow":
		severity = diagnostics.SeverityWarning
	}
	merged := map[string]any{}
	for _, obs := range observations {
		for k, v := range obs.Data {
			merged[k] = v
		}
		// 能力读结果的业务字段嵌在 data 子键（{kind,severity,summary,data:{...}}），
		// 扁平化合并让 retention_hours 等字段能被候选入参提取。
		if nested, ok := obs.Data["data"].(map[string]any); ok {
			for k, v := range nested {
				merged[k] = v
				// 适配器字段提取把嵌套对象打平成 "data.X" 点号键存进 data，
				// 提升为裸键让 retention_hours 等字段能被候选入参提取。
				if name, ok := strings.CutPrefix(k, "data."); ok {
					if _, exists := merged[name]; !exists {
						merged[name] = v
					}
				}
			}
		}
	}
	toolName, candidateInput := diagnostics.DeriveFixAction(alert.Domain, severity, alert.ResourceName, merged)
	if toolName == "" {
		return "**建议处置**：无（域内没有可自动派生的修复工具，或严重级别未达处置阈值）"
	}
	planID, err := d.planCreator.CreateRecommendationPlan(ctx, user, diagnostics.Recommendation{
		ID:             "rec-" + alert.ID,
		Summary:        report.Summary,
		Rationale:      report.RootCause,
		ToolName:       toolName,
		CandidateInput: candidateInput,
	})
	if err != nil {
		return "**建议处置未落地**：" + toolName + ": " + err.Error()
	}
	log.Printf("[alert-auto-plan] created plan %s from LLM report (tool=%s)", planID[:8], toolName)
	return "**建议处置（待确认）**：" + toolName + " (plan " + planID + ")"
}

// autodiagUser 与确定性 Diagnoser 一致：内部可信身份，只读 + 建待确认 plan。
func (d *LLMDiagnoser) autodiagUser() identity.CurrentUser {
	return identity.CurrentUser{
		Subject: "alert-llm-diag",
		Roles:   []string{"admin"},
	}
}

// planDiagnosis 调 LLM 生成诊断计划。
func (d *LLMDiagnoser) planDiagnosis(ctx context.Context, alert Alert) (diagnosisPlan, error) {
	userMsg := buildDiagnosisPlanPrompt(
		alert.Title,
		alert.Description,
		string(alert.Severity),
		formatLabels(alert.Labels),
		alert.ResourceType,
		alert.ResourceName,
		alert.FiredAt.Format(time.RFC3339),
		maxLLMDiagnosisSteps,
	)

	messages := []*schema.Message{
		schema.SystemMessage("你是中间件运维告警研判助手，只返回 JSON。"),
		schema.UserMessage(userMsg),
	}

	started := time.Now()
	resp, err := d.chat.Generate(ctx, messages)
	if err != nil {
		return diagnosisPlan{}, fmt.Errorf("LLM plan generate: %w", err)
	}
	d.recordAudit(ctx, started, resp, "diagnosis_plan")

	var plan diagnosisPlan
	if err := parseLLMJSON(resp, &plan); err != nil {
		return diagnosisPlan{}, fmt.Errorf("parse diagnosis plan: %w", err)
	}
	return plan, nil
}

// executePlan 执行诊断计划中的每个步骤，收集 Observations。
func (d *LLMDiagnoser) executePlan(ctx context.Context, alert Alert, plan diagnosisPlan) []diagnostics.Observation {
	user := identity.CurrentUser{
		Subject: "alert-llm-diag",
		Roles:   []string{"admin"},
	}

	var observations []diagnostics.Observation
	for _, step := range plan.DiagnosticSteps {
		if d.diag == nil {
			continue
		}
		req := diagnostics.Request{
			Domain:  step.Domain,
			Runbook: step.Runbook,
			Labels:  alert.Labels,
		}
		// 资源类型决定 resolver 选哪个诊断读能力：缺失时按域取第一个匹配，
		// topic 告警会被误读成 consumer_group（orders 被当组名查 lag）。
		if alert.ResourceType != "" {
			req.ResourceType = alert.ResourceType
		}
		if alert.ResourceName != "" {
			req.ResourceName = alert.ResourceName
		}

		pkg, err := d.diag.Run(ctx, user, req)
		if err != nil {
			log.Printf("[alert-llm] step domain=%s runbook=%s resource=%s failed: %v", step.Domain, step.Runbook, req.ResourceName, err)
			continue
		}
		observations = append(observations, pkg.Observations...)
	}
	return observations
}

// generateReport 调 LLM 生成研判报告。
func (d *LLMDiagnoser) generateReport(ctx context.Context, alert Alert, plan diagnosisPlan, observations []diagnostics.Observation) (diagnosisReport, error) {
	planJSON, _ := json.Marshal(plan)
	obsJSON, _ := json.Marshal(observations)

	userMsg := fmt.Sprintf(llmDiagnosisReportPrompt,
		alert.Title,
		alert.Description,
		alert.Severity,
		alert.ResourceType,
		alert.ResourceName,
		string(planJSON),
		string(obsJSON),
	)

	messages := []*schema.Message{
		schema.SystemMessage("你是中间件运维告警研判助手，只返回 JSON。"),
		schema.UserMessage(userMsg),
	}

	started := time.Now()
	resp, err := d.chat.Generate(ctx, messages)
	if err != nil {
		return diagnosisReport{}, fmt.Errorf("LLM report generate: %w", err)
	}
	d.recordAudit(ctx, started, resp, "diagnosis_report")

	var report diagnosisReport
	if err := parseLLMJSON(resp, &report); err != nil {
		return diagnosisReport{}, fmt.Errorf("parse diagnosis report: %w", err)
	}
	return report, nil
}

// formatReport 把 LLM 研判报告格式化为写回 alert.Description 的文本。
func (d *LLMDiagnoser) formatReport(alert Alert, report diagnosisReport) string {
	var parts []string
	parts = append(parts, fmt.Sprintf("**状态**: %s", report.Status))
	parts = append(parts, fmt.Sprintf("**摘要**: %s", report.Summary))

	if report.RootCause != "" {
		parts = append(parts, fmt.Sprintf("\n**根因分析**: %s", report.RootCause))
	}
	if report.Impact != "" {
		parts = append(parts, fmt.Sprintf("\n**影响范围**: %s", report.Impact))
	}

	if len(report.Recommendations) > 0 {
		parts = append(parts, "\n**建议**:")
		for i, rec := range report.Recommendations {
			icon := "📋"
			suffix := ""
			if rec.Actionable && rec.ToolName != "" {
				icon = "🔧"
				suffix = fmt.Sprintf(" (风险: %s, 工具: %s)", rec.Risk, rec.ToolName)
			}
			parts = append(parts, fmt.Sprintf("%d. %s %s%s", i+1, icon, rec.Summary, suffix))
		}
	}

	summary := strings.Join(parts, "\n")

	desc := alert.Description
	if desc != "" {
		desc += "\n\n---\n\n[LLM 研判]\n" + summary
	} else {
		desc = "[LLM 研判]\n" + summary
	}
	return desc
}

// fallbackDiagnose 调用确定性兜底研判器。fallback 拿到独立的超时预算：
// 它通常在 LLM 各阶段耗尽 llmDiagnosisOverallTimeout 之后才被调用，
// 复用同一个 ctx 会让兜底在起跑线上就 context deadline exceeded（实测：
// 报告阶段超时后 fallback 静默失败，告警连摘要都没有）。
func (d *LLMDiagnoser) fallbackDiagnose(ctx context.Context, alert Alert) {
	if d.fallback == nil {
		return
	}
	fallbackCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), llmDiagnosisOverallTimeout)
	defer cancel()
	d.fallback.Diagnose(fallbackCtx, alert)
}

// --- 工具函数 ---

// parseLLMJSON 从 LLM 响应中解析 JSON，支持 markdown 代码块包裹。
// 当 content 为空但 reasoning_content 存在时，尝试从 reasoning 中提取。
func parseLLMJSON(resp *schema.Message, target any) error {
	if resp == nil {
		return fmt.Errorf("nil LLM response")
	}
	text := strings.TrimSpace(resp.Content)
	// content 为空时，尝试从 reasoning_content 提取（推理模型常见格式）
	if text == "" && resp.ReasoningContent != "" {
		text = strings.TrimSpace(resp.ReasoningContent)
	}
	if text == "" {
		return fmt.Errorf("empty LLM response content")
	}
	// 去掉 markdown 代码块包裹
	if strings.HasPrefix(text, "```") {
		lines := strings.SplitN(text, "\n", 2)
		if len(lines) > 1 {
			text = lines[1]
		}
		if idx := strings.LastIndex(text, "```"); idx >= 0 {
			text = text[:idx]
		}
		text = strings.TrimSpace(text)
	}
	return json.Unmarshal([]byte(text), target)
}

// formatLabels 把 Labels map 格式化为可读文本。
func formatLabels(labels map[string]string) string {
	if len(labels) == 0 {
		return "(无)"
	}
	parts := make([]string, 0, len(labels))
	for k, v := range labels {
		parts = append(parts, k+"="+v)
	}
	return strings.Join(parts, ", ")
}

// recordAudit 记录 LLM 调用审计（可 nil）。
func (d *LLMDiagnoser) recordAudit(ctx context.Context, started time.Time, resp *schema.Message, component string) {
	if d.audit != nil {
		d.audit.record(ctx, started, resp, component)
	}
}

// --- LLM 审计 ---

// diagLLMAuditRecorder 记录告警研判 LLM 调用的审计事件。
type diagLLMAuditRecorder struct {
	audit *audit.Service
	model string
	now   func() time.Time
}

func newDiagLLMAuditRecorder(audit *audit.Service, model string) *diagLLMAuditRecorder {
	return &diagLLMAuditRecorder{audit: audit, model: model, now: time.Now}
}

func (r *diagLLMAuditRecorder) record(ctx context.Context, started time.Time, resp *schema.Message, component string) {
	if r == nil || r.audit == nil {
		return
	}
	metadata := map[string]any{
		"model":      r.model,
		"component":  "alert_" + component,
		"latency_ms": time.Since(started).Milliseconds(),
	}
	if resp != nil && resp.ResponseMeta != nil && resp.ResponseMeta.Usage != nil {
		metadata["prompt_tokens"] = resp.ResponseMeta.Usage.PromptTokens
		metadata["completion_tokens"] = resp.ResponseMeta.Usage.CompletionTokens
		metadata["total_tokens"] = resp.ResponseMeta.Usage.TotalTokens
	}
	event := audit.Event{
		ID:        fmt.Sprintf("llm-alert-%d", started.UnixNano()),
		Subject:   "system:llm:alert-diag",
		Action:    audit.ActionLLMInvoked,
		Decision:  audit.DecisionPermitted,
		Metadata:  metadata,
		CreatedAt: r.now(),
	}
	_ = r.audit.Record(ctx, event)
}
