package assistant_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/orchestrator"
	"github.com/gracegaoya/ai-operations-copilot/internal/plans"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestAssistantViewerReadReturnsAnswer(t *testing.T) {
	t.Parallel()
	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{ToolName: tools.ClusterStatusRead, Input: map[string]any{"environment": "prod"}}})

	response, err := service.HandleMessage(context.Background(), viewer(), "查看 prod 集群状态", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "answer" || response.Tool != tools.ClusterStatusRead || response.Answer["status"] != "green" {
		t.Fatalf("response = %+v, want answer", response)
	}
}

func TestAssistantViewerWriteIsDeniedByPolicy(t *testing.T) {
	t.Parallel()
	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{ToolName: tools.TopicRetentionSet, Input: retentionInput()}})

	_, err := service.HandleMessage(context.Background(), viewer(), "retention", "", assistant.PageContext{})
	if !errors.Is(err, assistant.ErrPolicyDenied) || !strings.Contains(err.Error(), "permission_denied") {
		t.Fatalf("error = %v, want policy denial", err)
	}
}

func TestAssistantRejectsForgedPlannerTool(t *testing.T) {
	t.Parallel()
	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{ToolName: "shell.exec", Input: map[string]any{"command": "rm -rf /"}}})

	_, err := service.HandleMessage(context.Background(), admin(), "danger", "", assistant.PageContext{})
	if !errors.Is(err, assistant.ErrPolicyDenied) || !strings.Contains(err.Error(), "tool_not_registered") {
		t.Fatalf("error = %v, want tool_not_registered policy denial", err)
	}
}

func TestAssistantAdminWriteCreatesPendingPlanWithInternalTokenOnly(t *testing.T) {
	t.Parallel()
	service, repository := newAssistant(t, fakePlanner{intent: assistant.Intent{ToolName: tools.TopicRetentionSet, Input: retentionInput()}})

	response, err := service.HandleMessage(context.Background(), admin(), "retention", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "confirmation_required" || response.Tool != tools.TopicRetentionSet || response.PlanID == "" || response.Status != string(plans.PendingConfirmation) {
		t.Fatalf("response = %+v, want pending plan", response)
	}
	if response.ConfirmationToken == "" {
		t.Fatal("response did not retain internal confirmation token")
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if strings.Contains(string(encoded), "confirmation_token") || strings.Contains(string(encoded), response.ConfirmationToken) {
		t.Fatalf("json response %s exposed confirmation token", encoded)
	}
	plan, err := repository.GetPlan(context.Background(), response.PlanID)
	if err != nil {
		t.Fatalf("stored plan: %v", err)
	}
	if plan.Status != store.PlanPendingConfirmation {
		t.Fatalf("stored status = %q, want pending", plan.Status)
	}
}

// TestAssistantWritePlanAttachesDryRunRiskNoticeBlock verifies that when a
// dry-run runner is wired, creating a pending write plan auto-previews the
// operation and attaches a risk_notice block to the confirmation_required
// response.
func TestAssistantWritePlanAttachesDryRunRiskNoticeBlock(t *testing.T) {
	t.Parallel()
	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{
		ToolName: tools.TopicRetentionSet,
		Input:    retentionInput(),
	}})
	dryRun := &fakeDryRunRunner{result: execution.DryRunResult{
		Summary:           "将把 prod 环境的 topic orders 的消息保留时间设置为 72 小时。",
		AffectedResources: []string{"topic:orders@prod"},
		Commands:          []string{"kafka-configs --alter ..."},
		Warnings:          []string{"缩短保留时间可能导致历史消息被删除"},
	}}
	service = service.WithDryRunRunner(dryRun)

	response, err := service.HandleMessage(context.Background(), admin(), "retention", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "confirmation_required" {
		t.Fatalf("type = %q, want confirmation_required", response.Type)
	}
	if !dryRun.called {
		t.Fatal("dry-run was not invoked for write plan")
	}
	if dryRun.tool != tools.TopicRetentionSet {
		t.Fatalf("dry-run tool = %q, want %q", dryRun.tool, tools.TopicRetentionSet)
	}
	var riskBlock *assistant.Block
	for i := range response.Blocks {
		if response.Blocks[i].Type == assistant.BlockRiskNotice {
			riskBlock = &response.Blocks[i]
			break
		}
	}
	if riskBlock == nil {
		t.Fatalf("response.Blocks = %+v, want a risk_notice block", response.Blocks)
	}
	if riskBlock.Title == "" {
		t.Error("risk_notice block has empty title")
	}
	if riskBlock.Content == "" {
		t.Error("risk_notice block has empty content")
	}
	payload := riskBlock.Payload
	if payload == nil {
		t.Fatal("risk_notice block payload is nil")
	}
	if _, ok := payload["affected_resources"]; !ok {
		t.Error("risk_notice payload missing affected_resources")
	}
	if _, ok := payload["commands"]; !ok {
		t.Error("risk_notice payload missing commands")
	}
	if _, ok := payload["warnings"]; !ok {
		t.Error("risk_notice payload missing warnings")
	}
}

// TestAssistantWritePlanPersistsDryRun verifies that the dry-run preview is
// persisted onto the action plan (结果准 #5), so the confirmation flow can be
// reviewed with the full "how to run" plan even after the response is gone.
func TestAssistantWritePlanPersistsDryRun(t *testing.T) {
	t.Parallel()
	service, repository := newAssistant(t, fakePlanner{intent: assistant.Intent{
		ToolName: tools.TopicRetentionSet,
		Input:    retentionInput(),
	}})
	dryRun := &fakeDryRunRunner{result: execution.DryRunResult{
		Summary:           "将把 prod 环境的 topic orders 的消息保留时间设置为 72 小时。",
		AffectedResources: []string{"topic:orders@prod"},
		Warnings:          []string{"缩短保留时间可能导致历史消息被删除"},
	}}
	service = service.WithDryRunRunner(dryRun)

	response, err := service.HandleMessage(context.Background(), admin(), "retention", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "confirmation_required" {
		t.Fatalf("type = %q, want confirmation_required", response.Type)
	}

	// 验证 dry-run 结果已持久化到 plan 记录
	plans, err := repository.ListPlans(context.Background(), store.PlanFilter{})
	if err != nil {
		t.Fatalf("list plans: %v", err)
	}
	if len(plans.Plans) != 1 {
		t.Fatalf("plans = %d, want 1", len(plans.Plans))
	}
	if len(plans.Plans[0].DryRun) == 0 {
		t.Fatal("DryRun not persisted on plan record")
	}
	var persisted execution.DryRunResult
	if err := json.Unmarshal(plans.Plans[0].DryRun, &persisted); err != nil {
		t.Fatalf("decode persisted dry-run: %v", err)
	}
	if persisted.Summary == "" {
		t.Error("persisted dry-run summary is empty")
	}
}

// TestAssistantWritePlanRiskNoticeIncludesSuggestedStrategy verifies that
// when the dry-run result carries a SuggestedStrategy (借鉴-3: 任务草稿自动
// 补齐执行策略), the risk_notice block payload includes it so the operator
// sees the full "how to run" hint (timeout/retry/concurrency/target hosts/
// risk level) before confirming.
func TestAssistantWritePlanRiskNoticeIncludesSuggestedStrategy(t *testing.T) {
	t.Parallel()
	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{
		ToolName: tools.TopicRetentionSet,
		Input:    retentionInput(),
	}})
	strategy := &execution.SuggestedStrategy{
		Timeout:     60 * time.Second,
		Concurrency: 1,
		RiskLevel:   "medium",
	}
	dryRun := &fakeDryRunRunner{result: execution.DryRunResult{
		Summary:           "preview",
		SuggestedStrategy: strategy,
	}}
	service = service.WithDryRunRunner(dryRun)

	response, err := service.HandleMessage(context.Background(), admin(), "retention", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	var riskBlock *assistant.Block
	for i := range response.Blocks {
		if response.Blocks[i].Type == assistant.BlockRiskNotice {
			riskBlock = &response.Blocks[i]
			break
		}
	}
	if riskBlock == nil {
		t.Fatalf("response.Blocks = %+v, want a risk_notice block", response.Blocks)
	}
	payload := riskBlock.Payload
	if payload == nil {
		t.Fatal("risk_notice block payload is nil")
	}
	strategyPayload, ok := payload["suggested_strategy"]
	if !ok {
		t.Fatal("risk_notice payload missing suggested_strategy")
	}
	got, ok := strategyPayload.(*execution.SuggestedStrategy)
	if !ok {
		t.Fatalf("suggested_strategy = %T, want *execution.SuggestedStrategy", strategyPayload)
	}
	if got.RiskLevel != "medium" {
		t.Errorf("risk_level = %v, want medium", got.RiskLevel)
	}
	if got.Timeout != 60*time.Second {
		t.Errorf("timeout = %v, want 60s", got.Timeout)
	}
}

// TestAssistantWritePlanWithoutDryRunRunnerHasNoBlock verifies backward
// compatibility: when no dry-run runner is wired, the confirmation_required
// response carries no blocks.
func TestAssistantWritePlanWithoutDryRunRunnerHasNoBlock(t *testing.T) {
	t.Parallel()
	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{
		ToolName: tools.TopicRetentionSet,
		Input:    retentionInput(),
	}})

	response, err := service.HandleMessage(context.Background(), admin(), "retention", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "confirmation_required" {
		t.Fatalf("type = %q, want confirmation_required", response.Type)
	}
	if len(response.Blocks) != 0 {
		t.Fatalf("response.Blocks = %+v, want empty when no dry-run runner", response.Blocks)
	}
}

