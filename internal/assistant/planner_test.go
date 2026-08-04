package assistant_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestDeterministicPlannerParsesChineseClusterStatus(t *testing.T) {
	t.Parallel()

	intent, err := assistant.DeterministicPlanner{}.Plan(context.Background(), user(), "查看 prod 集群状态", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if intent.ToolName != tools.ClusterStatusRead {
		t.Fatalf("tool = %q, want %q", intent.ToolName, tools.ClusterStatusRead)
	}
	if intent.Input["environment"] != "prod" {
		t.Fatalf("input = %#v, want prod environment", intent.Input)
	}
}

// TestDeterministicPlannerParsesTopicRetentionWrite 验证中间件写意图经
// CapabilityAwarePlanner（生产链路）解析到已发布的动态写能力 topic.retention.set。
// 该工具不再硬编码在静态 allowlist 中，改为由 YAML published 能力注册。
func TestDeterministicPlannerParsesTopicRetentionWrite(t *testing.T) {
	registerMiddlewareWriteTool(t)
	planner := assistant.NewCapabilityAwarePlanner(assistant.DeterministicPlanner{})

	intent, err := planner.Plan(context.Background(), user(), "把 prod 的 orders topic retention 改成 72 小时", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if intent.ToolName != tools.TopicRetentionSet {
		t.Fatalf("tool = %q, want %q", intent.ToolName, tools.TopicRetentionSet)
	}
	if intent.Input["environment"] != "prod" || intent.Input["topic"] != "orders" || intent.Input["retention_hours"] != 72 {
		t.Fatalf("input = %#v, want prod/orders/72", intent.Input)
	}
}

func TestDeterministicPlannerClarifiesUnknownMessage(t *testing.T) {
	t.Parallel()

	_, err := assistant.DeterministicPlanner{}.Plan(context.Background(), user(), "帮我看看", nil, assistant.PageContext{})
	if !errors.Is(err, assistant.ErrClarificationNeeded) {
		t.Fatalf("error = %v, want ErrClarificationNeeded", err)
	}
}

func TestDeterministicPlannerIncludesSelectionSummary(t *testing.T) {
	t.Parallel()

	intent, err := assistant.DeterministicPlanner{}.Plan(context.Background(), user(), "查看 prod 集群状态", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if intent.Selection == nil {
		t.Fatalf("intent.Selection = nil, want selection summary")
	}
	if intent.Selection.Selected != tools.ClusterStatusRead {
		t.Fatalf("selected = %q, want %q", intent.Selection.Selected, tools.ClusterStatusRead)
	}
	if intent.Selection.Confidence != 0.9 {
		t.Fatalf("confidence = %v, want 0.9", intent.Selection.Confidence)
	}
	if intent.Selection.Reason == "" {
		t.Fatalf("reason is empty, want explanation")
	}
	foundEnvironment := false
	for _, param := range intent.Selection.Extracted {
		if param.Name == "environment" && param.Value == "prod" && param.Source == "environment" {
			foundEnvironment = true
			break
		}
	}
	if !foundEnvironment {
		t.Fatalf("extracted = %+v, want environment=prod with source=environment", intent.Selection.Extracted)
	}
}

func TestDeterministicPlannerIncludesSelectionForTopicRetention(t *testing.T) {
	registerMiddlewareWriteTool(t)
	planner := assistant.NewCapabilityAwarePlanner(assistant.DeterministicPlanner{})

	intent, err := planner.Plan(context.Background(), user(), "把 prod 的 orders topic retention 改成 72 小时", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if intent.Selection == nil {
		t.Fatalf("intent.Selection = nil, want selection summary")
	}
	if intent.Selection.Selected != tools.TopicRetentionSet {
		t.Fatalf("selected = %q, want %q", intent.Selection.Selected, tools.TopicRetentionSet)
	}
	extracted := map[string]assistant.ExtractedParameter{}
	for _, param := range intent.Selection.Extracted {
		extracted[param.Name] = param
	}
	if extracted["topic"].Value != "orders" || extracted["retention_hours"].Value != 72 {
		t.Fatalf("extracted = %+v, want orders/72", intent.Selection.Extracted)
	}
}

func user() identity.CurrentUser {
	return identity.CurrentUser{
		Subject:             "operator-1",
		Roles:               []string{"admin"},
		AllowedEnvironments: []string{"prod"},
		RequestID:           "request-1",
	}
}

// registerMiddlewareWriteTool loads the topic.retention.set write capability
// into the dynamic registry, mirroring the published YAML. The placeholder
// environment is required by validateDynamicInputSchema.
func registerMiddlewareWriteTool(t *testing.T) {
	t.Helper()
	tools.ResetDynamicToolsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{{
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
			"retention_hours": {Type: "integer", Required: true, Min: ptr(1), Max: ptr(8760)},
		},
	}})
	if err != nil {
		t.Fatalf("register middleware write tool: %v", err)
	}
}

func ptr(value float64) *float64 { return &value }

// --- PageContext（缺口-3：页面上下文带入）测试 ---

// TestDeterministicPlannerPageContextSuppliesEnvironment verifies that when
// the message lacks an environment token, the planner falls back to
// PageContext.Environment. This is the core fix for "用户在 prod 页面问'查看
// 集群状态'但没说环境"——planner 从页面上下文补全 environment。
func TestDeterministicPlannerPageContextSuppliesEnvironment(t *testing.T) {
	t.Parallel()

	pc := assistant.PageContext{Environment: "prod"}
	intent, err := assistant.DeterministicPlanner{}.Plan(context.Background(), user(), "查看集群状态", nil, pc)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if intent.ToolName != tools.ClusterStatusRead {
		t.Fatalf("tool = %q, want %q", intent.ToolName, tools.ClusterStatusRead)
	}
	if intent.Input["environment"] != "prod" {
		t.Fatalf("input environment = %v, want prod (from PageContext)", intent.Input["environment"])
	}
}

// TestDeterministicPlannerPageContextSuppliesDiagnosticDomain verifies that
// PageContext.Domain + ResourceName are used to build a diagnostic intent when
// the message mentions health/status but no domain keyword. This fixes "用户
// 在 GlusterFS 页面问'这个 volume 健康吗'——planner 从上下文推断
// domain=glusterfs".
func TestDeterministicPlannerPageContextSuppliesDiagnosticDomain(t *testing.T) {
	t.Parallel()

	pc := assistant.PageContext{
		Environment:  "prod",
		Domain:       "glusterfs",
		ResourceType: "volume",
		ResourceName: "data",
	}
	intent, err := assistant.DeterministicPlanner{}.Plan(context.Background(), user(), "这个健康吗", nil, pc)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if intent.Diagnostic == nil {
		t.Fatal("diagnostic = nil, want a diagnostic intent built from PageContext")
	}
	if intent.Diagnostic.Domain != "glusterfs" {
		t.Fatalf("diagnostic domain = %q, want glusterfs (from PageContext)", intent.Diagnostic.Domain)
	}
	if intent.Diagnostic.Environment != "prod" {
		t.Fatalf("diagnostic environment = %q, want prod (from PageContext)", intent.Diagnostic.Environment)
	}
	if intent.Diagnostic.ResourceName != "data" {
		t.Fatalf("diagnostic resource name = %q, want data (from PageContext)", intent.Diagnostic.ResourceName)
	}
}

// TestDeterministicPlannerMessageOverridesPageContext verifies that explicit
// message tokens take precedence over PageContext. When the user says "staging
// 集群状态" while the page context is prod, the message wins.
func TestDeterministicPlannerMessageOverridesPageContext(t *testing.T) {
	t.Parallel()

	pc := assistant.PageContext{Environment: "prod"}
	intent, err := assistant.DeterministicPlanner{}.Plan(context.Background(), user(), "查看 staging 集群状态", nil, pc)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if intent.Input["environment"] != "staging" {
		t.Fatalf("input environment = %v, want staging (message overrides PageContext)", intent.Input["environment"])
	}
}

// TestDeterministicPlannerEmptyPageContextPreservesBehavior verifies backward
// compatibility: a zero-value PageContext (no fields set) produces the same
// behavior as before — the planner relies solely on the message text.
func TestDeterministicPlannerEmptyPageContextPreservesBehavior(t *testing.T) {
	t.Parallel()

	_, err := assistant.DeterministicPlanner{}.Plan(context.Background(), user(), "帮我看看", nil, assistant.PageContext{})
	if !errors.Is(err, assistant.ErrClarificationNeeded) {
		t.Fatalf("error = %v, want ErrClarificationNeeded (zero PageContext preserves old behavior)", err)
	}
}

// --- 系统态势（借鉴-1：系统态势 SLA 入口）测试 ---

// TestDeterministicPlannerSystemPostureIntent verifies that when the user asks
// about the overall system posture ("系统怎么样"/"整体健康"/"全局状态"), the
// planner routes to the QuerySystemPosture read tool instead of the generic
// ClusterStatusRead. This is the entry point that lets the operator ask "is
// the system OK?" and get a multi-domain aggregate view.
func TestDeterministicPlannerSystemPostureIntent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		message string
	}{
		{name: "系统怎么样", message: "prod 系统怎么样"},
		{name: "整体健康", message: "prod 整体健康状态"},
		{name: "全局状态", message: "查看 prod 全局状态"},
		{name: "系统态势", message: "prod 系统态势"},
		{name: "整体状态", message: "prod 整体状态如何"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			intent, err := assistant.DeterministicPlanner{}.Plan(context.Background(), user(), tt.message, nil, assistant.PageContext{})
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			if intent.ToolName != tools.QuerySystemPosture {
				t.Fatalf("tool = %q, want %q for message %q", intent.ToolName, tools.QuerySystemPosture, tt.message)
			}
			if intent.Input["environment"] != "prod" {
				t.Fatalf("input environment = %v, want prod", intent.Input["environment"])
			}
		})
	}
}

