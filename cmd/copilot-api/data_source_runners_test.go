package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/alert"
	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/mcp"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// --- postureReadRunner -------------------------------------------------------

// fakeProbeSource 是可控的巡检历史源。
type fakeProbeSource struct {
	rows []map[string]any
	err  error
}

func (f fakeProbeSource) RecentProbes(_ context.Context, limit int) ([]map[string]any, error) {
	if f.err != nil {
		return nil, f.err
	}
	if limit < len(f.rows) {
		return f.rows[:limit], nil
	}
	return f.rows, nil
}

func postureTestAlerts(t *testing.T, alerts ...alert.WebhookPayload) *alert.Service {
	t.Helper()
	svc := alert.NewService(store.NewMemoryAlertStore())
	for _, p := range alerts {
		if _, err := svc.Ingest(t.Context(), p); err != nil {
			t.Fatalf("ingest alert: %v", err)
		}
	}
	return svc
}

func TestPostureReadRunnerAggregatesRealSignals(t *testing.T) {
	svc := postureTestAlerts(t,
		alert.WebhookPayload{Source: "am", ExternalID: "a1", Title: "Kafka 消费积压", Severity: "critical", Status: "firing", Domain: "kafka", ResourceName: "group-1"},
		alert.WebhookPayload{Source: "am", ExternalID: "a2", Title: "MinIO 容量高", Severity: "warning", Status: "firing", Domain: "minio", ResourceName: "bucket-a"},
		alert.WebhookPayload{Source: "am", ExternalID: "a3", Title: "历史告警", Severity: "critical", Status: "resolved", Domain: "kafka", ResourceName: "group-2"},
	)
	probes := fakeProbeSource{rows: []map[string]any{
		{"title": "巡检: moonlightbox", "domain": "moonlightbox", "findings": "巡检 moonlightbox: ok (status_code=200)", "created_at": "2026-08-29 10:00:00"},
		{"title": "巡检: gateway", "domain": "gateway", "findings": "巡检 gateway: error (connection refused)", "created_at": "2026-08-29 10:00:00"},
	}}
	runner := postureReadRunner{
		alerts: svc,
		probes: probes,
		capabilities: []capabilities.Capability{
			{Name: "kafka.lag.read", Status: capabilities.StatusPublished, Operation: tools.Read, Domain: "kafka"},
			{Name: "topic.retention.set", Status: capabilities.StatusPublished, Operation: tools.Write, Domain: "kafka"}, // 写不计入
		},
	}

	tool, _ := tools.Lookup(tools.QuerySystemPosture)
	got, err := runner.Read(t.Context(), tool, nil)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got["overall_status"] != "critical" {
		t.Fatalf("overall_status = %v, want critical (1 firing critical alert)", got["overall_status"])
	}
	alertsMap := got["alerts"].(map[string]any)
	if alertsMap["firing_total"] != 2 {
		t.Fatalf("firing_total = %v, want 2 (resolved must not count)", alertsMap["firing_total"])
	}
	probesMap := got["probes"].(map[string]any)
	if probesMap["errors"] != 1 {
		t.Fatalf("probe errors = %v, want 1", probesMap["errors"])
	}
	capsMap := got["capabilities"].(map[string]any)
	if capsMap["read_published"] != 1 {
		t.Fatalf("read_published = %v, want 1 (write capability excluded)", capsMap["read_published"])
	}
}