// TestAssistantWritePlanDryRunFailureStillReturnsPlan verifies that a dry-run
// failure (e.g. unsupported tool) is silently ignored: the pending plan is
// still returned for confirmation, just without a risk_notice block.
func TestAssistantWritePlanDryRunFailureStillReturnsPlan(t *testing.T) {
	t.Parallel()
	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{
		ToolName: tools.TopicRetentionSet,
		Input:    retentionInput(),
	}})
	dryRun := &fakeDryRunRunner{err: execution.ErrDryRunNotSupported}
	service = service.WithDryRunRunner(dryRun)

	response, err := service.HandleMessage(context.Background(), admin(), "retention", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "confirmation_required" {
		t.Fatalf("type = %q, want confirmation_required", response.Type)
	}
	if response.PlanID == "" {
		t.Fatal("plan_id is empty, dry-run failure should not block plan creation")
	}
	if len(response.Blocks) != 0 {
		t.Fatalf("response.Blocks = %+v, want empty when dry-run fails", response.Blocks)
	}
}

func TestAssistantClarifiesUnclearMessage(t *testing.T) {
	t.Parallel()
	service, _ := newAssistant(t, fakePlanner{err: assistant.ErrClarificationNeeded})

	response, err := service.HandleMessage(context.Background(), viewer(), "帮我看看", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "clarification_needed" || response.Message == "" {
		t.Fatalf("response = %+v, want clarification", response)
	}
}

func TestAssistantReturnsTypedClarificationMessage(t *testing.T) {
	t.Parallel()
	service, _ := newAssistant(t, fakePlanner{err: assistant.NewClarification("缺少参数: cluster, bucket")})

	response, err := service.HandleMessage(context.Background(), viewer(), "查 minio 容量", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "clarification_needed" || response.Message != "缺少参数: cluster, bucket" {
		t.Fatalf("response = %+v, want typed clarification message", response)
	}
}

func TestAssistantDynamicCapabilityReadReturnsAnswer(t *testing.T) {
	registerAssistantDynamicCapacityTool(t)
	service, _ := newAssistant(t, assistant.NewCapabilityAwarePlanner(fakePlanner{err: assistant.ErrClarificationNeeded}))

	response, err := service.HandleMessage(context.Background(), viewer(), "查一下 prod m1 archive bucket 的 minio 容量", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "answer" || response.Tool != "minio.bucket.capacity.read" {
		t.Fatalf("response = %+v, want dynamic capability answer", response)
	}
	if response.Answer["tool"] != "minio.bucket.capacity.read" || response.Answer["environment"] != "prod" {
		t.Fatalf("answer = %+v, want read runner result", response.Answer)
	}
}

func TestAssistantDynamicCapabilityReadDeniesDisallowedEnvironment(t *testing.T) {
	registerAssistantDynamicCapacityTool(t)
	service, _ := newAssistant(t, assistant.NewCapabilityAwarePlanner(fakePlanner{err: assistant.ErrClarificationNeeded}))
	unauthorized := viewer()
	unauthorized.AllowedEnvironments = []string{"staging"}

	_, err := service.HandleMessage(context.Background(), unauthorized, "查一下 prod m1 archive bucket 的 minio 容量", "", assistant.PageContext{})
	if !errors.Is(err, assistant.ErrPolicyDenied) || !strings.Contains(err.Error(), "environment_denied") {
		t.Fatalf("error = %v, want environment policy denial", err)
	}
}

func registerAssistantDynamicCapacityTool(t *testing.T) {
	t.Helper()
	tools.ResetDynamicToolsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	policy.ResetDynamicRolePermissionsForTest()
	t.Cleanup(policy.ResetDynamicRolePermissionsForTest)
	err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{{
		Tool: tools.Tool{Name: "minio.bucket.capacity.read", Operation: tools.Read, Risk: tools.Low, Domain: "minio", ResourceType: "bucket"},
		InputSchema: map[string]tools.DynamicInputField{
			"environment": {Type: "string", Required: true},
			"cluster":     {Type: "string", Required: true},
			"bucket":      {Type: "string", Required: true},
		},
	}})
	if err != nil {
		t.Fatalf("register dynamic tool: %v", err)
	}
	policy.RegisterDynamicRolePermissions(map[string][]string{"minio.bucket.capacity.read": {"viewer"}})
}

func TestAssistantDiagnosticIntentReturnsPackage(t *testing.T) {
	t.Parallel()
	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{
		Diagnostic: &diagnostics.Request{Domain: "glusterfs", Environment: "prod", ResourceType: "volume", ResourceName: "data", Runbook: "health"},
	}})

	response, err := service.HandleMessage(context.Background(), viewer(), "检查 prod glusterfs data volume 健康", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "answer" || response.Tool != tools.GlusterVolumeHealthRead || response.Diagnostic == nil || response.Answer["message"] != "Diagnostic package is ready." {
		t.Fatalf("response = %+v, want compatible diagnostic answer", response)
	}
	if response.Diagnostic.Environment != "prod" || response.Diagnostic.Domains[0] != "glusterfs" {
		t.Fatalf("diagnostic = %+v, want glusterfs prod package", response.Diagnostic)
	}
}

func TestAssistantResponseIncludesTraceWithSelectionAndToolInvocation(t *testing.T) {
	// NOTE: intentionally NOT t.Parallel(). This test mutates the process-global
	// dynamic-tool registry (registerAssistantDynamicCapacityTool resets it), and
	// running it in parallel with the other two dynamic-capability tests races
	// on Reset/Register — the registry can be wiped mid-assertion, surfacing as
	// "tool_not_registered" / trace=nil. Keep it serial with its siblings.
	registerAssistantDynamicCapacityTool(t)
	service, _ := newAssistant(t, assistant.NewCapabilityAwarePlanner(fakePlanner{err: assistant.ErrClarificationNeeded}))

	response, err := service.HandleMessage(context.Background(), viewer(), "查一下 prod m1 archive bucket 的 minio 容量", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Trace == nil {
		t.Fatalf("trace = nil, want trace")
	}
	if response.Trace.Selection == nil || response.Trace.Selection.Selected != "minio.bucket.capacity.read" {
		t.Fatalf("selection = %+v, want minio.bucket.capacity.read", response.Trace.Selection)
	}
	if response.Trace.ToolInvocation == nil {
		t.Fatalf("tool_invocation = nil, want invocation")
	}
	invocation := response.Trace.ToolInvocation
	if invocation.Tool != "minio.bucket.capacity.read" {
		t.Fatalf("tool = %q, want minio.bucket.capacity.read", invocation.Tool)
	}
	if invocation.Input["cluster"] != "m1" || invocation.Input["bucket"] != "archive" {
		t.Fatalf("input = %+v, want extracted parameters", invocation.Input)
	}
	if invocation.RawResponse["status"] != "green" {
		t.Fatalf("raw_response = %+v, want green status", invocation.RawResponse)
	}
}

func TestAssistantResponseIncludesTraceForWritePlanWithoutRawResponse(t *testing.T) {
	t.Parallel()
	intent := assistant.Intent{
		ToolName:   tools.TopicRetentionSet,
		Input:      retentionInput(),
		Confidence: 0.8,
		Selection: &assistant.CapabilitySelection{
			Selected:   tools.TopicRetentionSet,
			Confidence: 0.8,
			Reason:     "topic retention intent",
			Extracted: []assistant.ExtractedParameter{
				{Name: "environment", Value: "prod", Source: "environment"},
				{Name: "topic", Value: "orders", Source: "pattern"},
				{Name: "retention_hours", Value: 72, Source: "pattern"},
			},
		},
	}
	service, _ := newAssistant(t, fakePlanner{intent: intent})

	response, err := service.HandleMessage(context.Background(), admin(), "retention", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "confirmation_required" {
		t.Fatalf("type = %q, want confirmation_required", response.Type)
	}
	if response.Trace == nil || response.Trace.ToolInvocation == nil {
		t.Fatalf("trace = %+v, want tool invocation", response.Trace)
	}
	invocation := response.Trace.ToolInvocation
	if invocation.Tool != tools.TopicRetentionSet {
		t.Fatalf("tool = %q, want %q", invocation.Tool, tools.TopicRetentionSet)
	}
	if invocation.Input["topic"] != "orders" || invocation.Input["retention_hours"] != 72 {
		t.Fatalf("input = %+v, want topic=orders and retention_hours=72", invocation.Input)
	}
	if invocation.RawResponse != nil {
		t.Fatalf("raw_response = %+v, want nil for write plan", invocation.RawResponse)
	}
	if response.Trace.Selection == nil || response.Trace.Selection.Selected != tools.TopicRetentionSet {
		t.Fatalf("trace.selection = %+v, want TopicRetentionSet", response.Trace.Selection)
	}
}

func TestAssistantResponseIncludesTraceSelectionForClarification(t *testing.T) {
	t.Parallel()
	selection := &assistant.CapabilitySelection{
		Selected: "minio.bucket.capacity.read",
		Missing:  []string{"cluster", "bucket"},
		Reason:   "missing required parameters",
	}
	service, _ := newAssistant(t, fakePlanner{err: assistant.NewClarificationWithSelection("缺少参数: cluster, bucket", selection)})

	response, err := service.HandleMessage(context.Background(), viewer(), "查一下 prod minio bucket 容量", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "clarification_needed" {
		t.Fatalf("type = %q, want clarification_needed", response.Type)
	}
	if response.Trace == nil || response.Trace.Selection == nil {
		t.Fatalf("trace = %+v, want selection", response.Trace)
	}
	if response.Trace.Selection.Selected != "minio.bucket.capacity.read" {
		t.Fatalf("selected = %q, want minio.bucket.capacity.read", response.Trace.Selection.Selected)
	}
	if response.Trace.ToolInvocation != nil {
		t.Fatalf("tool_invocation = %+v, want nil for clarification", response.Trace.ToolInvocation)
	}
	if len(response.Trace.Selection.Missing) == 0 {
		t.Fatalf("missing = %+v, want missing fields", response.Trace.Selection.Missing)
	}
}

func TestAssistantDiagnosticPolicyDenialIsClassified(t *testing.T) {
	t.Parallel()
	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{
		Diagnostic: &diagnostics.Request{Domain: "glusterfs", Environment: "prod", ResourceName: "data"},
	}})
	unauthorized := viewer()
	unauthorized.AllowedEnvironments = []string{"staging"}

	_, err := service.HandleMessage(context.Background(), unauthorized, "检查 prod glusterfs data volume 健康", "", assistant.PageContext{})
	if !errors.Is(err, assistant.ErrPolicyDenied) {
		t.Fatalf("error = %v, want diagnostic policy denial", err)
	}
}

func TestAssistantDiagnosticUnsupportedDomainIsTyped(t *testing.T) {
	t.Parallel()
	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{
		Diagnostic: &diagnostics.Request{Domain: "elasticsearch", Environment: "prod", ResourceName: "logs"},
	}})

	_, err := service.HandleMessage(context.Background(), viewer(), "check prod elasticsearch logs health", "", assistant.PageContext{})
	if !errors.Is(err, diagnostics.ErrUnsupportedDomain) {
		t.Fatalf("error = %v, want unsupported diagnostic domain", err)
	}
}