// TestDeterministicPlannerAlertQueryIntent verifies that alert keywords route
// to the AlertQuery read tool. The branch must run BEFORE the generic cluster
// status branch because "告警状态" contains "状态" which would otherwise
// trigger ClusterStatusRead.
func TestDeterministicPlannerAlertQueryIntent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		message string
	}{
		{name: "当前有哪些告警", message: "prod 当前有哪些告警"},
		{name: "活动告警", message: "prod 活动告警有哪些"},
		{name: "alert english", message: "show active alerts in prod"},
		{name: "告警状态", message: "prod 告警状态"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			intent, err := assistant.DeterministicPlanner{}.Plan(context.Background(), user(), tt.message, nil, assistant.PageContext{})
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			if intent.ToolName != tools.AlertQuery {
				t.Fatalf("tool = %q, want %q for message %q", intent.ToolName, tools.AlertQuery, tt.message)
			}
			if intent.Input["environment"] != "prod" {
				t.Fatalf("input environment = %v, want prod", intent.Input["environment"])
			}
		})
	}
}

// TestDeterministicPlannerEventQueryIntent verifies that event/audit keywords
// route to the EventQuery tool.
func TestDeterministicPlannerEventQueryIntent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		message string
	}{
		{name: "审计记录", message: "查看 prod 上周的审计记录"},
		{name: "事件", message: "prod 有哪些拒绝事件"},
		{name: "english audit", message: "show audit events in prod"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			intent, err := assistant.DeterministicPlanner{}.Plan(context.Background(), user(), tt.message, nil, assistant.PageContext{})
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			if intent.ToolName != tools.EventQuery {
				t.Fatalf("tool = %q, want %q for message %q", intent.ToolName, tools.EventQuery, tt.message)
			}
			if intent.Input["environment"] != "prod" {
				t.Fatalf("input environment = %v, want prod", intent.Input["environment"])
			}
		})
	}
}

