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

func TestDeterministicPlannerParsesTopicRetentionWrite(t *testing.T) {
	t.Parallel()

	intent, err := assistant.DeterministicPlanner{}.Plan(context.Background(), user(), "把 prod 的 orders topic retention 改成 72 小时", nil, assistant.PageContext{})
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
	t.Parallel()

	intent, err := assistant.DeterministicPlanner{}.Plan(context.Background(), user(), "把 prod 的 orders topic retention 改成 72 小时", nil, assistant.PageContext{})
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

// TestDeterministicPlannerTopicRetentionChinese 锁定中文写操作在**静态**层的
// 解析结果。生产链路上 CapabilityAwarePlanner 会把它改派给同域动态能力
// （kafka.topic.retention.write，见 intent_routing_test.go），但那条改派的前提
// 是这一层先把 topic 和小时数解析出来——中文量词"保留 72 小时"一旦解析失败，
// 这里会退化成澄清，上层也就没有可改派的意图了。
func TestDeterministicPlannerTopicRetentionChinese(t *testing.T) {
	det := assistant.DeterministicPlanner{}
	intent, err := det.Plan(context.Background(), identity.CurrentUser{}, "配置 prod kafka m1 orders topic 保留 72 小时", nil, assistant.PageContext{})
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
