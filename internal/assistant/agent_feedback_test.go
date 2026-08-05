package assistant

import (
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
)

// feedbackText must hand the replanning LLM both the actual tool result and an
// explicit instruction to conclude (final_answer) when that result already
// answers the user's question — this is what stops a single-domain diagnostic
// from being repeated until maxSteps is exhausted.
func TestFeedbackTextInstructsConclusionWithResult(t *testing.T) {
	t.Parallel()
	out := StepOutcome{Tool: "glusterfs.volume.health.read", Summary: "glusterfs 资源 data 状态为 warning"}
	text := feedbackText(out)
	if !strings.Contains(text, out.Summary) {
		t.Fatalf("feedbackText %q does not carry the tool result %q", text, out.Summary)
	}
	if !strings.Contains(text, "final_answer: true") {
		t.Fatalf("feedbackText %q does not instruct final_answer: true", text)
	}
	if !strings.Contains(text, "不要重复执行") {
		t.Fatalf("feedbackText %q does not warn against re-running the tool", text)
	}
}

func TestFeedbackTextHandlesEmptyResult(t *testing.T) {
	t.Parallel()
	text := feedbackText(StepOutcome{})
	if text == "" {
		t.Fatal("feedbackText returned empty for empty step")
	}
	if !strings.Contains(text, "final_answer: true") {
		t.Fatalf("feedbackText %q does not instruct final_answer: true", text)
	}
}

// feedbackTurn must mark the step as assistant/tool_step so the planner sees a
// concrete assistant turn carrying the tool result.
func TestFeedbackTurnCarriesToolResult(t *testing.T) {
	t.Parallel()
	intent := Intent{ToolName: "glusterfs.volume.health.read"}
	out := StepOutcome{Tool: intent.ToolName, Summary: "glusterfs 资源 data 状态为 warning"}
	turn := feedbackTurn(intent, out)
	if turn.Role != "assistant" {
		t.Fatalf("role = %q, want assistant", turn.Role)
	}
	if turn.ResponseType != "tool_step" {
		t.Fatalf("response type = %q, want tool_step", turn.ResponseType)
	}
	if turn.Content == "" {
		t.Fatal("turn content empty, want feedback text")
	}
	if turn.Intent == nil || turn.Intent.ToolName != intent.ToolName {
		t.Fatalf("turn intent = %+v, want to carry the executed intent", turn.Intent)
	}
}

// A diagnostic step must feed its structured evidence (severity, per-observation
// and per-finding lines) back to the replanning LLM, not just a one-line
// summary — this is what lets a later step converge into the SOP's 候选根因表
// with real data instead of the values existing only in the prompt text.
func TestFeedbackTextCarriesDiagnosticFindings(t *testing.T) {
	t.Parallel()
	out := StepOutcome{
		Tool:    "GlusterVolumeHealthRead",
		Summary: "诊断完成：glusterfs volume data 状态为 warning",
		Output: map[string]any{
			"severity":     "warning",
			"domains":      []string{"glusterfs"},
			"observations": []string{"glusterfs 资源 data 状态为 warning", "glusterfs 资源 data 容量使用率 85%"},
			"findings":     []string{"glusterfs 资源 data 需要运维人员检查"},
		},
	}
	text := feedbackText(out)
	if !strings.Contains(text, "综合严重级别：warning") {
		t.Fatalf("feedbackText %q missing severity", text)
	}
	if !strings.Contains(text, "容量使用率 85%") {
		t.Fatalf("feedbackText %q missing per-observation line", text)
	}
	if !strings.Contains(text, "需要运维人员检查") {
		t.Fatalf("feedbackText %q missing per-finding line", text)
	}
	if !strings.Contains(text, "诊断域：glusterfs") {
		t.Fatalf("feedbackText %q missing domain line", text)
	}
	// 既有契约不因附加结构而被破坏。
	if !strings.Contains(text, "final_answer: true") {
		t.Fatalf("feedbackText %q does not still instruct final_answer: true", text)
	}
}