func TestDeterministicPlannerDetectsMiddlewareDiagnostics(t *testing.T) {
	t.Parallel()
	intent, err := assistant.DeterministicPlanner{}.Plan(context.Background(), viewer(), "检查 prod glusterfs data volume 健康", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("Plan returned %v", err)
	}
	if intent.Diagnostic == nil || intent.Diagnostic.Domain != "glusterfs" || intent.Diagnostic.Environment != "prod" || intent.Diagnostic.ResourceName != "data" {
		t.Fatalf("intent = %+v, want glusterfs diagnostic", intent)
	}
}

func TestServiceHandleMessageCreatesConversationWhenIDMissing(t *testing.T) {
	t.Parallel()
	conversations := store.NewMemoryAssistantConversationStore()
	planner := &historyCapturingPlanner{intent: assistant.Intent{
		ToolName: tools.ClusterStatusRead,
		Input:    map[string]any{"environment": "prod"},
	}}
	service, _ := newAssistantWithStore(t, planner, conversations)

	response, err := service.HandleMessage(context.Background(), viewer(), "查看 prod 集群状态", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.ConversationID == "" {
		t.Fatal("conversation_id = empty, want a new conversation id")
	}
	if response.TurnID == "" {
		t.Fatal("turn_id = empty, want the assistant turn id")
	}
	conv, err := conversations.GetConversation(context.Background(), response.ConversationID, viewer().Subject)
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}
	if conv.Title != "查看 prod 集群状态" {
		t.Fatalf("title = %q, want the first user message", conv.Title)
	}
	if conv.LastMessagePreview != "查看 prod 集群状态" {
		t.Fatalf("preview = %q, want the first user message", conv.LastMessagePreview)
	}
	if conv.Subject != viewer().Subject {
		t.Fatalf("subject = %q, want %q", conv.Subject, viewer().Subject)
	}
	if planner.calls != 1 {
		t.Fatalf("planner calls = %d, want 1", planner.calls)
	}
	if len(planner.history) != 0 {
		t.Fatalf("history len = %d, want 0 on first turn", len(planner.history))
	}
}