// TestDeterministicPlannerTaskQueryIntent verifies that task/scheduled keywords
// route to the TaskQuery tool.
func TestDeterministicPlannerTaskQueryIntent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		message string
	}{
		{name: "定时任务", message: "查看 prod 有哪些定时任务"},
		{name: "巡检", message: "prod 巡检任务列表"},
		{name: "english task", message: "list scheduled tasks in prod"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			intent, err := assistant.DeterministicPlanner{}.Plan(context.Background(), user(), tt.message, nil, assistant.PageContext{})
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			if intent.ToolName != tools.TaskQuery {
				t.Fatalf("tool = %q, want %q for message %q", intent.ToolName, tools.TaskQuery, tt.message)
			}
			if intent.Input["environment"] != "prod" {
				t.Fatalf("input environment = %v, want prod", intent.Input["environment"])
			}
		})
	}
}

// TestDeterministicPlannerSystemPosturePageContextEnvironment verifies that
// SystemPosture respects PageContext.Environment when the message lacks an
// explicit environment token (e.g. "系统怎么样" without saying "prod").
func TestDeterministicPlannerSystemPosturePageContextEnvironment(t *testing.T) {
	t.Parallel()
	pc := assistant.PageContext{Environment: "prod"}
	intent, err := assistant.DeterministicPlanner{}.Plan(context.Background(), user(), "系统怎么样", nil, pc)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if intent.ToolName != tools.QuerySystemPosture {
		t.Fatalf("tool = %q, want %q", intent.ToolName, tools.QuerySystemPosture)
	}
	if intent.Input["environment"] != "prod" {
		t.Fatalf("input environment = %v, want prod (from PageContext)", intent.Input["environment"])
	}
}

// TestDeterministicPlannerTopicRetentionChinese 锁定中文写操作在生产链路
// （CapabilityAwarePlanner）上的解析结果。中间件写工具已外置为动态能力
// topic.retention.set，不再由静态层返回；该测试验证动态解析器能从中文量词
// "保留 72 小时"解析出小时数并路由到写能力。
func TestDeterministicPlannerTopicRetentionChinese(t *testing.T) {
	registerMiddlewareWriteTool(t)
	planner := assistant.NewCapabilityAwarePlanner(assistant.DeterministicPlanner{})
	intent, err := planner.Plan(context.Background(), user(), "配置 prod kafka m1 orders topic 保留 72 小时", nil, assistant.PageContext{})
	if err != nil {
		t.Fatalf("Plan returned %v", err)
	}
	if intent.ToolName != tools.TopicRetentionSet {
		t.Fatalf("tool = %q, want %q", intent.ToolName, tools.TopicRetentionSet)
	}
	if intent.Input["topic"] != "orders" {
		t.Fatalf("topic = %v, want orders", intent.Input["topic"])
	}
	if intent.Input["retention_hours"] != 72 {
		t.Fatalf("retention_hours = %v, want 72", intent.Input["retention_hours"])
	}
}
