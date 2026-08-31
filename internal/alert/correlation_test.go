package alert

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

func newCorrelationService(t *testing.T) (*Service, *store.MemoryAlertStore, *store.MemoryIncidentStore) {
	t.Helper()
	alertStore := store.NewMemoryAlertStore().WithClock(fixedNow)
	incidents := store.NewMemoryIncidentStore().WithClock(fixedNow)
	svc := NewService(alertStore).WithCorrelation(incidents, 30*time.Minute).WithClock(fixedNow)
	return svc, alertStore, incidents
}

func correlatedPayload(externalID, severity string) WebhookPayload {
	p := validPayload()
	p.ExternalID = externalID
	p.Severity = severity
	p.Status = "firing"
	return p
}

func TestCorrelateFirstAlertCreatesIncident(t *testing.T) {
	t.Parallel()
	svc, _, incidents := newCorrelationService(t)
	result, err := svc.Ingest(context.Background(), correlatedPayload("a1", "warning"))
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if result.Incident == nil || !result.IncidentCreated {
		t.Fatalf("first alert should create incident, got %+v", result.Incident)
	}
	if result.Incident.Status != "firing" || result.Incident.AlertCount != 1 {
		t.Errorf("incident = %+v, want firing count=1", result.Incident)
	}
	if result.SeverityEscalated {
		t.Error("SeverityEscalated should be false for new incident")
	}
	members, err := incidents.MemberAlertIDs(context.Background(), result.Incident.ID)
	if err != nil || len(members) != 1 {
		t.Fatalf("members = %v (err %v), want 1", members, err)
	}
}

func TestCorrelateIdempotentRepushDoesNotRecount(t *testing.T) {
	t.Parallel()
	svc, _, _ := newCorrelationService(t)
	ctx := context.Background()
	first, err := svc.Ingest(ctx, correlatedPayload("a1", "warning"))
	if err != nil {
		t.Fatalf("first Ingest: %v", err)
	}
	// 同一告警（同 source+external_id）窗口内重推多次：不重复计数、不升级。
	for i := 0; i < 3; i++ {
		repush, err := svc.Ingest(ctx, correlatedPayload("a1", "warning"))
		if err != nil {
			t.Fatalf("repush %d: %v", i, err)
		}
		if repush.Incident == nil || repush.Incident.ID != first.Incident.ID {
			t.Fatalf("repush %d should merge into incident %s, got %+v", i, first.Incident.ID, repush.Incident)
		}
		if repush.IncidentCreated {
			t.Errorf("repush %d: IncidentCreated = true, want false", i)
		}
		if repush.SeverityEscalated {
			t.Errorf("repush %d: SeverityEscalated = true, want false", i)
		}
		if repush.Incident.AlertCount != 1 {
			t.Errorf("repush %d: AlertCount = %d, want 1", i, repush.Incident.AlertCount)
		}
	}
}

func TestCorrelateSecondAlertMergesAndCounts(t *testing.T) {
	t.Parallel()
	svc, _, _ := newCorrelationService(t)
	ctx := context.Background()
	if _, err := svc.Ingest(ctx, correlatedPayload("a1", "warning")); err != nil {
		t.Fatalf("first Ingest: %v", err)
	}
	second, err := svc.Ingest(ctx, correlatedPayload("a2", "warning"))
	if err != nil {
		t.Fatalf("second Ingest: %v", err)
	}
	if second.IncidentCreated {
		t.Error("same-key alert should merge, not create")
	}
	if second.Incident.AlertCount != 2 {
		t.Errorf("AlertCount = %d, want 2", second.Incident.AlertCount)
	}
	// 归并告警应被降噪门控抑制（shouldDiagnose 的服务端判定依据）。
	shouldDiagnose := second.Incident == nil || second.IncidentCreated || second.SeverityEscalated
	if shouldDiagnose {
		t.Error("merged non-escalated alert should be suppressed from diagnosis")
	}
}

