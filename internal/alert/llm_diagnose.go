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
)

// LLMDiagnoser 用大模型做告警智能研判。
//
// 两阶段 LLM 调用：
//  1. DiagnosisPlan：分析告警上下文 → 决定查哪些 domain 的哪些 runbook
//  2. DiagnosisReport：综合诊断数据 → 生成结构化研判报告
//
// 任何阶段 LLM 失败都 fallback 到注入的确定性 Diagnoser（可 nil）。
type LLMDiagnoser struct {
	chat     model.BaseChatModel
	diag     *diagnostics.Service
	alertSvc *Service
	fallback *Diagnoser            // 可 nil，LLM 失败时的兜底
	audit    *diagLLMAuditRecorder // 可 nil
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

// WithAudit 启用 LLM 调用审计。
func (d *LLMDiagnoser) WithAudit(auditSvc *audit.Service, modelName string) *LLMDiagnoser {
	d.audit = newDiagLLMAuditRecorder(auditSvc, modelName)
	return d
}

// --- Prompt 1: 诊断计划 ---

const llmDiagnosisPlanPrompt = `你是中间件运维的告警研判助手。分析以下告警，决定需要执行哪些诊断检查。

## 可用的诊断领域
- kafka：消息队列（消费者组积压、Topic 健康）
- minio：对象存储（桶健康、存储容量）
- glusterfs：分布式文件系统（卷健康、容量）
- moonlightbox：制品仓库管理（仓库健康状态、缓存命中率、安全漏洞、下载统计）

## 告警信息
标题: %s
描述: %s
严重级别: %s
环境: %s
标签: %s
资源: %s/%s
触发时间: %s

## 输出要求
返回严格 JSON（无 markdown 代码块）:
{
  "diagnostic_steps": [
    {
      "domain": "kafka|minio|glusterfs",
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
- 从告警标题/标签中识别涉及的中间件类型
- moonlightbox 相关告警（制品、仓库、npm/maven/pypi、缓存、安全扫描）优先查 moonlightbox 领域`

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
环境: %s
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
	Status         string               `json:"status"`
	Summary        string               `json:"summary"`
	RootCause      string               `json:"root_cause"`
	Impact         string               `json:"impact"`
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
	maxLLMDiagnosisSteps        = 3
	llmDiagnosisOverallTimeout  = 120 * time.Second
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

	// 写回告警
	desc := d.formatReport(alert, report)
	if desc == "" {
		return
	}
	if d.alertSvc != nil {
		_ = d.alertSvc.UpdateDescription(ctx, alert.ID, desc)
	}
}

// planDiagnosis 调 LLM 生成诊断计划。
func (d *LLMDiagnoser) planDiagnosis(ctx context.Context, alert Alert) (diagnosisPlan, error) {
	userMsg := fmt.Sprintf(llmDiagnosisPlanPrompt,
		alert.Title,
		alert.Description,
		alert.Severity,
		alert.Environment,
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
		Subject:             "alert-llm-diag",
		Roles:               []string{"admin"},
		AllowedEnvironments: []string{"prod", "staging", "dev"},
	}

	env := alert.Environment
	if env == "" {
		env = "prod"
	}

	var observations []diagnostics.Observation
	for _, step := range plan.DiagnosticSteps {
		if d.diag == nil {
			continue
		}
		req := diagnostics.Request{
			Domain:      step.Domain,
			Environment: env,
			Runbook:     step.Runbook,
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
		alert.Environment,
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

// fallbackDiagnose 调用确定性兜底研判器。
func (d *LLMDiagnoser) fallbackDiagnose(ctx context.Context, alert Alert) {
	if d.fallback != nil {
		d.fallback.Diagnose(ctx, alert)
	}
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