func TestPostureReadRunnerStatusLadder(t *testing.T) {
	// 无数据 → unknown（不伪造健康）。
	runner := postureReadRunner{}
	tool, _ := tools.Lookup(tools.ClusterStatusRead)
	got, err := runner.Read(t.Context(), tool, nil)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got["status"] != "unknown" || got["overall_status"] != "unknown" {
		t.Fatalf("empty sources must be unknown, got %v", got)
	}

	// 全正常信号 → healthy；warning 告警 → warning；探活失败 → warning。
	svcHealthy := postureTestAlerts(t)
	runner = postureReadRunner{alerts: svcHealthy, probes: fakeProbeSource{rows: []map[string]any{
		{"title": "巡检: api", "findings": "巡检 api: ok", "created_at": "t"},
	}}}
	got, err = runner.Read(t.Context(), tool, nil)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got["status"] != "healthy" {
		t.Fatalf("healthy signals = %v, want healthy", got["status"])
	}

	svcWarn := postureTestAlerts(t, alert.WebhookPayload{Source: "am", ExternalID: "w1", Title: "warn", Severity: "warning", Status: "firing"})
	runner = postureReadRunner{alerts: svcWarn}
	got, err = runner.Read(t.Context(), tool, nil)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got["status"] != "warning" {
		t.Fatalf("warning alert → %v, want warning", got["status"])
	}
}

// --- promReadRunner ----------------------------------------------------------

func TestPromReadRunnerQueriesPromQL(t *testing.T) {
	var receivedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			http.NotFound(w, r)
			return
		}
		receivedQuery = r.URL.Query().Get("query")
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[
			{"metric":{"__name__":"up","job":"kafka"},"value":[1756440000,"1"]},
			{"metric":{"__name__":"up","job":"minio"},"value":[1756440000,"0"]}
		]}}`))
	}))
	t.Cleanup(srv.Close)

	runner := newPromReadRunner(srv.URL)
	tool, _ := tools.Lookup(tools.PrometheusQuery)
	got, err := runner.Read(t.Context(), tool, map[string]any{"query": `up{job="kafka"}`})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if receivedQuery != `up{job="kafka"}` {
		t.Fatalf("prometheus received query %q", receivedQuery)
	}
	if got["result_type"] != "vector" || got["count"] != 2 {
		t.Fatalf("result = %v", got)
	}
	series := got["series"].([]map[string]any)
	if series[0]["value"] != "1" {
		t.Fatalf("series value = %v, want \"1\"", series[0]["value"])
	}

	// 未配置 → 如实 unconfigured，不报错不伪造。
	empty := newPromReadRunner("")
	got, err = empty.Read(t.Context(), tool, map[string]any{"query": "up"})
	if err != nil {
		t.Fatalf("unconfigured Read: %v", err)
	}
	if got["status"] != "unconfigured" {
		t.Fatalf("unconfigured status = %v", got["status"])
	}

	// 缺 query → 报错。
	if _, err := runner.Read(t.Context(), tool, nil); err == nil {
		t.Fatalf("missing query: want error")
	}
}

// --- mcpReadRunner -----------------------------------------------------------

func TestMCPReadRouterPassesThroughNonMCPTools(t *testing.T) {
	// manager 为 nil 时必须原样透传，不能吞掉 alert.query 等静态工具。
	inner := &fakeInnerRunner{result: map[string]any{"ok": true}}
	router := mcpReadRunner{manager: nil, inner: inner}
	tool, _ := tools.Lookup(tools.AlertQuery)
	if _, err := router.Read(t.Context(), tool, nil); err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !inner.called {
		t.Fatalf("inner runner was not called for non-MCP tool")
	}
}

type fakeInnerRunner struct {
	result map[string]any
	called bool
}

func (f *fakeInnerRunner) Read(_ context.Context, _ tools.Tool, _ map[string]any) (map[string]any, error) {
	f.called = true
	return f.result, nil
}

// --- staticReadTool ----------------------------------------------------------

func TestStaticReadToolPolicyAndValidation(t *testing.T) {
	runner := postureReadRunner{alerts: postureTestAlerts(t)}
	// 用 posture runner 当底层（alert.query 不由它执行，但校验/policy 在 runner 之前）。
	denied := &staticReadTool{spec: staticAgentToolSpec{Name: tools.AlertQuery, Desc: "x"}, runner: runner}

	// 无身份（空角色）→ policy fail-closed 拒绝。
	if _, err := denied.InvokableRun(t.Context(), "{}"); err == nil || !strings.Contains(err.Error(), "policy denied") {
		t.Fatalf("empty user must be denied by policy, got %v", err)
	}

	// 非法参数（severity 越界）→ registry 校验拒绝。
	tool := &staticReadTool{spec: staticAgentToolSpec{Name: tools.AlertQuery, Desc: "x"}, runner: runner}
	ctx := assistant.WithToolUser(t.Context(), identity.CurrentUser{Subject: "op", Roles: []string{"viewer"}})
	if _, err := tool.InvokableRun(ctx, `{"severity":"banana"}`); err == nil || !strings.Contains(err.Error(), "invalid arguments") {
		t.Fatalf("invalid severity must be rejected by ValidateInput, got %v", err)
	}

	// 合法调用 → 走真实 runner，返回 JSON data。
	alertTool := &staticReadTool{
		spec: staticAgentToolSpec{
			Name: tools.AlertQuery, Desc: "x",
			Params: map[string]staticToolParam{"severity": {Type: "string", Enum: []string{"info", "warning", "critical"}}},
		},
		runner: alertReadRunner{svc: postureTestAlerts(t, alert.WebhookPayload{
			Source: "am", ExternalID: "z1", Title: "t1", Severity: "critical", Status: "firing",
		})},
	}
	out, err := alertTool.InvokableRun(ctx, `{"severity":"critical"}`)
	if err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	var parsed struct {
		Tool string         `json:"tool"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("output not json: %v (%s)", err, out)
	}
	if parsed.Tool != tools.AlertQuery || parsed.Data["count"] != float64(1) {
		t.Fatalf("parsed = %+v", parsed)
	}
}