func TestServiceHandleMessagePersistsTurns(t *testing.T) {
	t.Parallel()
	conversations := store.NewMemoryAssistantConversationStore()
	planner := &historyCapturingPlanner{intent: assistant.Intent{
		ToolName: tools.ClusterStatusRead,
		Input:    map[string]any{"environment": "prod"},
	}}
	service, _ := newAssistantWithStore(t, planner, conversations)

	response, err := service.HandleMessage(context.Background(), viewer(), "查看 prod 集群状态", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	page, err := conversations.ListTurns(context.Background(), response.ConversationID, 0, "")
	if err != nil {
		t.Fatalf("ListTurns: %v", err)
	}
	if len(page.Turns) != 2 {
		t.Fatalf("turns = %d, want 2 (user + assistant)", len(page.Turns))
	}
	// ListTurns returns newest first: assistant turn then user turn.
	assistantTurn := page.Turns[0]
	userTurn := page.Turns[1]
	if userTurn.Role != store.ConversationRoleUser || userTurn.Content != "查看 prod 集群状态" {
		t.Fatalf("user turn = %+v, want the original message", userTurn)
	}
	if assistantTurn.Role != store.ConversationRoleAssistant {
		t.Fatalf("assistant turn role = %q, want %q", assistantTurn.Role, store.ConversationRoleAssistant)
	}
	if assistantTurn.ResponseType != "answer" {
		t.Fatalf("assistant turn response_type = %q, want answer", assistantTurn.ResponseType)
	}
	if assistantTurn.Content != "green" {
		t.Fatalf("assistant turn content = %q, want green", assistantTurn.Content)
	}
	if assistantTurn.ResponsePayload == nil || assistantTurn.ResponsePayload["type"] != "answer" {
		t.Fatalf("assistant turn payload = %+v, want serialized response", assistantTurn.ResponsePayload)
	}
	if assistantTurn.ID != response.TurnID {
		t.Fatalf("turn_id = %q, want %q", response.TurnID, assistantTurn.ID)
	}
}

func TestServiceHandleMessageLoadsHistoryForPlanner(t *testing.T) {
	t.Parallel()
	conversations := store.NewMemoryAssistantConversationStore()
	planner := &historyCapturingPlanner{intent: assistant.Intent{
		ToolName: tools.ClusterStatusRead,
		Input:    map[string]any{"environment": "prod"},
	}}
	service, _ := newAssistantWithStore(t, planner, conversations)

	firstUser := viewer()
	firstResponse, err := service.HandleMessage(context.Background(), firstUser, "查看 prod 集群状态", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("first handle message: %v", err)
	}

	// Second turn on the same conversation must deliver history to the planner.
	_, err = service.HandleMessage(context.Background(), firstUser, "再来一次", firstResponse.ConversationID, assistant.PageContext{})
	if err != nil {
		t.Fatalf("second handle message: %v", err)
	}
	if planner.calls != 2 {
		t.Fatalf("planner calls = %d, want 2", planner.calls)
	}
	// After the second turn, the store has 2 user + 2 assistant = 4 turns.
	// The history delivered to the planner on the second call should be the
	// most recent turns persisted before that call (1 user + 1 assistant = 2).
	if len(planner.history) != 2 {
		t.Fatalf("history len = %d, want 2 (1 user + 1 assistant)", len(planner.history))
	}
	// History is chronological: user first, assistant second.
	if planner.history[0].Role != store.ConversationRoleUser || planner.history[0].Content != "查看 prod 集群状态" {
		t.Fatalf("history[0] = %+v, want the first user turn", planner.history[0])
	}
	if planner.history[1].Role != store.ConversationRoleAssistant || planner.history[1].ResponseType != "answer" {
		t.Fatalf("history[1] = %+v, want the first assistant turn", planner.history[1])
	}
}

func TestServiceHandleMessageRejectsForeignConversation(t *testing.T) {
	t.Parallel()
	conversations := store.NewMemoryAssistantConversationStore()
	planner := &historyCapturingPlanner{intent: assistant.Intent{
		ToolName: tools.ClusterStatusRead,
		Input:    map[string]any{"environment": "prod"},
	}}
	service, _ := newAssistantWithStore(t, planner, conversations)

	owner := viewer()
	owner.Subject = "owner-1"
	intruder := viewer()
	intruder.Subject = "intruder-1"

	ownerResponse, err := service.HandleMessage(context.Background(), owner, "查看 prod 集群状态", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("owner handle message: %v", err)
	}

	_, err = service.HandleMessage(context.Background(), intruder, "继续查询", ownerResponse.ConversationID, assistant.PageContext{})
	if !errors.Is(err, assistant.ErrForeignConversation) {
		t.Fatalf("intruder error = %v, want ErrForeignConversation", err)
	}
	if planner.calls != 1 {
		t.Fatalf("planner calls = %d, want 1 (intruder must not reach the planner)", planner.calls)
	}
}

func TestServiceHandleMessageStreamDeterministicFallbackEmitsTerminalResponse(t *testing.T) {
	t.Parallel()
	// DeterministicPlanner does not implement PlanStream, so HandleMessageStream
	// must degrade to one-shot Plan and emit a single terminal event carrying
	// the final executed Response (no deltas).
	service, _ := newAssistant(t, assistant.DeterministicPlanner{})

	events, err := service.HandleMessageStream(context.Background(), viewer(), "查看 prod 集群状态", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("HandleMessageStream start: %v", err)
	}
	var (
		deltas  []string
		resp    *assistant.Response
		done    bool
		lastErr error
	)
	for ev := range events {
		if ev.Delta != "" {
			deltas = append(deltas, ev.Delta)
		}
		if ev.Done {
			done = true
			resp = ev.Response
			lastErr = ev.Err
		}
	}
	if !done {
		t.Fatal("no terminal event received")
	}
	if lastErr != nil {
		t.Fatalf("terminal err = %v, want nil", lastErr)
	}
	if resp == nil || resp.Type != "answer" || resp.Tool != tools.ClusterStatusRead || resp.Answer["status"] != "green" {
		t.Fatalf("response = %+v, want cluster status answer", resp)
	}
	if len(deltas) != 0 {
		t.Fatalf("deltas = %v, want none in fallback mode", deltas)
	}
}

func TestServiceHandleMessageStreamFallbackPersistsTurnsAndConversation(t *testing.T) {
	t.Parallel()
	conversations := store.NewMemoryAssistantConversationStore()
	service, _ := newAssistantWithStore(t, assistant.DeterministicPlanner{}, conversations)

	events, err := service.HandleMessageStream(context.Background(), viewer(), "查看 prod 集群状态", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("HandleMessageStream start: %v", err)
	}
	var resp *assistant.Response
	for ev := range events {
		if ev.Done {
			resp = ev.Response
		}
	}
	if resp == nil {
		t.Fatal("no terminal response event")
	}
	if resp.ConversationID == "" {
		t.Fatal("conversation_id = empty, want a new conversation id")
	}
	if resp.TurnID == "" {
		t.Fatal("turn_id = empty, want the assistant turn id")
	}
	page, err := conversations.ListTurns(context.Background(), resp.ConversationID, 0, "")
	if err != nil {
		t.Fatalf("ListTurns: %v", err)
	}
	if len(page.Turns) != 2 {
		t.Fatalf("turns = %d, want 2 (user + assistant)", len(page.Turns))
	}
}

func TestServiceHandleMessageStreamForwardsDeltasAndFinalResponse(t *testing.T) {
	t.Parallel()
	// EinoPlanner supports PlanStream: deltas from the LLM stream are
	// forwarded, then the execution pipeline runs and the final answer lands
	// in the terminal Response event.
	chat := fakeEinoChatModel{
		streamChunks: []*schema.Message{
			schema.AssistantMessage(`{"tool_name":"cluster.status.read","input":`, nil),
			schema.AssistantMessage(`{"environment":"prod"},"confidence":0.91,`, nil),
			schema.AssistantMessage(`"explanation":"read cluster status"}`, nil),
		},
	}
	planner := assistant.NewEinoPlanner(&chat)
	service, _ := newAssistant(t, planner)

	events, err := service.HandleMessageStream(context.Background(), viewer(), "查看 prod 集群状态", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("HandleMessageStream start: %v", err)
	}
	var (
		deltas  []string
		resp    *assistant.Response
		done    bool
		lastErr error
	)
	for ev := range events {
		if ev.Delta != "" {
			deltas = append(deltas, ev.Delta)
		}
		if ev.Done {
			done = true
			resp = ev.Response
			lastErr = ev.Err
		}
	}
	if !done {
		t.Fatal("no terminal event received")
	}
	if lastErr != nil {
		t.Fatalf("terminal err = %v, want nil", lastErr)
	}
	if resp == nil || resp.Type != "answer" || resp.Tool != tools.ClusterStatusRead {
		t.Fatalf("response = %+v, want cluster status answer", resp)
	}
	if len(deltas) != 3 {
		t.Fatalf("deltas = %v, want 3 chunks from the planner stream", deltas)
	}
}

// TestServiceHandleMessageStreamEmitsProgressEvents asserts that the streaming
// path emits progress events for each pipeline stage (planning →
// tool_executing → formatting) before the terminal Done event. The frontend
// uses these events to render a "进度事件折叠" panel so the operator can see
// which stage the agent is in while waiting for the final answer.
func TestServiceHandleMessageStreamEmitsProgressEvents(t *testing.T) {
	t.Parallel()
	// EinoPlanner supports PlanStream; read tool path exercises the full
	// planning → tool_executing → formatting chain.
	chat := fakeEinoChatModel{
		streamChunks: []*schema.Message{
			schema.AssistantMessage(`{"tool_name":"cluster.status.read","input":`, nil),
			schema.AssistantMessage(`{"environment":"prod"},"confidence":0.91,`, nil),
			schema.AssistantMessage(`"explanation":"read cluster status"}`, nil),
		},
	}
	planner := assistant.NewEinoPlanner(&chat)
	service, _ := newAssistant(t, planner)

	events, err := service.HandleMessageStream(context.Background(), viewer(), "查看 prod 集群状态", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("HandleMessageStream start: %v", err)
	}
	var (
		stages  []string
		resp    *assistant.Response
		done    bool
		lastErr error
	)
	for ev := range events {
		if ev.Progress != nil {
			stages = append(stages, ev.Progress.Stage)
		}
		if ev.Done {
			done = true
			resp = ev.Response
			lastErr = ev.Err
		}
	}
	if !done {
		t.Fatal("no terminal event received")
	}
	if lastErr != nil {
		t.Fatalf("terminal err = %v, want nil", lastErr)
	}
	if resp == nil || resp.Type != "answer" {
		t.Fatalf("response = %+v, want answer", resp)
	}
	// Expect at minimum: planning (before planner stream), tool_executing
	// (before read tool runs), formatting (before formatter runs, if wired).
	// The formatter is not wired in newAssistant, so formatting may be absent;
	// assert the two guaranteed stages are present and in order.
	wantPrefix := []string{assistant.ProgressPlanning, assistant.ProgressToolExecuting}
	if !hasOrderedPrefix(stages, wantPrefix) {
		t.Fatalf("stages = %v, want prefix %v", stages, wantPrefix)
	}
}

// hasOrderedPrefix returns true if want appears as an ordered subsequence at
// the start of got (allowing additional stages interspersed only after the
// prefix is fully matched). Duplicates of the same stage are tolerated.
func hasOrderedPrefix(got, want []string) bool {
	i := 0
	for _, s := range got {
		if i < len(want) && s == want[i] {
			i++
		}
	}
	return i == len(want)
}

// TestServiceHandleMessageStreamFallbackEmitsProgressEvents asserts the
// fallback planner path (DeterministicPlanner without PlanStream) still emits
// planning + tool_executing progress events before the terminal response.
func TestServiceHandleMessageStreamFallbackEmitsProgressEvents(t *testing.T) {
	t.Parallel()
	service, _ := newAssistant(t, assistant.DeterministicPlanner{})

	events, err := service.HandleMessageStream(context.Background(), viewer(), "查看 prod 集群状态", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("HandleMessageStream start: %v", err)
	}
	var (
		stages []string
		done   bool
	)
	for ev := range events {
		if ev.Progress != nil {
			stages = append(stages, ev.Progress.Stage)
		}
		if ev.Done {
			done = true
		}
	}
	if !done {
		t.Fatal("no terminal event received")
	}
	wantPrefix := []string{assistant.ProgressPlanning, assistant.ProgressToolExecuting}
	if !hasOrderedPrefix(stages, wantPrefix) {
		t.Fatalf("stages = %v, want prefix %v (fallback path)", stages, wantPrefix)
	}
}

// TestServiceHandleMessageStreamEmitsFormattingProgress asserts the
// formatting progress event fires when a formatter is wired. Confirms the
// formatter stage is reported separately from tool_executing.
func TestServiceHandleMessageStreamEmitsFormattingProgress(t *testing.T) {
	t.Parallel()
	chat := fakeEinoChatModel{
		streamChunks: []*schema.Message{
			schema.AssistantMessage(`{"tool_name":"cluster.status.read","input":{"environment":"prod"},"confidence":0.91,"explanation":"x"}`, nil),
		},
	}
	planner := assistant.NewEinoPlanner(&chat)
	service, _ := newAssistant(t, planner)
	// Wire a stub formatter so formatResponse runs and emits the formatting
	// progress event. Reuses the stubFormatter defined in formatter_test.go.
	service = service.WithFormatter(&stubFormatter{result: assistant.FormatResult{Summary: "stub summary"}})

	events, err := service.HandleMessageStream(context.Background(), viewer(), "查看 prod 集群状态", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("HandleMessageStream start: %v", err)
	}
	var stages []string
	for ev := range events {
		if ev.Progress != nil {
			stages = append(stages, ev.Progress.Stage)
		}
	}
	wantPrefix := []string{assistant.ProgressPlanning, assistant.ProgressToolExecuting, assistant.ProgressFormatting}
	if !hasOrderedPrefix(stages, wantPrefix) {
		t.Fatalf("stages = %v, want prefix %v", stages, wantPrefix)
	}
}

func newAssistant(t *testing.T, planner assistant.Planner) (*assistant.Service, *store.MemoryActionPlanStore) {
	t.Helper()
	return newAssistantWithStore(t, planner, nil)
}

// registerMiddlewareToolsForService loads the middleware capabilities into the
// dynamic tool registry and injects their role permissions, mirroring what main
// does at startup via the published YAML capabilities. The middleware tools are
// no longer part of the static allowlist, so service-level tests that route
// write/read intents through policy must register them the same way production
// does. Reads are visible to all roles; the retention write maps to
// operator/admin (its environment risk is handled by the risk check).
//
// It is idempotent and mutex-guarded so parallel tests can all call it through
// newAssistantWithout tripping RegisterDynamicTools' duplicate rejection. It
// deliberately does NOT reset the registry: resets belong to the serial
// dynamic-capability tests (registerAssistantDynamicCapacityTool), which run in
// the sequential phase before the t.Parallel() tests.
func registerMiddlewareToolsForService(t *testing.T) {
	t.Helper()
	ensureMiddlewareForTests(t)
}

var ensureMiddlewareMu sync.Mutex

func ensureMiddlewareForTests(t *testing.T) {
	t.Helper()
	ensureMiddlewareMu.Lock()
	defer ensureMiddlewareMu.Unlock()
	if _, ok := tools.Lookup(tools.TopicRetentionSet); !ok {
		if err := tools.RegisterDynamicTools(toolsMiddlewareDefinitions()); err != nil {
			t.Fatalf("register middleware tools: %v", err)
		}
	}
	// Role permissions are additive/idempotent; re-inject on every call so a
	// policy-level reset elsewhere cannot leave the middleware tools un-routable.
	policy.RegisterDynamicRolePermissions(map[string][]string{
		tools.GlusterVolumeHealthRead: {"viewer", "operator", "admin"},
		tools.MinIOBucketHealthRead:   {"viewer", "operator", "admin"},
		tools.KafkaConsumerLagRead:    {"viewer", "operator", "admin"},
		tools.TopicRetentionSet:       {"operator", "admin"},
	})
}

func toolsMiddlewareDefinitions() []tools.DynamicToolDefinition {
	return []tools.DynamicToolDefinition{
		{
			Tool: tools.Tool{Name: tools.GlusterVolumeHealthRead, Operation: tools.Read, Risk: tools.Low, Domain: "glusterfs", ResourceType: "volume"},
			InputSchema: map[string]tools.DynamicInputField{
				"environment": {Type: "string", Required: true},
				"name":        {Type: "string", Required: true},
			},
		},
		{
			Tool: tools.Tool{Name: tools.MinIOBucketHealthRead, Operation: tools.Read, Risk: tools.Low, Domain: "minio", ResourceType: "bucket"},
			InputSchema: map[string]tools.DynamicInputField{
				"environment": {Type: "string", Required: true},
				"name":        {Type: "string", Required: true},
			},
		},
		{
			Tool: tools.Tool{Name: tools.KafkaConsumerLagRead, Operation: tools.Read, Risk: tools.Low, Domain: "kafka", ResourceType: "consumer_group"},
			InputSchema: map[string]tools.DynamicInputField{
				"environment": {Type: "string", Required: true},
				"name":        {Type: "string", Required: true},
			},
		},
		{
			Tool: tools.Tool{
				Name:                tools.TopicRetentionSet,
				Operation:           tools.Write,
				Risk:                tools.Medium,
				RollbackDescription: "reset_to_previous",
				Domain:              "kafka",
				ResourceType:        "topic",
				SupportsDryRun:      true,
			},
			InputSchema: map[string]tools.DynamicInputField{
				"environment":     {Type: "string", Required: true},
				"topic":           {Type: "string", Required: true},
				"retention_hours": {Type: "integer", Required: true, Min: minBound(1), Max: maxBound(8760)},
			},
		},
	}
}

func minBound(value float64) *float64 { return &value }
func maxBound(value float64) *float64 { return &value }

// newAssistantWithStore constructs an assistant service with an optional
// conversation store. When conversations is non-nil the service persists
// turns and enforces subject isolation; otherwise it falls back to the
// stateless behavior.
func newAssistantWithStore(t *testing.T, planner assistant.Planner, conversations store.AssistantConversationStore) (*assistant.Service, *store.MemoryActionPlanStore) {
	t.Helper()
	registerMiddlewareToolsForService(t)
	repository := store.NewMemoryActionPlanStore()
	readService := execution.NewReadOnlyService(readRunner{}, audit.NewService(repository))
	planService := plans.NewService(repository, plans.ClockFunc(func() time.Time {
		return time.Date(2026, time.July, 21, 10, 0, 0, 0, time.UTC)
	}))
	return assistant.NewService(planner, readService, planService, conversations), repository
}

type fakePlanner struct {
	intent assistant.Intent
	err    error
}

func (p fakePlanner) Plan(context.Context, identity.CurrentUser, string, []assistant.Turn, assistant.PageContext) (assistant.Intent, error) {
	return p.intent, p.err
}

// fakeDryRunRunner is a test double for assistant.DryRunRunner. It records the
// last call's tool and input so tests can assert the assistant invoked dry-run
// with the right arguments.
type fakeDryRunRunner struct {
	result execution.DryRunResult
	err    error
	called bool
	tool   string
	input  map[string]any
}

func (f *fakeDryRunRunner) DryRun(_ context.Context, toolName string, input map[string]any) (execution.DryRunResult, error) {
	f.called = true
	f.tool = toolName
	f.input = input
	return f.result, f.err
}

// historyCapturingPlanner records the history slice it received so tests can
// assert that the service loaded prior turns before invoking the planner.
type historyCapturingPlanner struct {
	intent  assistant.Intent
	history []assistant.Turn
	calls   int
}

func (p *historyCapturingPlanner) Plan(_ context.Context, _ identity.CurrentUser, _ string, history []assistant.Turn, _ assistant.PageContext) (assistant.Intent, error) {
	p.calls++
	p.history = append([]assistant.Turn(nil), history...)
	return p.intent, nil
}

type readRunner struct{}

func (readRunner) Read(_ context.Context, tool tools.Tool, input map[string]any) (map[string]any, error) {
	return map[string]any{"tool": tool.Name, "environment": input["environment"], "status": "green"}, nil
}

// stubDiagnostics implements assistant.DiagnosticRunner, returning a canned
// diagnostic package. It lets tests drive the recommendation→plan link without
// depending on the real diagnostics service surfacing actionable recommendations.
type stubDiagnostics struct {
	pkg diagnostics.Package
}

func (s stubDiagnostics) Run(_ context.Context, _ identity.CurrentUser, _ diagnostics.Request) (diagnostics.Package, error) {
	return s.pkg, nil
}

func viewer() identity.CurrentUser {
	return identity.CurrentUser{
		Subject:             "viewer-1",
		Roles:               []string{"viewer"},
		AllowedEnvironments: []string{"prod"},
		RequestID:           "request-viewer",
	}
}

func admin() identity.CurrentUser {
	return identity.CurrentUser{
		Subject:             "admin-1",
		Roles:               []string{"admin"},
		AllowedEnvironments: []string{"prod"},
		RequestID:           "request-admin",
	}
}

func retentionInput() map[string]any {
	return map[string]any{"environment": "prod", "topic": "orders", "retention_hours": 72}
}

func kafkaDiagnosticIntent() assistant.Intent {
	return assistant.Intent{Diagnostic: &diagnostics.Request{Domain: "kafka", Environment: "prod", ResourceType: "consumer_group", ResourceName: "orders", Runbook: "health"}}
}

func TestAssistantDiagnosticActionableRecommendationCreatesPlan(t *testing.T) {
	t.Parallel()
	pkg := diagnostics.Package{
		ID:          "diag-rec-1",
		Environment: "prod",
		Domains:     []string{"kafka"},
		Resources:   []diagnostics.ResourceRef{{Domain: "kafka", Type: "consumer_group", ID: "kafka:consumer_group:orders", Name: "orders", Environment: "prod"}},
		Recommendations: []diagnostics.Recommendation{
			{ID: "rec-1", Summary: "降低 Kafka 消费组 orders 的 retention", Rationale: "延迟过高", Risk: tools.Medium, Actionable: true, ToolName: tools.TopicRetentionSet, CandidateInput: retentionInput()},
		},
		CreatedAt: time.Date(2026, time.July, 21, 10, 0, 0, 0, time.UTC),
	}
	service, repository := newAssistant(t, fakePlanner{intent: kafkaDiagnosticIntent()})
	service = service.WithDiagnostics(stubDiagnostics{pkg: pkg})

	response, err := service.HandleMessage(context.Background(), admin(), "诊断 prod kafka orders 消费组", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Diagnostic == nil {
		t.Fatal("diagnostic = nil, want the diagnostic package returned alongside the recommendation plan")
	}
	if response.RecommendationPlan == nil {
		t.Fatalf("recommendation_plan = nil, want a plan summary for an actionable write recommendation")
	}
	plan := response.RecommendationPlan
	if plan.Tool != tools.TopicRetentionSet {
		t.Fatalf("tool = %q, want %q", plan.Tool, tools.TopicRetentionSet)
	}
	if plan.PlanID == "" {
		t.Fatal("plan_id = empty, want a pending plan id")
	}
	if !plan.RequiresConfirmation {
		t.Fatal("requires_confirmation = false, want true for a write recommendation")
	}
	if plan.Risk != string(tools.Medium) {
		t.Fatalf("risk = %q, want %q", plan.Risk, tools.Medium)
	}
	if plan.ExpiresAt == "" {
		t.Fatal("expires_at = empty, want an expiry timestamp")
	}
	stored, err := repository.GetPlan(context.Background(), plan.PlanID)
	if err != nil {
		t.Fatalf("stored plan: %v", err)
	}
	if stored.Status != store.PlanPendingConfirmation {
		t.Fatalf("stored status = %q, want pending_confirmation", stored.Status)
	}
}

func TestAssistantDiagnosticNonActionableRecommendationLeavesPlanNil(t *testing.T) {
	t.Parallel()
	pkg := diagnostics.Package{
		ID:          "diag-rec-2",
		Environment: "prod",
		Domains:     []string{"kafka"},
		Recommendations: []diagnostics.Recommendation{
			{ID: "rec-2", Summary: "继续监控 Kafka 消费组 orders", Rationale: "状态正常", Risk: tools.Low, Actionable: false},
		},
	}
	service, _ := newAssistant(t, fakePlanner{intent: kafkaDiagnosticIntent()})
	service = service.WithDiagnostics(stubDiagnostics{pkg: pkg})

	response, err := service.HandleMessage(context.Background(), viewer(), "诊断 prod kafka orders 消费组", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Diagnostic == nil {
		t.Fatal("diagnostic = nil, want the diagnostic package")
	}
	if response.RecommendationPlan != nil {
		t.Fatalf("recommendation_plan = %+v, want nil for a non-actionable recommendation", response.RecommendationPlan)
	}
	if response.Answer["message"] != "Diagnostic package is ready." {
		t.Fatalf("answer = %+v, want the default diagnostic message", response.Answer)
	}
}

func TestAssistantDiagnosticRecommendationPolicyDeniedLeavesPlanNil(t *testing.T) {
	t.Parallel()
	pkg := diagnostics.Package{
		ID:          "diag-rec-3",
		Environment: "prod",
		Domains:     []string{"kafka"},
		Recommendations: []diagnostics.Recommendation{
			{ID: "rec-3", Summary: "调整 retention", Rationale: "延迟过高", Risk: tools.Medium, Actionable: true, ToolName: tools.TopicRetentionSet, CandidateInput: retentionInput()},
		},
	}
	service, _ := newAssistant(t, fakePlanner{intent: kafkaDiagnosticIntent()})
	service = service.WithDiagnostics(stubDiagnostics{pkg: pkg})

	// viewer lacks permission for the write tool topic.retention.set, so the
	// recommendation is policy-denied. The diagnostic package must still be
	// returned and no error must surface.
	response, err := service.HandleMessage(context.Background(), viewer(), "诊断 prod kafka orders 消费组", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v, want no error when the recommendation is policy-denied", err)
	}
	if response.Diagnostic == nil {
		t.Fatal("diagnostic = nil, want the package returned even when the recommendation is denied")
	}
	if response.RecommendationPlan != nil {
		t.Fatalf("recommendation_plan = %+v, want nil when policy denies the recommendation", response.RecommendationPlan)
	}
}

func TestAssistantDiagnosticRecommendationReadExecutesAndPopulatesAnswer(t *testing.T) {
	t.Parallel()
	pkg := diagnostics.Package{
		ID:          "diag-rec-4",
		Environment: "prod",
		Domains:     []string{"kafka"},
		Recommendations: []diagnostics.Recommendation{
			{ID: "rec-4", Summary: "重新查看消费组延迟", Rationale: "需复核最新状态", Risk: tools.Low, Actionable: true, ToolName: tools.KafkaConsumerLagRead, CandidateInput: map[string]any{"environment": "prod", "name": "orders"}},
		},
	}
	service, _ := newAssistant(t, fakePlanner{intent: kafkaDiagnosticIntent()})
	service = service.WithDiagnostics(stubDiagnostics{pkg: pkg})

	response, err := service.HandleMessage(context.Background(), viewer(), "诊断 prod kafka orders 消费组", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Diagnostic == nil {
		t.Fatal("diagnostic = nil, want the package")
	}
	if response.Answer == nil || response.Answer["status"] != "green" {
		t.Fatalf("answer = %+v, want an executed read result with status=green", response.Answer)
	}
	if response.Answer["tool"] != tools.KafkaConsumerLagRead {
		t.Fatalf("answer tool = %v, want %q", response.Answer["tool"], tools.KafkaConsumerLagRead)
	}
	if response.RecommendationPlan != nil {
		t.Fatalf("recommendation_plan = %+v, want nil for an executed read recommendation", response.RecommendationPlan)
	}
}

// TestAssistantDiagnosticFactSetAggregatesDiagnosticAndRecommendationRead
// verifies 缺口-4 (FactSet aggregation) in the diagnostic branch: when a
// diagnostic package carries an actionable read recommendation and a
// CodeFallbackFormatter is wired, the formatted response must carry one
// tool_trace block per fact — the diagnostic fact plus the executed
// recommendation-read fact. Without FactSet aggregation the fallback only
// sees the single top-level Answer and drops the diagnostic context.
func TestAssistantDiagnosticFactSetAggregatesDiagnosticAndRecommendationRead(t *testing.T) {
	t.Parallel()
	pkg := diagnostics.Package{
		ID:          "diag-fact-1",
		Environment: "prod",
		Domains:     []string{"kafka"},
		Resources:   []diagnostics.ResourceRef{{Domain: "kafka", Type: "consumer_group", ID: "kafka:consumer_group:orders", Name: "orders", Environment: "prod"}},
		Observations: []diagnostics.Observation{
			{ID: "obs-1", ResourceID: "kafka:consumer_group:orders", Kind: "lag", Severity: diagnostics.SeverityWarning, Summary: "消费组延迟升高", CollectedAt: time.Date(2026, time.July, 21, 10, 0, 0, 0, time.UTC)},
		},
		Findings: []diagnostics.Finding{
			{ID: "f-1", Severity: diagnostics.SeverityWarning, Summary: "延迟超过阈值", Confidence: diagnostics.ConfidenceHigh},
		},
		Recommendations: []diagnostics.Recommendation{
			{ID: "rec-1", Summary: "复核最新消费组延迟", Rationale: "需确认是否已恢复", Risk: tools.Low, Actionable: true, ToolName: tools.KafkaConsumerLagRead, CandidateInput: map[string]any{"environment": "prod", "name": "orders"}},
		},
		CreatedAt: time.Date(2026, time.July, 21, 10, 0, 0, 0, time.UTC),
	}
	service, _ := newAssistant(t, fakePlanner{intent: kafkaDiagnosticIntent()})
	service = service.WithDiagnostics(stubDiagnostics{pkg: pkg})
	// Wire the real code fallback formatter so FactSet aggregation runs.
	service = service.WithFormatter(assistant.NewCodeFallbackFormatter())

	response, err := service.HandleMessage(context.Background(), viewer(), "诊断 prod kafka orders 消费组", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Diagnostic == nil {
		t.Fatal("diagnostic = nil, want the package")
	}
	// FactSet should contain 2 facts: the diagnostic fact + the recommendation
	// read fact. The fallback formatter emits one tool_trace per fact.
	toolTraceCount := 0
	for _, b := range response.Blocks {
		if b.Type == assistant.BlockToolTrace {
			toolTraceCount++
		}
	}
	if toolTraceCount != 2 {
		t.Fatalf("tool_trace block count = %d, want 2 (diagnostic + recommendation read)", toolTraceCount)
	}
}

// TestAssistantDiagnosticFactSetAggregatesDiagnosticOnlyWhenNoRecommendation
// verifies the no-recommendation path: when the diagnostic package has no
// actionable recommendation, FactSet contains only the diagnostic fact and
// the fallback emits exactly one tool_trace block.
func TestAssistantDiagnosticFactSetAggregatesDiagnosticOnlyWhenNoRecommendation(t *testing.T) {
	t.Parallel()
	pkg := diagnostics.Package{
		ID:          "diag-fact-2",
		Environment: "prod",
		Domains:     []string{"kafka"},
		Observations: []diagnostics.Observation{
			{ID: "obs-1", ResourceID: "kafka:consumer_group:orders", Kind: "lag", Severity: diagnostics.SeverityOK, Summary: "消费组延迟正常", CollectedAt: time.Date(2026, time.July, 21, 10, 0, 0, 0, time.UTC)},
		},
		// No recommendations.
		CreatedAt: time.Date(2026, time.July, 21, 10, 0, 0, 0, time.UTC),
	}
	service, _ := newAssistant(t, fakePlanner{intent: kafkaDiagnosticIntent()})
	service = service.WithDiagnostics(stubDiagnostics{pkg: pkg})
	service = service.WithFormatter(assistant.NewCodeFallbackFormatter())

	response, err := service.HandleMessage(context.Background(), viewer(), "诊断 prod kafka orders 消费组", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Diagnostic == nil {
		t.Fatal("diagnostic = nil, want the package")
	}
	toolTraceCount := 0
	for _, b := range response.Blocks {
		if b.Type == assistant.BlockToolTrace {
			toolTraceCount++
		}
	}
	if toolTraceCount != 1 {
		t.Fatalf("tool_trace block count = %d, want 1 (diagnostic only, no recommendation)", toolTraceCount)
	}
}

// recordingRunner is a thread-safe fake DiagnosticRunner that records the
// domain of each request it receives. Wrapped by the orchestrator, it proves
// whether the assistant injected the user message into the context: with the
// message present the orchestrator splits a multi-domain request into multiple
// runner calls; without it only a single call reaches the runner.
type recordingRunner struct {
	mu       sync.Mutex
	calls    []string
	packages map[string]diagnostics.Package
}

func (r *recordingRunner) Run(_ context.Context, _ identity.CurrentUser, request diagnostics.Request) (diagnostics.Package, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, request.Domain)
	if pkg, ok := r.packages[request.Domain]; ok {
		return pkg, nil
	}
	return diagnostics.Package{ID: "diag-" + request.Domain, Environment: request.Environment, Domains: []string{request.Domain}, CreatedAt: time.Date(2026, time.July, 31, 10, 0, 0, 0, time.UTC)}, nil
}

func (r *recordingRunner) domains() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	return out
}

// TestAssistantOrchestratorIntegrationSplitsMultiDomainMessage verifies that
// the assistant service injects the user message into the diagnostic context,
// enabling the orchestrator to detect and split multi-domain requests. Without
// the injection the orchestrator would receive an empty message and delegate a
// single request; with it the orchestrator splits "kafka 和 minio" into two
// concurrent sub-requests.
func TestAssistantOrchestratorIntegrationSplitsMultiDomainMessage(t *testing.T) {
	t.Parallel()
	runner := &recordingRunner{
		packages: map[string]diagnostics.Package{
			"kafka": {ID: "diag-kafka", Environment: "prod", Domains: []string{"kafka"}, CreatedAt: time.Date(2026, time.July, 31, 10, 0, 0, 0, time.UTC)},
			"minio": {ID: "diag-minio", Environment: "prod", Domains: []string{"minio"}, CreatedAt: time.Date(2026, time.July, 31, 10, 0, 0, 0, time.UTC)},
		},
	}
	orch := orchestrator.New(runner, 3, func() time.Time { return time.Date(2026, time.July, 31, 10, 0, 0, 0, time.UTC) })
	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{
		Diagnostic: &diagnostics.Request{Domain: "kafka", Environment: "prod", Runbook: "health"},
	}})
	service = service.WithDiagnostics(orch)

	_, err := service.HandleMessage(context.Background(), admin(), "检查 prod kafka 和 minio 健康状态", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}

	domains := runner.domains()
	if len(domains) != 2 {
		t.Fatalf("runner calls = %v (count %d), want 2 domains [kafka minio] — message was not injected into diagnostic context", domains, len(domains))
	}
}

// --- IntentType 分类（借鉴-2）测试 ---

func TestClassifyIntent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		intent assistant.Intent
		want   assistant.IntentType
	}{
		{
			name:   "empty intent defaults to advisory",
			intent: assistant.Intent{},
			want:   assistant.IntentAdvisory,
		},
		{
			name:   "read tool classifies as advisory",
			intent: assistant.Intent{ToolName: tools.ClusterStatusRead, Input: map[string]any{"environment": "prod"}},
			want:   assistant.IntentAdvisory,
		},
		{
			name:   "write tool classifies as executive",
			intent: assistant.Intent{ToolName: tools.TopicRetentionSet, Type: assistant.IntentExecutive, Input: retentionInput()},
			want:   assistant.IntentExecutive,
		},
		{
			name:   "diagnostic intent classifies as advisory",
			intent: assistant.Intent{Diagnostic: &diagnostics.Request{Domain: "kafka", Environment: "prod", Runbook: "health"}},
			want:   assistant.IntentAdvisory,
		},
		{
			name:   "explicit generative type is preserved",
			intent: assistant.Intent{Type: assistant.IntentGenerative, ToolName: tools.TopicRetentionSet, Input: retentionInput()},
			want:   assistant.IntentGenerative,
		},
		{
			name:   "explicit executive type is preserved",
			intent: assistant.Intent{Type: assistant.IntentExecutive, ToolName: tools.TopicRetentionSet, Input: retentionInput()},
			want:   assistant.IntentExecutive,
		},
		{
			name:   "explicit advisory type is preserved",
			intent: assistant.Intent{Type: assistant.IntentAdvisory, ToolName: tools.ClusterStatusRead, Input: map[string]any{"environment": "prod"}},
			want:   assistant.IntentAdvisory,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := assistant.ClassifyIntent(tt.intent)
			if got != tt.want {
				t.Fatalf("ClassifyIntent(%+v) = %q, want %q", tt.intent, got, tt.want)
			}
		})
	}
}

