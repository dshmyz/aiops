package assistant_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestCapabilityAwarePlannerResolvesDynamicReadCapability(t *testing.T) {
	registerDynamicCapacityTool(t)
	planner := assistant.NewCapabilityAwarePlanner(failingPlanner{})

	intent, err := planner.Plan(context.Background(), viewer(), "查一下 prod m1 archive bucket 的 minio 容量", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("Plan returned %v", err)
	}
	if intent.ToolName != "minio.bucket.capacity.read" {
		t.Fatalf("tool = %q, want dynamic capacity tool", intent.ToolName)
	}
	if intent.Input["environment"] != "prod" || intent.Input["cluster"] != "m1" || intent.Input["bucket"] != "archive" {
		t.Fatalf("input = %+v, want extracted environment, cluster, bucket", intent.Input)
	}
}

func TestCapabilityAwarePlannerClarifiesMissingDynamicInputs(t *testing.T) {
	registerDynamicCapacityTool(t)
	planner := assistant.NewCapabilityAwarePlanner(failingPlanner{})

	_, err := planner.Plan(context.Background(), viewer(), "查一下 prod minio bucket 容量", nil, assistant.PageContext{})
	if !errors.Is(err, assistant.ErrClarificationNeeded) || !strings.Contains(err.Error(), "cluster") || !strings.Contains(err.Error(), "bucket") {
		t.Fatalf("error = %v, want clarification naming missing fields", err)
	}
}

func TestCapabilityAwarePlannerFallsBackWhenNoDynamicToolMatches(t *testing.T) {
	registerDynamicCapacityTool(t)
	planner := assistant.NewCapabilityAwarePlanner(fakePlanner{intent: assistant.Intent{ToolName: tools.ClusterStatusRead, Input: map[string]any{"environment": "prod"}}})

	intent, err := planner.Plan(context.Background(), viewer(), "查看 prod 集群状态", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("Plan returned %v", err)
	}
	if intent.ToolName != tools.ClusterStatusRead {
		t.Fatalf("tool = %q, want fallback static planner", intent.ToolName)
	}
}

func TestCapabilityAwarePlannerFallsBackWhenDynamicOperationDoesNotMatch(t *testing.T) {
	registerDynamicCapacityTool(t)
	planner := assistant.NewCapabilityAwarePlanner(fakePlanner{intent: assistant.Intent{ToolName: tools.MinIOBucketHealthRead, Input: map[string]any{"environment": "prod"}}})

	intent, err := planner.Plan(context.Background(), viewer(), "prod minio archive bucket health", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("Plan returned %v", err)
	}
	if intent.ToolName != tools.MinIOBucketHealthRead {
		t.Fatalf("tool = %q, want fallback health intent", intent.ToolName)
	}
}

func TestCapabilityAwarePlannerClarifiesWhenConnectorIsNotACluster(t *testing.T) {
	registerDynamicCapacityTool(t)
	planner := assistant.NewCapabilityAwarePlanner(failingPlanner{})

	_, err := planner.Plan(context.Background(), viewer(), "prod minio cluster 的 archive bucket capacity", nil, assistant.PageContext{})
	if !errors.Is(err, assistant.ErrClarificationNeeded) || !strings.Contains(err.Error(), "cluster") {
		t.Fatalf("error = %v, want clarification naming cluster", err)
	}
}

func TestCapabilityAwarePlannerClarifiesAmbiguousDynamicReadCapabilities(t *testing.T) {
	registerDynamicTools(t,
		dynamicReadTool("minio.bucket.capacity.read", map[string]tools.DynamicInputField{
			"environment": {Type: "string", Required: true},
			"cluster":     {Type: "string", Required: true},
			"bucket":      {Type: "string", Required: true},
		}),
		dynamicReadTool("minio.bucket.capacity.summary.read", map[string]tools.DynamicInputField{
			"environment": {Type: "string", Required: true},
			"cluster":     {Type: "string", Required: true},
			"bucket":      {Type: "string", Required: true},
		}),
	)
	planner := assistant.NewCapabilityAwarePlanner(failingPlanner{})

	_, err := planner.Plan(context.Background(), viewer(), "prod m1 archive bucket 的 minio 容量", nil, assistant.PageContext{})
	if !errors.Is(err, assistant.ErrClarificationNeeded) || !strings.Contains(err.Error(), "minio.bucket.capacity.read") || !strings.Contains(err.Error(), "minio.bucket.capacity.summary.read") {
		t.Fatalf("error = %v, want clarification naming ambiguous capabilities", err)
	}
}