func TestStaticAgentToolSpecsAreRegisteredAndValid(t *testing.T) {
	for _, spec := range staticAgentToolSpecs {
		if _, ok := tools.Lookup(spec.Name); !ok {
			t.Errorf("spec %q is not in the tool registry", spec.Name)
		}
	}
}

// 编译期断言：真实 MCP Manager 的方法签名满足路由假设。
var _ interface {
	OwnsTool(string) bool
} = (*mcp.Manager)(nil)

// TestStaticReadToolAuditRecordLands 回归：修复前静态工具（及 CapabilityTool）
// 用未注册的 action 字面量 "tool_executed" 记审计，audit.Record 枚举校验拒绝、
// 调用方 `_ =` 吞错——主执行路径的工具执行从未落过审计。此测试锁死：合法调用
// 必须产出一条可查的 ActionToolExecuted 事件。
func TestStaticReadToolAuditRecordLands(t *testing.T) {
	repo := store.NewMemoryActionPlanStore()
	auditSvc := audit.NewService(repo)
	alertTool := &staticReadTool{
		spec: staticAgentToolSpec{
			Name: tools.AlertQuery, Desc: "x",
			Params: map[string]staticToolParam{"severity": {Type: "string", Enum: []string{"info", "warning", "critical"}}},
		},
		runner: alertReadRunner{svc: postureTestAlerts(t, alert.WebhookPayload{
			Source: "am", ExternalID: "audit-1", Title: "t", Severity: "warning", Status: "firing",
		})},
		audit: auditSvc,
	}
	ctx := assistant.WithToolUser(t.Context(), identity.CurrentUser{Subject: "op-audit", Roles: []string{"admin"}})
	if _, err := alertTool.InvokableRun(ctx, `{"severity":"warning"}`); err != nil {
		t.Fatalf("InvokableRun: %v", err)
	}
	page, err := auditSvc.List(context.Background(), audit.Filter{Limit: 50})
	if err != nil {
		t.Fatalf("list audit events: %v", err)
	}
	found := false
	for _, e := range page.Events {
		if e.Action == audit.ActionToolExecuted && e.ToolName == tools.AlertQuery && e.Subject == "op-audit" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no %s audit event for %s landed; executor-path tool audit is silently dropped", audit.ActionToolExecuted, tools.AlertQuery)
	}
}