// TestAssistantGenerativeIntentReturnsDraft verifies that a generative intent
// (Type=generative) returns a draft response without executing the tool or
// creating a pending plan. The draft carries the resolved tool + input so the
// operator can review and modify before converting to an executive action.
func TestAssistantGenerativeIntentReturnsDraft(t *testing.T) {
	t.Parallel()
	service, repository := newAssistant(t, fakePlanner{intent: assistant.Intent{
		Type:     assistant.IntentGenerative,
		ToolName: tools.TopicRetentionSet,
		Input:    retentionInput(),
	}})

	response, err := service.HandleMessage(context.Background(), admin(), "帮我写个 retention 草稿", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "draft" {
		t.Fatalf("type = %q, want draft", response.Type)
	}
	if response.Tool != tools.TopicRetentionSet {
		t.Fatalf("tool = %q, want %q", response.Tool, tools.TopicRetentionSet)
	}
	// Draft must carry the resolved input so the operator can review/modify.
	if response.Answer == nil || response.Answer["topic"] != "orders" {
		t.Fatalf("answer = %+v, want draft input with topic=orders", response.Answer)
	}
	// No plan should be created for a generative draft.
	if response.PlanID != "" {
		t.Fatalf("plan_id = %q, want empty for a draft", response.PlanID)
	}
	page, err := repository.ListPlans(context.Background(), store.PlanFilter{Limit: 100})
	if err != nil {
		t.Fatalf("list plans: %v", err)
	}
	if len(page.Plans) != 0 {
		t.Fatalf("stored plans = %d, want 0 for a generative draft", len(page.Plans))
	}
}

// TestAssistantExecutiveIntentReturnsConfirmationRequired verifies that an
// explicit executive intent still routes to the confirmation_required path
// (existing write behavior), even when the planner sets Type explicitly.
func TestAssistantExecutiveIntentReturnsConfirmationRequired(t *testing.T) {
	t.Parallel()
	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{
		Type:     assistant.IntentExecutive,
		ToolName: tools.TopicRetentionSet,
		Input:    retentionInput(),
	}})

	response, err := service.HandleMessage(context.Background(), admin(), "执行 retention 变更", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "confirmation_required" {
		t.Fatalf("type = %q, want confirmation_required", response.Type)
	}
	if response.PlanID == "" {
		t.Fatal("plan_id = empty, want a pending plan")
	}
}

