package e2e_test

// 告警处置流程的可复用端到端测试脚本。
//
// alertE2E 把「收到告警的处置流程」的完整生产装配（真实 SQLite + SQL store、
// 真实发布能力注册成动态工具 + policy 角色、真实 ChainDiagnoser、真实
// plans.Service，仅 mock 中间件 HTTP 后端）装配一次，供按步骤拆分的各个
// E2E 测试复用。覆盖的处置链路：
//
//	webhook 接入(去重/审计) → 归并 incident → 规则命中 → 链式诊断
//	（读能力打后端）→ 写步骤建待确认处置 plan → run 记录 → 恢复关 incident。

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/alert"
	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/httpapi"
	"github.com/gracegaoya/ai-operations-copilot/internal/plans"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

const e2eWebhookSecret = "e2e-webhook-secret"

// alertE2E 是一次完整的告警处置装配。不适用 t.Parallel()：动态工具/角色
// 是包级全局注册表，测试间串行执行（与包内既有测试同一约定）。
type alertE2E struct {
	t             *testing.T
	ctx           context.Context
	backend       *httptest.Server
	hitsMu        sync.Mutex
	hits          []string
	db            *sql.DB
	alertStore    *store.SQLAlertStore
	incidentStore *store.SQLIncidentStore
	runStore      *store.SQLAlertActionRunStore
	registry      *alert.AlertActionRegistry
	planRepo      *store.MemoryActionPlanStore
	auditSvc      *audit.Service
	alertSvc      *alert.Service
	server        *httptest.Server
}

func newAlertE2E(t *testing.T) *alertE2E {
	t.Helper()
	e := &alertE2E{t: t, ctx: context.Background()}

	// mock 中间件后端：诊断读 + 处置写两个真实端点，记录命中。
	e.backend = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		e.hitsMu.Lock()
		e.hits = append(e.hits, r.Method+" "+r.URL.Path)
		e.hitsMu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/kafka/k1/consumer-groups/payments/lag":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"status":"ok","data":{"lag":42,"members":3}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/kafka/default/topics/orders/retention":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"status":"ok"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(e.backend.Close)

	// 真实 SQLite + SQL store（生产装配）。
	e.db = openAssistantSQLite(t)
	e.alertStore = store.NewSQLAlertStore(e.db)
	e.incidentStore = store.NewSQLIncidentStore(e.db)
	e.runStore = store.NewSQLAlertActionRunStore(e.db)
	ruleStore := store.NewSQLAlertActionRuleStore(e.db)
	e.alertSvc = alert.NewService(e.alertStore).
		WithCorrelation(e.incidentStore, alert.DefaultCorrelationWindow)

	// 真实能力：发布能力 → 动态工具 + policy 角色。
	tools.ResetDynamicToolsForTest()
	policy.ResetDynamicRolePermissionsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	t.Cleanup(policy.ResetDynamicRolePermissionsForTest)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "published"), 0o755); err != nil {
		t.Fatalf("mkdir published: %v", err)
	}
	readYAML := strings.ReplaceAll(alertDisposalReadCapabilityYAML(), "BASE_URL", e.backend.URL)
	if err := os.WriteFile(filepath.Join(root, "published", "kafka.consumer_group.lag.read.yaml"), []byte(readYAML), 0o644); err != nil {
		t.Fatalf("write read capability: %v", err)
	}
	writeYAML := strings.ReplaceAll(alertDisposalWriteCapabilityYAML(), "BASE_URL", e.backend.URL)
	if err := os.WriteFile(filepath.Join(root, "published", "topic.retention.set.yaml"), []byte(writeYAML), 0o644); err != nil {
		t.Fatalf("write write capability: %v", err)
	}
	loaded, err := capabilities.RegisterPublished(root)
	if err != nil {
		t.Fatalf("register published capabilities: %v", err)
	}

	// 真实读服务 + 真实域诊断。
	e.auditSvc = audit.NewService(store.NewMemoryActionPlanStore())
	readRunner := capabilities.NewCapabilityReadRunner(e2eReadRunner{}, loaded, capabilities.NewHTTPAdapter(http.DefaultClient))
	readService := execution.NewReadOnlyService(readRunner, e.auditSvc)
	diagService := diagnostics.NewService(readService, nil).
		WithCapabilityResolver(diagnostics.NewCapabilityResolver(loaded))

	// 规则注册表（真实 SQL + 热重载）。
	e.registry = alert.NewAlertActionRegistry(ruleStore).WithRunStore(e.runStore)

	// 真实 plan 服务 + 处置 plan 创建器 + 真实链式研判。
	e.planRepo = store.NewMemoryActionPlanStore()
	planService := plans.NewService(e.planRepo, nil)
	planCreator := alert.NewPlanCreator(planService, e.alertSvc)
	chainDiag := alert.NewChainDiagnoser(diagService, e.alertSvc, planCreator, readService.ExecuteRead).
		WithRunStore(e.runStore)

	// webhook 组合服务 + 真实路由 + 真实 HTTP 服务器。
	webhookSvc := httpapi.NewAlertWebhookService(e.alertSvc, e.auditSvc).
		WithChainDiagnoser(chainDiag).
		WithActions(e.registry)
	handler := httpapi.NewRouter(nil, nil,
		httpapi.WithAlertWebhook(webhookSvc),
		httpapi.WithAlertWebhookSecret(e2eWebhookSecret),
	)
	e.server = httptest.NewServer(handler)
	t.Cleanup(e.server.Close)
	return e
}