func TestCorrelateSeverityEscalationTriggersRediagnose(t *testing.T) {
	t.Parallel()
	svc, _, _ := newCorrelationService(t)
	ctx := context.Background()
	if _, err := svc.Ingest(ctx, correlatedPayload("a1", "warning")); err != nil {
		t.Fatalf("first Ingest: %v", err)
	}
	escalated, err := svc.Ingest(ctx, correlatedPayload("a2", "critical"))
	if err != nil {
		t.Fatalf("escalating Ingest: %v", err)
	}
	if !escalated.SeverityEscalated {
		t.Error("SeverityEscalated = false, want true for warning→critical")
	}
	if escalated.Incident.Severity != "critical" {
		t.Errorf("incident severity = %q, want critical", escalated.Incident.Severity)
	}
	shouldDiagnose := escalated.Incident == nil || escalated.IncidentCreated || escalated.SeverityEscalated
	if !shouldDiagnose {
		t.Error("escalated alert should re-trigger diagnosis")
	}
	// 升级不重复计数（a2 是新告警 +1，但幂等重推 critical 不再 +1）。
	repush, err := svc.Ingest(ctx, correlatedPayload("a2", "critical"))
	if err != nil {
		t.Fatalf("repush: %v", err)
	}
	if repush.Incident.AlertCount != 2 {
		t.Errorf("AlertCount after repush = %d, want 2", repush.Incident.AlertCount)
	}
}

func TestCorrelateNoKeyAlertsSkipCorrelation(t *testing.T) {
	t.Parallel()
	svc, _, incidents := newCorrelationService(t)
	p := correlatedPayload("a1", "warning")
	p.Domain = "" // 无域标无法归并
	result, err := svc.Ingest(context.Background(), p)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if result.Incident != nil {
		t.Errorf("keyless alert should not correlate, got %+v", result.Incident)
	}
	all, err := incidents.ListIncidents(context.Background(), store.IncidentFilter{})
	if err != nil || len(all) != 0 {
		t.Fatalf("incidents = %v (err %v), want none", all, err)
	}
}

func TestResolvedAlertClosesIncident(t *testing.T) {
	t.Parallel()
	svc, _, incidents := newCorrelationService(t)
	ctx := context.Background()
	if _, err := svc.Ingest(ctx, correlatedPayload("a1", "warning")); err != nil {
		t.Fatalf("firing Ingest: %v", err)
	}
	second, err := svc.Ingest(ctx, correlatedPayload("a2", "warning"))
	if err != nil {
		t.Fatalf("second Ingest: %v", err)
	}
	incidentID := second.Incident.ID

	// 第一条恢复：incident 还有成员在响，保持 firing。
	r1 := correlatedPayload("a1", "warning")
	r1.Status = "resolved"
	if _, err := svc.Ingest(ctx, r1); err != nil {
		t.Fatalf("resolve a1: %v", err)
	}
	inc, err := incidents.GetIncident(ctx, incidentID)
	if err != nil {
		t.Fatalf("GetIncident: %v", err)
	}
	if inc.Status != "firing" {
		t.Errorf("status = %q after a1 resolved, want firing (a2 still firing)", inc.Status)
	}

	// 第二条恢复：全部成员恢复，incident 关闭。
	r2 := correlatedPayload("a2", "warning")
	r2.Status = "resolved"
	res, err := svc.Ingest(ctx, r2)
	if err != nil {
		t.Fatalf("resolve a2: %v", err)
	}
	if res.Incident != nil {
		t.Errorf("resolved ingest should not correlate, got %+v", res.Incident)
	}
	inc, err = incidents.GetIncident(ctx, incidentID)
	if err != nil {
		t.Fatalf("GetIncident: %v", err)
	}
	if inc.Status != "resolved" {
		t.Errorf("status = %q after all members resolved, want resolved", inc.Status)
	}
}