// TestAssistantAdvisoryIntentReturnsAnswer verifies that an explicit advisory
// intent routes to the direct-answer path (existing read behavior).
func TestAssistantAdvisoryIntentReturnsAnswer(t *testing.T) {
	t.Parallel()
	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{
		Type:     assistant.IntentAdvisory,
		ToolName: tools.ClusterStatusRead,
		Input:    map[string]any{"environment": "prod"},
	}})

	response, err := service.HandleMessage(context.Background(), viewer(), "查看 prod 集群状态", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "answer" {
		t.Fatalf("type = %q, want answer", response.Type)
	}
	if response.Answer["status"] != "green" {
		t.Fatalf("answer status = %v, want green", response.Answer["status"])
	}
}

// TestAssistantGenerativeDraftAttachesSimplifiedDryRunBlock verifies that a
// generative draft auto-previews the operation when a dry-run runner is wired,
// attaching a simplified risk_notice block. Unlike the executive path which
// returns the full preview (summary + affected_resources + commands +
// warnings), the draft preview omits commands (execution detail not yet
// decided) and uses a draft-specific title. This lets the operator see the
// expected impact while still in the "review and modify" stage.
func TestAssistantGenerativeDraftAttachesSimplifiedDryRunBlock(t *testing.T) {
	t.Parallel()
	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{
		Type:     assistant.IntentGenerative,
		ToolName: tools.TopicRetentionSet,
		Input:    retentionInput(),
	}})
	dryRun := &fakeDryRunRunner{result: execution.DryRunResult{
		Summary:           "将把 prod 环境的 topic orders 的消息保留时间设置为 72 小时。",
		AffectedResources: []string{"topic:orders@prod"},
		Commands:          []string{"kafka-configs --alter ..."},
		Warnings:          []string{"缩短保留时间可能导致历史消息被删除"},
	}}
	service = service.WithDryRunRunner(dryRun)

	response, err := service.HandleMessage(context.Background(), admin(), "帮我写个 retention 草稿", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "draft" {
		t.Fatalf("type = %q, want draft", response.Type)
	}
	if !dryRun.called {
		t.Fatal("dry-run was not invoked for a generative draft")
	}
	var riskBlock *assistant.Block
	for i := range response.Blocks {
		if response.Blocks[i].Type == assistant.BlockRiskNotice {
			riskBlock = &response.Blocks[i]
			break
		}
	}
	if riskBlock == nil {
		t.Fatalf("response.Blocks = %+v, want a risk_notice block for the draft", response.Blocks)
	}
	if riskBlock.Content == "" {
		t.Error("risk_notice block has empty content (summary)")
	}
	payload := riskBlock.Payload
	if payload == nil {
		t.Fatal("risk_notice payload is nil")
	}
	if _, ok := payload["affected_resources"]; !ok {
		t.Error("draft risk_notice payload missing affected_resources")
	}
	if _, ok := payload["warnings"]; !ok {
		t.Error("draft risk_notice payload missing warnings")
	}
	// Draft preview must NOT carry commands — execution detail is not yet
	// decided in the generative stage.
	if _, ok := payload["commands"]; ok {
		t.Error("draft risk_notice payload should not include commands (generative stage omits execution detail)")
	}
}