func TestCapabilityAwarePlannerClarifiesInvalidBooleanInput(t *testing.T) {
	registerDynamicTools(t, dynamicReadTool("minio.bucket.lifecycle.read", map[string]tools.DynamicInputField{
		"environment": {Type: "string", Required: true},
		"enabled":     {Type: "boolean", Required: true},
	}))
	planner := assistant.NewCapabilityAwarePlanner(failingPlanner{})

	_, err := planner.Plan(context.Background(), viewer(), "prod minio bucket lifecycle enabled=maybe", nil, assistant.PageContext{})
	if !errors.Is(err, assistant.ErrClarificationNeeded) || !strings.Contains(err.Error(), "enabled") {
		t.Fatalf("error = %v, want clarification naming invalid boolean field", err)
	}
}

func TestCapabilityAwarePlannerCoercesChineseBooleanInput(t *testing.T) {
	registerDynamicTools(t, dynamicReadTool("minio.bucket.lifecycle.read", map[string]tools.DynamicInputField{
		"environment": {Type: "string", Required: true},
		"enabled":     {Type: "boolean", Required: true},
	}))
	planner := assistant.NewCapabilityAwarePlanner(failingPlanner{})

	enabledIntent, err := planner.Plan(context.Background(), viewer(), "prod minio bucket lifecycle enabled=是", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("Plan true boolean returned %v", err)
	}
	if enabledIntent.Input["enabled"] != true {
		t.Fatalf("input = %+v, want enabled=true", enabledIntent.Input)
	}

	disabledIntent, err := planner.Plan(context.Background(), viewer(), "prod minio bucket lifecycle enabled=否", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("Plan false boolean returned %v", err)
	}
	if disabledIntent.Input["enabled"] != false {
		t.Fatalf("input = %+v, want enabled=false", disabledIntent.Input)
	}
}

func TestCapabilityAwarePlannerMatchesGenericReadCue(t *testing.T) {
	registerDynamicCapacityTool(t)
	planner := assistant.NewCapabilityAwarePlanner(failingPlanner{})

	intent, err := planner.Plan(context.Background(), viewer(), "查询 prod m1 archive bucket 的 minio", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("Plan returned %v", err)
	}
	if intent.ToolName != "minio.bucket.capacity.read" || intent.Input["cluster"] != "m1" || intent.Input["bucket"] != "archive" {
		t.Fatalf("intent = %+v, want capacity read intent from generic read cue", intent)
	}
}