func TestResolvedAlertDoesNotJoinOrCreateIncident(t *testing.T) {
	t.Parallel()
	svc, _, incidents := newCorrelationService(t)
	ctx := context.Background()
	// 恢复告警是该键首条：不得新建 firing incident。
	resolved := correlatedPayload("a1", "warning")
	resolved.Status = "resolved"
	if _, err := svc.Ingest(ctx, resolved); err != nil {
		t.Fatalf("resolved Ingest: %v", err)
	}
	all, err := incidents.ListIncidents(context.Background(), store.IncidentFilter{})
	if err != nil || len(all) != 0 {
		t.Fatalf("incidents = %v (err %v), want none for first-alert-resolved", all, err)
	}
}

func TestCorrelationWindowExpiryStartsNewIncident(t *testing.T) {
	t.Parallel()
	alertStore := store.NewMemoryAlertStore().WithClock(fixedNow)
	incidents := store.NewMemoryIncidentStore().WithClock(fixedNow)
	now := fixedNow()
	svc := NewService(alertStore).WithCorrelation(incidents, 30*time.Minute).
		WithClock(func() time.Time { return now })
	ctx := context.Background()
	if _, err := svc.Ingest(ctx, correlatedPayload("a1", "warning")); err != nil {
		t.Fatalf("first Ingest: %v", err)
	}
	now = now.Add(45 * time.Minute) // 窗口（30m）已过期
	late, err := svc.Ingest(ctx, correlatedPayload("a2", "warning"))
	if err != nil {
		t.Fatalf("late Ingest: %v", err)
	}
	if !late.IncidentCreated {
		t.Error("alert outside window should create a new incident")
	}
}

func TestCorrelateDisabledFailOpen(t *testing.T) {
	t.Parallel()
	// 未启用关联时 IngestResult.Incident 为 nil，研判门控放行。
	svc, _ := newTestService()
	result, err := svc.Ingest(context.Background(), validPayload())
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	if result.Incident != nil || result.IncidentCreated || result.SeverityEscalated {
		t.Errorf("correlation disabled: got %+v", result)
	}
	shouldDiagnose := result.Incident == nil || result.IncidentCreated || result.SeverityEscalated
	if !shouldDiagnose {
		t.Error("fail-open: alert without correlation must still diagnose")
	}
}

func TestSeverityRankUnknownIsBelowInfo(t *testing.T) {
	t.Parallel()
	// 归一化保证 info|warning|critical；未识别值不越过已识别值。
	if severityRank("warn") >= severityRank("info") {
		t.Error("unknown severity should rank below info")
	}
	if !(severityRank("info") < severityRank("warning") && severityRank("warning") < severityRank("critical")) {
		t.Error("severity order info < warning < critical violated")
	}
}

// resolveErrorStore 包一层 MemoryIncidentStore，可注入反查失败。
type resolveErrorStore struct {
	store.IncidentStore
	failFind bool
}

func (s *resolveErrorStore) FindOpenIncidentByAlert(ctx context.Context, alertID string) (store.AlertIncident, bool, error) {
	if s.failFind {
		return store.AlertIncident{}, false, errors.New("injected find failure")
	}
	return s.IncidentStore.FindOpenIncidentByAlert(ctx, alertID)
}