// TestAssistantGenerativeDraftDryRunFailureStillReturnsDraft verifies that a
// dry-run failure (e.g. unsupported tool) is silently ignored for a generative
// draft: the draft is still returned, just without a risk_notice block.
func TestAssistantGenerativeDraftDryRunFailureStillReturnsDraft(t *testing.T) {
	t.Parallel()
	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{
		Type:     assistant.IntentGenerative,
		ToolName: tools.TopicRetentionSet,
		Input:    retentionInput(),
	}})
	dryRun := &fakeDryRunRunner{err: execution.ErrDryRunNotSupported}
	service = service.WithDryRunRunner(dryRun)

	response, err := service.HandleMessage(context.Background(), admin(), "帮我写个 retention 草稿", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "draft" {
		t.Fatalf("type = %q, want draft", response.Type)
	}
	if len(response.Blocks) != 0 {
		t.Fatalf("response.Blocks = %+v, want empty when dry-run fails", response.Blocks)
	}
}

// TestAssistantSystemPostureReturnsAggregateAnswer verifies that the
// QuerySystemPosture read tool returns an answer with multi-domain posture
// data (借鉴-1: 系统态势 SLA 入口). The operator asks "系统怎么样" and gets
// an aggregate view instead of a single-domain health check.
func TestAssistantSystemPostureReturnsAggregateAnswer(t *testing.T) {
	t.Parallel()
	service, _ := newAssistant(t, fakePlanner{intent: assistant.Intent{
		ToolName: tools.QuerySystemPosture,
		Input:    map[string]any{"environment": "prod"},
	}})

	response, err := service.HandleMessage(context.Background(), viewer(), "prod 系统怎么样", "", assistant.PageContext{})
	if err != nil {
		t.Fatalf("handle message: %v", err)
	}
	if response.Type != "answer" {
		t.Fatalf("type = %q, want answer", response.Type)
	}
	if response.Tool != tools.QuerySystemPosture {
		t.Fatalf("tool = %q, want %q", response.Tool, tools.QuerySystemPosture)
	}
	// The read runner returns {status: green} for all tools; SystemPosture
	// should at minimum surface the environment and a status field.
	if response.Answer["environment"] != "prod" {
		t.Fatalf("answer environment = %v, want prod", response.Answer["environment"])
	}
	if response.Answer["status"] == "" {
		t.Error("answer status is empty, want a posture status")
	}
}