func TestCapabilityAwarePlannerSelectionIncludesCandidatesAndExtractedParameters(t *testing.T) {
	registerDynamicCapacityTool(t)
	planner := assistant.NewCapabilityAwarePlanner(failingPlanner{})

	intent, err := planner.Plan(context.Background(), viewer(), "查一下 prod m1 archive bucket 的 minio 容量", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("Plan returned %v", err)
	}
	if intent.Selection == nil {
		t.Fatalf("intent.Selection = nil, want selection trace")
	}
	selection := intent.Selection
	if selection.Selected != "minio.bucket.capacity.read" {
		t.Fatalf("selected = %q, want minio.bucket.capacity.read", selection.Selected)
	}
	if selection.Confidence != 0.5 {
		t.Fatalf("confidence = %v, want 0.5", selection.Confidence)
	}
	if selection.Reason == "" {
		t.Fatalf("reason is empty, want explanation")
	}
	if len(selection.Candidates) == 0 {
		t.Fatalf("candidates is empty, want at least the selected capability")
	}
	found := false
	for _, candidate := range selection.Candidates {
		if candidate.Name != "minio.bucket.capacity.read" {
			continue
		}
		if candidate.Score < 2 {
			t.Fatalf("candidate score = %d, want >= 2", candidate.Score)
		}
		if len(candidate.Reasons) == 0 {
			t.Fatalf("candidate %s reasons is empty", candidate.Name)
		}
		found = true
		break
	}
	if !found {
		t.Fatalf("candidates = %+v, want minio.bucket.capacity.read entry", selection.Candidates)
	}
	extracted := map[string]assistant.ExtractedParameter{}
	for _, param := range selection.Extracted {
		extracted[param.Name] = param
	}
	for _, required := range []string{"environment", "cluster", "bucket"} {
		param, ok := extracted[required]
		if !ok {
			t.Fatalf("extracted = %+v, want %s included", selection.Extracted, required)
		}
		if param.Source == "" {
			t.Fatalf("parameter %s source is empty", required)
		}
	}
	if extracted["environment"].Value != "prod" || extracted["cluster"].Value != "m1" || extracted["bucket"].Value != "archive" {
		t.Fatalf("extracted values = %+v, want prod/m1/archive", selection.Extracted)
	}
}

func TestCapabilityAwarePlannerSelectionCarriesMissingFieldsForClarification(t *testing.T) {
	registerDynamicCapacityTool(t)
	planner := assistant.NewCapabilityAwarePlanner(failingPlanner{})

	_, err := planner.Plan(context.Background(), viewer(), "查一下 prod minio bucket 容量", nil, assistant.PageContext{})
	if err == nil {
		t.Fatal("Plan returned nil error, want clarification")
	}
	var clarification assistant.ClarificationError
	if !errors.As(err, &clarification) {
		t.Fatalf("error = %v, want ClarificationError", err)
	}
	if clarification.Selection == nil {
		t.Fatalf("clarification.Selection = nil, want selection with missing fields")
	}
	if clarification.Selection.Selected != "minio.bucket.capacity.read" {
		t.Fatalf("selected = %q, want minio.bucket.capacity.read (single winner but missing fields)", clarification.Selection.Selected)
	}
	if len(clarification.Selection.Missing) == 0 {
		t.Fatalf("missing = %+v, want missing required fields", clarification.Selection.Missing)
	}
}

func TestCapabilityAwarePlannerCoercesNumberInput(t *testing.T) {
	registerDynamicTools(t, dynamicReadTool("minio.bucket.threshold.read", map[string]tools.DynamicInputField{
		"environment": {Type: "string", Required: true},
		"threshold":   {Type: "number", Required: true},
	}))
	planner := assistant.NewCapabilityAwarePlanner(failingPlanner{})

	intent, err := planner.Plan(context.Background(), viewer(), "prod minio bucket threshold=86.5", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("Plan returned %v", err)
	}
	if intent.ToolName != "minio.bucket.threshold.read" || intent.Input["threshold"] != 86.5 {
		t.Fatalf("intent = %+v, want numeric threshold input", intent)
	}
}

func TestCapabilityAwarePlannerClarifiesInvalidNumberInput(t *testing.T) {
	registerDynamicTools(t, dynamicReadTool("minio.bucket.threshold.read", map[string]tools.DynamicInputField{
		"environment": {Type: "string", Required: true},
		"threshold":   {Type: "number", Required: true},
	}))
	planner := assistant.NewCapabilityAwarePlanner(failingPlanner{})

	_, err := planner.Plan(context.Background(), viewer(), "prod minio bucket threshold=high", nil, assistant.PageContext{})
	if !errors.Is(err, assistant.ErrClarificationNeeded) || !strings.Contains(err.Error(), "threshold") {
		t.Fatalf("error = %v, want clarification naming invalid numeric field", err)
	}
}