func TestPropagateResolveFindErrorKeepsIncidentFiring(t *testing.T) {
	t.Parallel()
	alertStore := store.NewMemoryAlertStore().WithClock(fixedNow)
	inner := store.NewMemoryIncidentStore().WithClock(fixedNow)
	incidents := &resolveErrorStore{IncidentStore: inner}
	svc := NewService(alertStore).WithCorrelation(incidents, 30*time.Minute).WithClock(fixedNow)
	ctx := context.Background()
	first, err := svc.Ingest(ctx, correlatedPayload("a1", "warning"))
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	incidentID := first.Incident.ID
	// 打开反查失败注入后恢复：传播失败必须 fail-closed（incident 保持
	// firing），且不阻断告警接入本身。
	incidents.failFind = true
	resolved := correlatedPayload("a1", "warning")
	resolved.Status = "resolved"
	result, err := svc.Ingest(ctx, resolved)
	if err != nil {
		t.Fatalf("resolved Ingest (find fails): %v", err)
	}
	if result.Alert.Status != StatusResolved {
		t.Errorf("alert status = %q, want resolved (resolve must not fail)", result.Alert.Status)
	}
	inc, err := inner.GetIncident(ctx, incidentID)
	if err != nil {
		t.Fatalf("GetIncident: %v", err)
	}
	if inc.Status != "firing" {
		t.Errorf("incident status = %q under find failure, want firing", inc.Status)
	}
}

// memberFailureAlertStore 在查询指定成员告警时注入瞬时错误，
// 验证 propagateResolve 的错误极性（查询失败 ≠ 已恢复）。
type memberFailureAlertStore struct {
	store.AlertStore
	failIDs map[string]bool
}

func (s *memberFailureAlertStore) Get(ctx context.Context, id string) (store.Alert, error) {
	if s.failIDs[id] {
		return store.Alert{}, errors.New("injected member get failure")
	}
	return s.AlertStore.Get(ctx, id)
}

func TestPropagateResolveMemberLookupErrorKeepsFiring(t *testing.T) {
	t.Parallel()
	alertStore := store.NewMemoryAlertStore().WithClock(fixedNow)
	inner := store.NewMemoryIncidentStore().WithClock(fixedNow)
	svc := NewService(alertStore).WithCorrelation(inner, 30*time.Minute).WithClock(fixedNow)
	ctx := context.Background()
	if _, err := svc.Ingest(ctx, correlatedPayload("a1", "warning")); err != nil {
		t.Fatalf("first Ingest: %v", err)
	}
	second, err := svc.Ingest(ctx, correlatedPayload("a2", "warning"))
	if err != nil {
		t.Fatalf("second Ingest: %v", err)
	}
	a2ID := second.Alert.ID

	// 换上注入层后恢复 a2：其成员 Get 失败，应视为"仍在响"（fail-closed）。
	svc.store = &memberFailureAlertStore{AlertStore: alertStore, failIDs: map[string]bool{a2ID: true}}
	resolved := correlatedPayload("a2", "warning")
	resolved.Status = "resolved"
	if _, err := svc.Ingest(ctx, resolved); err != nil {
		t.Fatalf("resolve a2 with member failure: %v", err)
	}
	inc, _, err := inner.FindOpenIncident(ctx, store.IncidentKey{Domain: "kafka"}, fixedNow().Add(-time.Minute))
	if err != nil {
		t.Fatalf("FindOpenIncident: %v", err)
	}
	if inc.ID == "" {
		t.Fatal("incident not found")
	}
	if inc.Status != "firing" {
		t.Errorf("incident status = %q under member-get failure, want firing (fail-closed)", inc.Status)
	}
}

func TestCorrelateConcurrentSameKeySingleIncident(t *testing.T) {
	t.Parallel()
	svc, _, inner := newCorrelationService(t)
	ctx := context.Background()
	const n = 20
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			_, err := svc.Ingest(ctx, correlatedPayload(string(rune('a'+i)), "warning"))
			errs <- err
		}(i)
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("concurrent Ingest %d: %v", i, err)
		}
	}
	firing, err := inner.ListIncidents(ctx, store.IncidentFilter{Status: "firing"})
	if err != nil {
		t.Fatalf("ListIncidents: %v", err)
	}
	if len(firing) != 1 {
		t.Errorf("concurrent same-key ingests created %d incidents, want 1", len(firing))
	}
	if firing[0].AlertCount != n {
		t.Errorf("AlertCount = %d, want %d", firing[0].AlertCount, n)
	}
}