// addRule 注册一条告警→动作规则（真实 SQL 落库 + 热重载）。
func (e *alertE2E) addRule(t *testing.T, action alert.AlertAction) {
	t.Helper()
	if err := e.registry.Upsert(e.ctx, action); err != nil {
		t.Fatalf("upsert alert action rule: %v", err)
	}
}

// pushAlerts 推一批 Alertmanager 告警（HMAC 签名），返回 acknowledged 数。
func (e *alertE2E) pushAlerts(t *testing.T, alerts ...alert.AlertmanagerAlert) int {
	t.Helper()
	payload := alert.AlertmanagerPayload{Version: "4", Status: "firing", Alerts: alerts}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal alertmanager payload: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, e.server.URL+"/v1/alerts/alertmanager", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Signature", signE2EWebhook(string(raw), e2eWebhookSecret))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post webhook: %v", err)
	}
	defer resp.Body.Close()
	var parsed struct {
		Acknowledged int `json:"acknowledged"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		t.Fatalf("decode webhook response (status=%d): %v", resp.StatusCode, err)
	}
	return parsed.Acknowledged
}

// firingAlert 构造一条 firing 告警；改 Status/EndsAt 即可变 resolved。
func (e *alertE2E) firingAlert(fingerprint string, labels map[string]string) alert.AlertmanagerAlert {
	return alert.AlertmanagerAlert{
		Status:      "firing",
		Labels:      labels,
		Fingerprint: fingerprint,
		StartsAt:    time.Now().UTC().Format(time.RFC3339),
	}
}

// waitForPlans 轮询等待至少 want 条 plan 落库，超时返回当前条数。
func (e *alertE2E) waitForPlans(t *testing.T, want int) []store.PlanRecord {
	t.Helper()
	if !waitFor(t, 5*time.Second, func() bool {
		page, err := e.planRepo.ListPlans(e.ctx, store.PlanFilter{})
		if err != nil {
			t.Fatalf("list plans: %v", err)
		}
		return len(page.Plans) >= want
	}, fmt.Sprintf("%d plan(s)", want)) {
		page, _ := e.planRepo.ListPlans(e.ctx, store.PlanFilter{})
		t.Fatalf("timed out waiting for %d plans; got %d", want, len(page.Plans))
	}
	page, _ := e.planRepo.ListPlans(e.ctx, store.PlanFilter{})
	return page.Plans
}

// plans 返回当前全部 plan。
func (e *alertE2E) plans() []store.PlanRecord {
	page, _ := e.planRepo.ListPlans(e.ctx, store.PlanFilter{})
	return page.Plans
}

// alert 按外部身份（fp:<fingerprint>）取已落库告警。
func (e *alertE2E) alert(t *testing.T, externalID string) store.Alert {
	t.Helper()
	records, err := e.alertStore.Query(e.ctx, store.AlertFilter{})
	if err != nil {
		t.Fatalf("query alert: %v", err)
	}
	for _, r := range records {
		if r.ExternalID == externalID {
			return r
		}
	}
	t.Fatalf("alert %q not found", externalID)
	return store.Alert{}
}

// openIncident 查同键 firing incident（windowStart 须 UTC，与 correlation 服务一致）。
func (e *alertE2E) openIncident(t *testing.T, key store.IncidentKey) (store.AlertIncident, bool) {
	t.Helper()
	inc, found, err := e.incidentStore.FindOpenIncident(e.ctx, key, time.Now().UTC().Add(-alert.DefaultCorrelationWindow))
	if err != nil {
		t.Fatalf("find open incident: %v", err)
	}
	return inc, found
}

// members 返回 incident 的成员告警 ID。
func (e *alertE2E) members(t *testing.T, incidentID string) []string {
	t.Helper()
	members, err := e.incidentStore.MemberAlertIDs(e.ctx, incidentID)
	if err != nil {
		t.Fatalf("list incident members: %v", err)
	}
	return members
}

// backendHits 返回 mock 后端收到的请求（方法+路径）。
func (e *alertE2E) backendHits() []string {
	e.hitsMu.Lock()
	defer e.hitsMu.Unlock()
	return append([]string(nil), e.hits...)
}

// runRecords 返回某规则的历史执行记录。
func (e *alertE2E) runRecords(t *testing.T, rule string) []store.AlertActionRunRecord {
	t.Helper()
	recent, err := e.runStore.RecentByRule(e.ctx, rule, 10)
	if err != nil {
		t.Fatalf("list run records: %v", err)
	}
	return recent
}

// alertIngestedCount 返回 alert_ingested 审计事件数。
func (e *alertE2E) alertIngestedCount(t *testing.T) int {
	t.Helper()
	page, err := e.auditSvc.List(e.ctx, audit.Filter{Action: audit.ActionAlertIngested})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	return len(page.Events)
}

// waitFor 轮询直到 cond 为真或超时。
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, what string) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Logf("timed out waiting for %s", what)
	return false
}

func signE2EWebhook(body, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

func hasHit(hits []string, want string) bool {
	for _, h := range hits {
		if h == want {
			return true
		}
	}
	return false
}

// ---- 场景构件：规则 / 关联键 / 告警标签 ----

const e2eDisposalRuleName = "kafka-consumer-lag-disposal"

func kafkaDisposalRule() alert.AlertAction {
	return alert.AlertAction{
		Name:            e2eDisposalRuleName,
		Description:     "kafka 消费延迟告警：自动诊断 + 建处置 plan",
		ExecuteLastStep: false,
		AlertMatch:      alert.AlertMatch{AlertName: "KafkaConsumerGroupLag", Severity: "critical"},
		ToolSequence: []alert.AlertActionStep{
			{Tool: "kafka.consumer_group.lag.read", Input: map[string]string{"cluster": "{labels.cluster}", "group": "{labels.group}"}},
			{Tool: "topic.retention.set", Input: map[string]string{"topic": "{labels.topic}", "retention_hours": "72"}},
		},
	}
}

func kafkaIncidentKey() store.IncidentKey {
	return store.IncidentKey{Domain: "kafka", ResourceType: "consumer_group", ResourceName: "payments"}
}

func kafkaLagLabels() map[string]string {
	return map[string]string{
		"alertname":     "KafkaConsumerGroupLag",
		"severity":      "critical",
		"domain":        "kafka",
		"resource_type": "consumer_group",
		"resource_name": "payments",
		"cluster":       "k1",
		"group":         "payments",
		"topic":         "orders",
	}
}

// ---- 分步骤端到端测试 ----

// 步骤 1 接入：webhook → 归一化落库 + 幂等去重 + 审计。
func TestAlertIngestEndToEnd(t *testing.T) {
	e := newAlertE2E(t)
	if ack := e.pushAlerts(t, e.firingAlert("ingest-1", kafkaLagLabels())); ack != 1 {
		t.Fatalf("acknowledged = %d, want 1", ack)
	}
	a := e.alert(t, "fp:ingest-1")
	if a.Status != "firing" || a.Domain != "kafka" || a.ResourceType != "consumer_group" || a.ResourceName != "payments" {
		t.Errorf("alert = %+v, want normalized kafka consumer_group payments firing", a)
	}
	// 重复推送（同 fingerprint）幂等去重：acknowledged 仍 1，不新增行。
	if ack := e.pushAlerts(t, e.firingAlert("ingest-1", kafkaLagLabels())); ack != 1 {
		t.Fatalf("repush acknowledged = %d, want 1", ack)
	}
	records, err := e.alertStore.Query(e.ctx, store.AlertFilter{Domain: "kafka"})
	if err != nil {
		t.Fatalf("query alert: %v", err)
	}
	if len(records) != 1 {
		t.Errorf("alert rows = %d, want 1 (dedup)", len(records))
	}
	// 每次接入一条审计事件。
	if n := e.alertIngestedCount(t); n != 2 {
		t.Errorf("alert_ingested audit = %d, want 2", n)
	}
}

// 步骤 2 归并：同键告警归并进同一 incident，归并告警不再触发研判/建 plan。
func TestAlertCorrelateEndToEnd(t *testing.T) {
	e := newAlertE2E(t)
	e.addRule(t, kafkaDisposalRule())

	// 首条告警 → 新建 incident（首条触发链式处置）。
	if ack := e.pushAlerts(t, e.firingAlert("corr-1", kafkaLagLabels())); ack != 1 {
		t.Fatalf("acknowledged = %d, want 1", ack)
	}
	if plans := e.waitForPlans(t, 1); len(plans) != 1 {
		t.Fatalf("plans = %d, want 1", len(plans))
	}
	inc, found := e.openIncident(t, kafkaIncidentKey())
	if !found {
		t.Fatalf("incident not created")
	}
	if members := e.members(t, inc.ID); len(members) != 1 {
		t.Errorf("members = %d, want 1", len(members))
	}

	// 同键第二条（不同 fingerprint）→ 归并计数，不再新建 plan（门控）。
	if ack := e.pushAlerts(t, e.firingAlert("corr-2", kafkaLagLabels())); ack != 1 {
		t.Fatalf("acknowledged = %d, want 1", ack)
	}
	inc2, found2 := e.openIncident(t, kafkaIncidentKey())
	if !found2 {
		t.Fatalf("incident lost after merge")
	}
	if members := e.members(t, inc2.ID); len(members) != 2 {
		t.Errorf("members after merge = %d, want 2", len(members))
	}
	if inc2.AlertCount != 2 {
		t.Errorf("alert_count = %d, want 2", inc2.AlertCount)
	}
	if !waitFor(t, time.Second, func() bool { return len(e.plans()) == 1 }, "plan count stays 1 after merge") {
		t.Errorf("merge re-triggered disposal: plans = %d, want 1", len(e.plans()))
	}
}

// 步骤 3 规则命中：命中触发链式执行，未命中 / 停用规则不触发。
func TestAlertRuleMatchEndToEnd(t *testing.T) {
	e := newAlertE2E(t)
	e.addRule(t, kafkaDisposalRule())

	// 命中规则 → 诊断读步骤真实打后端。
	if ack := e.pushAlerts(t, e.firingAlert("match-1", kafkaLagLabels())); ack != 1 {
		t.Fatalf("acknowledged = %d, want 1", ack)
	}
	e.waitForPlans(t, 1)
	if !hasHit(e.backendHits(), "GET /api/kafka/k1/consumer-groups/payments/lag") {
		t.Errorf("diagnostic read did not hit backend; hits=%v", e.backendHits())
	}

	// 未命中规则（不同 alertname / severity）→ 不触发链式执行。
	miss := kafkaLagLabels()
	miss["alertname"] = "DiskFull"
	miss["severity"] = "warning"
	if ack := e.pushAlerts(t, e.firingAlert("match-2", miss)); ack != 1 {
		t.Fatalf("acknowledged = %d, want 1", ack)
	}
	if !waitFor(t, time.Second, func() bool { return len(e.plans()) == 1 }, "no extra plan for non-match") {
		t.Errorf("non-matching alert created plan: %d", len(e.plans()))
	}
	before := len(e.backendHits())
	time.Sleep(300 * time.Millisecond)
	if len(e.backendHits()) != before {
		t.Errorf("non-matching alert hit backend: %v", e.backendHits())
	}
}

// 步骤 3b 停用规则不触发。
func TestAlertRuleDisabledNotTriggeredEndToEnd(t *testing.T) {
	e := newAlertE2E(t)
	rule := kafkaDisposalRule()
	rule.Name = "disabled-rule"
	e.addRule(t, rule)
	if err := e.registry.SetEnabled(e.ctx, "disabled-rule", false); err != nil {
		t.Fatalf("set enabled: %v", err)
	}
	if ack := e.pushAlerts(t, e.firingAlert("disabled-1", kafkaLagLabels())); ack != 1 {
		t.Fatalf("acknowledged = %d, want 1", ack)
	}
	if !waitFor(t, time.Second, func() bool { return len(e.plans()) == 0 && len(e.backendHits()) == 0 }, "disabled rule stays idle") {
		t.Errorf("disabled rule triggered: plans=%d hits=%v", len(e.plans()), e.backendHits())
	}
}

// 步骤 4 链式诊断：诊断读步骤结果回写告警 description + run 记录落库。
func TestAlertChainSummaryAndRunRecordEndToEnd(t *testing.T) {
	e := newAlertE2E(t)
	e.addRule(t, kafkaDisposalRule())
	if ack := e.pushAlerts(t, e.firingAlert("chain-1", kafkaLagLabels())); ack != 1 {
		t.Fatalf("acknowledged = %d, want 1", ack)
	}
	// run 记录与 description 回写在 plan 创建之后，等 run 记录落库再断言。
	if !waitFor(t, 5*time.Second, func() bool { return len(e.runRecords(t, e2eDisposalRuleName)) >= 1 }, "run record after chain") {
		t.Fatalf("chain never recorded a run; plans=%d", len(e.plans()))
	}

	desc := e.alert(t, "fp:chain-1").Description
	if !strings.Contains(desc, "[链式研判:"+e2eDisposalRuleName+"]") {
		t.Errorf("alert description missing chain summary: %q", desc)
	}
	recent := e.runRecords(t, e2eDisposalRuleName)
	if len(recent) != 1 {
		t.Fatalf("run records = %d, want 1", len(recent))
	}
	if recent[0].Status != "success" {
		t.Errorf("run status = %q, want success (steps=%s)", recent[0].Status, recent[0].Steps)
	}
}

// 步骤 5 处置：写步骤建待确认处置 plan，输入按告警字段渲染。
func TestAlertDisposalPlanCreatedEndToEnd(t *testing.T) {
	e := newAlertE2E(t)
	e.addRule(t, kafkaDisposalRule())
	if ack := e.pushAlerts(t, e.firingAlert("plan-1", kafkaLagLabels())); ack != 1 {
		t.Fatalf("acknowledged = %d, want 1", ack)
	}
	plans := e.waitForPlans(t, 1)
	p := plans[0]
	if p.Status != store.PlanPendingConfirmation {
		t.Errorf("plan status = %q, want pending_confirmation", p.Status)
	}
	if p.ToolName != "topic.retention.set" {
		t.Errorf("plan tool = %q, want topic.retention.set", p.ToolName)
	}
	input := string(p.InputJSON)
	for _, want := range []string{`"topic":"orders"`, `"retention_hours":72`} {
		if !strings.Contains(input, want) {
			t.Errorf("plan input = %s, want to contain %s", input, want)
		}
	}
}

// 步骤 5b 处置 fail-closed：ExecuteLastStep=true 的写步骤链无执行器，明确失败。
func TestAlertDisposalWriteAutoExecuteFailClosed(t *testing.T) {
	e := newAlertE2E(t)
	rule := kafkaDisposalRule()
	rule.ExecuteLastStep = true
	e.addRule(t, rule)
	if ack := e.pushAlerts(t, e.firingAlert("auto-1", kafkaLagLabels())); ack != 1 {
		t.Fatalf("acknowledged = %d, want 1", ack)
	}
	// 等链执行完（run 记录落库），再断言 fail-closed 结果。
	if !waitFor(t, 3*time.Second, func() bool { return len(e.runRecords(t, e2eDisposalRuleName)) >= 1 }, "run record for fail-closed chain") {
		t.Fatalf("chain never ran; plans=%d hits=%v", len(e.plans()), e.backendHits())
	}
	if got := len(e.plans()); got != 0 {
		t.Errorf("auto-execute write created plan: %d", got)
	}
	for _, h := range e.backendHits() {
		if strings.Contains(h, "/retention") {
			t.Errorf("write endpoint was hit despite fail-closed: %v", h)
		}
	}
	recent := e.runRecords(t, e2eDisposalRuleName)
	if len(recent) != 1 {
		t.Fatalf("run records = %d, want 1", len(recent))
	}
	if recent[0].Status != "failure" {
		t.Errorf("run status = %q, want failure", recent[0].Status)
	}
}

// 步骤 6 恢复：resolved 告警走 Ingest 传播，全部成员恢复后关闭 incident。
func TestAlertResolveClosesIncidentEndToEnd(t *testing.T) {
	e := newAlertE2E(t)
	e.addRule(t, kafkaDisposalRule())
	if ack := e.pushAlerts(t, e.firingAlert("res-1", kafkaLagLabels())); ack != 1 {
		t.Fatalf("acknowledged = %d, want 1", ack)
	}
	e.waitForPlans(t, 1)
	if _, found := e.openIncident(t, kafkaIncidentKey()); !found {
		t.Fatalf("incident not created")
	}

	resolved := e.firingAlert("res-1", kafkaLagLabels())
	resolved.Status = "resolved"
	resolved.EndsAt = time.Now().UTC().Format(time.RFC3339)
	if ack := e.pushAlerts(t, resolved); ack != 1 {
		t.Fatalf("resolved acknowledged = %d, want 1", ack)
	}
	if !waitFor(t, 3*time.Second, func() bool {
		_, found := e.openIncident(t, kafkaIncidentKey())
		return !found
	}, "incident closes after resolve") {
		t.Errorf("incident still firing after resolve")
	}
}

func alertDisposalReadCapabilityYAML() string {
	return `schema_version: 1
name: kafka.consumer_group.lag.read
status: published
domain: kafka
resource_type: consumer_group
operation: read
risk: low
backend:
  adapter: http
  method: GET
  path: /api/kafka/{cluster}/consumer-groups/{group}/lag
  timeout_ms: 3000
  base_url: BASE_URL
input_schema:
    cluster:
      type: string
      required: true
    group:
      type: string
      required: true
output:
  kind: observation
  severity_path: $.status
  summary_template: Consumer group {group} lag is {lag}
  fields:
   lag: $.data.lag
   members: $.data.members
governance:
  requires_action_plan: false
  requires_approval: false
  precheck_tools: []
  rollback:
    strategy: ""
    source: ""
auth:
  roles:
  - viewer
  - operator
  - admin
ai:
  description: Read Kafka consumer group lag from the demo middleware backend.
`
}

func alertDisposalWriteCapabilityYAML() string {
	return `schema_version: 1
name: topic.retention.set
status: published
domain: kafka
resource_type: topic
operation: write
risk: medium
backend:
  adapter: http
  method: POST
  path: /api/kafka/default/topics/{topic}/retention
  timeout_ms: 5000
  base_url: BASE_URL
input_schema:
    topic:
      type: string
      required: true
    retention_hours:
      type: integer
      required: true
      min: 1
      max: 8760
output:
  kind: confirmation
  summary_template: Retention for topic {topic} set to {retention_hours}h
  fields: {}
governance:
  requires_action_plan: true
  requires_approval: true
  precheck_tools:
  - kafka.topic.retention.read
  rollback:
    strategy: reset_to_previous
    source: previous_value
auth:
  roles:
  - operator
  - admin
ai:
  description: Set Kafka topic retention hours via the demo middleware backend.
`
}