func TestCapabilityAwarePlannerResolvesDynamicWriteCapability(t *testing.T) {
	registerDynamicWriteTool(t)
	planner := assistant.NewCapabilityAwarePlanner(failingPlanner{})

	intent, err := planner.Plan(context.Background(), admin(), "set prod m1 archive bucket quota=100", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("Plan returned %v", err)
	}
	if intent.ToolName != "minio.bucket.quota.set" {
		t.Fatalf("tool = %q, want dynamic write capability", intent.ToolName)
	}
	if intent.Input["environment"] != "prod" || intent.Input["cluster"] != "m1" || intent.Input["bucket"] != "archive" || intent.Input["quota"] != 100 {
		t.Fatalf("input = %+v, want extracted write inputs", intent.Input)
	}
}

func TestCapabilityAwarePlannerClarifiesMissingDynamicWriteInputs(t *testing.T) {
	registerDynamicWriteTool(t)
	planner := assistant.NewCapabilityAwarePlanner(failingPlanner{})

	_, err := planner.Plan(context.Background(), admin(), "set prod m1 archive bucket quota", nil, assistant.PageContext{})
	if !errors.Is(err, assistant.ErrClarificationNeeded) || !strings.Contains(err.Error(), "quota") {
		t.Fatalf("error = %v, want clarification naming missing quota", err)
	}
}

func TestCapabilityAwarePlannerFallsBackWhenWriteIntentMissing(t *testing.T) {
	registerDynamicWriteTool(t)
	planner := assistant.NewCapabilityAwarePlanner(fakePlanner{intent: assistant.Intent{ToolName: tools.ClusterStatusRead, Input: map[string]any{"environment": "prod"}}})

	intent, err := planner.Plan(context.Background(), admin(), "prod m1 archive bucket quota", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("Plan returned %v", err)
	}
	if intent.ToolName != tools.ClusterStatusRead {
		t.Fatalf("tool = %q, want fallback intent when no write cue present", intent.ToolName)
	}
}

func TestCapabilityAwarePlannerMatchesGenericWriteCueInChinese(t *testing.T) {
	registerDynamicWriteTool(t)
	planner := assistant.NewCapabilityAwarePlanner(failingPlanner{})

	intent, err := planner.Plan(context.Background(), admin(), "配置 prod m1 archive bucket 的 quota=100", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("Plan returned %v", err)
	}
	if intent.ToolName != "minio.bucket.quota.set" {
		t.Fatalf("tool = %q, want dynamic write capability from Chinese cue", intent.ToolName)
	}
}

type failingPlanner struct{}

func (failingPlanner) Plan(context.Context, identity.CurrentUser, string, []assistant.Turn, assistant.PageContext) (assistant.Intent, error) {
	return assistant.Intent{}, errors.New("fallback should not be called")
}

// TestCapabilityAwarePlannerPrefersStaticToolOverDynamic verifies that a message
// hitting a static tool keyword (e.g. 告警) is resolved by the deterministic
// The planner must keep preferring the static AlertQuery tool over a dynamic
// capability even when a dynamic capability's loose name-token matching would also
// claim it (Bug1: 动态能力匹配误抢静态工具).
//
// NOTE: intentionally NOT t.Parallel(). registerDynamicTools resets the
// process-global dynamic-tool registry, which would wipe the middleware tools
// (topic.retention.set etc.) out from under the other t.Parallel() tests that
// route through newAssistantWithStore. Keep it serial with its reset-using
// siblings (see TestCapabilityAwarePlannerRoutesWriteToDynamicCapability).
func TestCapabilityAwarePlannerPrefersStaticToolOverDynamic(t *testing.T) {
	registerDynamicTools(t, dynamicReadTool("glusterfs.volume.status.read", map[string]tools.DynamicInputField{
		"environment": {Type: "string", Required: true},
		"cluster":     {Type: "string", Required: true},
		"volume":      {Type: "string", Required: true},
	}))
	planner := assistant.NewCapabilityAwarePlanner(assistant.DeterministicPlanner{})

	intent, err := planner.Plan(context.Background(), viewer(), "当前有哪些告警", nil, assistant.PageContext{Environment: "prod"})
	if err != nil {
		t.Fatalf("Plan returned %v", err)
	}
	if intent.ToolName != tools.AlertQuery {
		t.Fatalf("tool = %q, want static %q (dynamic must not hijack)", intent.ToolName, tools.AlertQuery)
	}
}