// A plain read step (no diagnostic structure in the output) must stay one line —
// no fabricated evidence block.
func TestFeedbackTextPlainReadHasNoEvidenceBlock(t *testing.T) {
	t.Parallel()
	out := StepOutcome{
		Tool:    "minio.bucket.health.read",
		Summary: "minio 资源 bucket 状态为 ok",
		Output: map[string]any{
			"environment": "prod",
			"name":        "bucket",
		},
	}
	text := feedbackText(out)
	if strings.Contains(text, "取证结果如下") {
		t.Fatalf("plain read should not carry a diagnostic evidence block, got %q", text)
	}
	if !strings.Contains(text, "final_answer: true") {
		t.Fatalf("plain read feedback %q missing final_answer instruction", text)
	}
}

// diagnosticStepSummary must be data-bearing (severity + resource + observation)
// rather than a generic "N 条观察" count, so the replanning LLM can tell the
// diagnostic answered the question.
func TestDiagnosticStepSummaryIsDataBearing(t *testing.T) {
	t.Parallel()
	pkg := diagnostics.Package{
		Environment: "prod",
		Domains:     []string{"glusterfs"},
		Resources: []diagnostics.ResourceRef{
			{Domain: "glusterfs", Type: "volume", Name: "data", Environment: "prod"},
		},
		Observations: []diagnostics.Observation{
			{Kind: "glusterfs.volume.health", Severity: diagnostics.SeverityWarning, Summary: "容量使用 82%"},
		},
	}
	summary := diagnosticStepSummary(pkg)
	for _, want := range []string{"glusterfs", "volume", "data", "warning"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("diagnosticStepSummary %q missing %q", summary, want)
		}
	}
	if strings.Contains(summary, "诊断完成：N") {
		t.Fatalf("diagnosticStepSummary %q still uses the generic count form", summary)
	}
	// Worst severity across observations is surfaced.
	critical := pkg
	critical.Observations = []diagnostics.Observation{
		{Kind: "k", Severity: diagnostics.SeverityCritical, Summary: "高延迟"},
	}
	if sev := packageSeverity(critical); sev != diagnostics.SeverityCritical {
		t.Fatalf("packageSeverity = %q, want critical", sev)
	}
}

// TestDiagnosticStepSummaryMultiDomainAggregatesAllDomains verifies that a
// multi-domain package (an orchestrator-merged sweep) is summarized across ALL
// domains rather than only the first — the operator-facing conclusion must
// reflect the glusterfs→minio→kafka chain, not a single domain.
func TestDiagnosticStepSummaryMultiDomainAggregatesAllDomains(t *testing.T) {
	t.Parallel()
	pkg := diagnostics.Package{
		Environment: "prod",
		Domains:     []string{"glusterfs", "minio", "kafka"},
		Resources: []diagnostics.ResourceRef{
			{Domain: "glusterfs", Type: "volume", Name: "glusterfs-volume", Environment: "prod"},
			{Domain: "minio", Type: "bucket", Name: "minio-bucket", Environment: "prod"},
			{Domain: "kafka", Type: "consumer_group", Name: "kafka-consumer_group", Environment: "prod"},
		},
		Observations: []diagnostics.Observation{
			{Kind: "glusterfs.volume.health", Severity: diagnostics.SeverityInfo, Summary: "glusterfs 资源 glusterfs-volume 状态为 可用"},
			{Kind: "minio.bucket.health", Severity: diagnostics.SeverityWarning, Summary: "minio 资源 minio-bucket 状态为 可用"},
			{Kind: "kafka.consumer_lag", Severity: diagnostics.SeverityInfo, Summary: "kafka 资源 kafka-consumer_group 状态为 可用"},
		},
	}
	summary := diagnosticStepSummary(pkg)
	for _, want := range []string{"glusterfs", "minio", "kafka", "3 个域"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("diagnosticStepSummary %q missing %q — multi-domain chain not aggregated", summary, want)
		}
	}
}