// TestCapabilityAwarePlannerRoutesWriteToDynamicCapability verifies that a
// write intent (e.g. "配置 ... 保留 72 小时") is routed to the dynamic
// capability (kafka.topic.retention.write) instead of the static tool
// (topic.retention.set) when a matching dynamic capability is registered.
func TestCapabilityAwarePlannerRoutesWriteToDynamicCapability(t *testing.T) {
	// 不使用 t.Parallel()，因为 registerDynamicTools 会清理全局状态
	registerDynamicTools(t, tools.DynamicToolDefinition{
		Tool: tools.Tool{
			Name:                "kafka.topic.retention.write",
			Operation:           tools.Write,
			Risk:                tools.Medium,
			Domain:              "kafka",
			ResourceType:        "topic",
			RollbackDescription: "Reset topic retention to previous value",
		},
		InputSchema: map[string]tools.DynamicInputField{
			"environment":     {Type: "string", Required: true},
			"cluster":         {Type: "string", Required: true},
			"topic":           {Type: "string", Required: true},
			"retention_hours": {Type: "integer", Required: true},
		},
	})
	planner := assistant.NewCapabilityAwarePlanner(assistant.DeterministicPlanner{})

	intent, err := planner.Plan(context.Background(), admin(), "配置 prod kafka m1 orders topic 保留 72 小时", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("Plan returned %v", err)
	}
	if intent.ToolName != "kafka.topic.retention.write" {
		t.Fatalf("tool = %q, want dynamic write capability kafka.topic.retention.write", intent.ToolName)
	}
	if intent.Input["retention_hours"] != 72 {
		t.Fatalf("retention_hours = %v, want 72", intent.Input["retention_hours"])
	}
}

func registerDynamicCapacityTool(t *testing.T) {
	t.Helper()
	registerDynamicTools(t, dynamicReadTool("minio.bucket.capacity.read", map[string]tools.DynamicInputField{
		"environment": {Type: "string", Required: true},
		"cluster":     {Type: "string", Required: true},
		"bucket":      {Type: "string", Required: true},
	}))
}

func dynamicReadTool(name string, schema map[string]tools.DynamicInputField) tools.DynamicToolDefinition {
	return tools.DynamicToolDefinition{
		Tool:        tools.Tool{Name: name, Operation: tools.Read, Risk: tools.Low, Domain: "minio", ResourceType: "bucket"},
		InputSchema: schema,
	}
}

func registerDynamicTools(t *testing.T, definitions ...tools.DynamicToolDefinition) {
	t.Helper()
	tools.ResetDynamicToolsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	err := tools.RegisterDynamicTools(definitions)
	if err != nil {
		t.Fatalf("register dynamic tools: %v", err)
	}
}

func registerDynamicWriteTool(t *testing.T) {
	t.Helper()
	tools.ResetDynamicToolsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{{
		Tool: tools.Tool{
			Name:                "minio.bucket.quota.set",
			Operation:           tools.Write,
			Risk:                tools.Medium,
			RollbackDescription: "Restore the previous quota through a confirmed action plan.",
			Domain:              "minio",
			ResourceType:        "bucket",
		},
		InputSchema: map[string]tools.DynamicInputField{
			"environment": {Type: "string", Required: true},
			"cluster":     {Type: "string", Required: true},
			"bucket":      {Type: "string", Required: true},
			"quota":       {Type: "integer", Required: true},
		},
	}})
	if err != nil {
		t.Fatalf("register dynamic write tool: %v", err)
	}
}
