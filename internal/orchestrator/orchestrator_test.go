package orchestrator_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/orchestrator"
)

// fakeRunner 是测试用的诊断 runner，按 domain 返回预设的 Package。
type fakeRunner struct {
	mu       sync.Mutex
	calls    []string // 记录调用的 domain
	packages map[string]diagnostics.Package
	errs     map[string]error
	delay    time.Duration
}

func (f *fakeRunner) Run(_ context.Context, _ identity.CurrentUser, request diagnostics.Request) (diagnostics.Package, error) {
	f.mu.Lock()
	f.calls = append(f.calls, request.Domain)
	f.mu.Unlock()
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if err, ok := f.errs[request.Domain]; ok {
		return diagnostics.Package{}, err
	}
	pkg, ok := f.packages[request.Domain]
	if !ok {
		return diagnostics.Package{}, errors.New("unknown domain: " + request.Domain)
	}
	return pkg, nil
}

func samplePackage(domain, env string) diagnostics.Package {
	return diagnostics.Package{
		ID:          "diag-" + domain,
		Environment: env,
		Domains:     []string{domain},
		Resources:   []diagnostics.ResourceRef{{Domain: domain, Type: "resource", ID: domain + ":resource:" + domain, Name: domain, Environment: env}},
		Observations: []diagnostics.Observation{
			{ID: "obs-" + domain, ResourceID: domain + ":resource:" + domain, Kind: domain + ".read", Severity: diagnostics.SeverityOK, Summary: domain + " healthy"},
		},
		Findings: []diagnostics.Finding{
			{ID: "finding-" + domain, Severity: diagnostics.SeverityOK, Summary: domain + " ok", EvidenceIDs: []string{"obs-" + domain}, Confidence: diagnostics.ConfidenceMedium},
		},
		Recommendations: []diagnostics.Recommendation{
			{ID: "rec-" + domain, Summary: "no action needed for " + domain},
		},
		CreatedAt: time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC),
	}
}

func testUser() identity.CurrentUser {
	return identity.CurrentUser{Subject: "ops-alice", Roles: []string{"admin"}, AllowedEnvironments: []string{"prod"}}
}

// TestOrchestrateConcurrentMultiDomain verifies that Orchestrate runs multiple
// diagnostic sub-requests concurrently and merges the results into a single
// package.
func TestOrchestrateConcurrentMultiDomain(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{
		packages: map[string]diagnostics.Package{
			"kafka":   samplePackage("kafka", "prod"),
			"minio":   samplePackage("minio", "prod"),
			"gluster": samplePackage("gluster", "prod"),
		},
	}
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	orch := orchestrator.New(runner, 3, func() time.Time { return now })

	requests := []diagnostics.Request{
		{Domain: "kafka", Environment: "prod", Runbook: "health"},
		{Domain: "minio", Environment: "prod", Runbook: "health"},
		{Domain: "gluster", Environment: "prod", Runbook: "health"},
	}

	pkg, err := orch.Orchestrate(context.Background(), testUser(), requests)
	if err != nil {
		t.Fatalf("Orchestrate: %v", err)
	}

	// All 3 sub-requests were executed.
	runner.mu.Lock()
	if len(runner.calls) != 3 {
		t.Fatalf("runner calls = %d, want 3", len(runner.calls))
	}
	runner.mu.Unlock()

	// Merged package contains all domains.
	if len(pkg.Domains) != 3 {
		t.Fatalf("Domains len = %d, want 3", len(pkg.Domains))
	}
	if len(pkg.Resources) != 3 {
		t.Fatalf("Resources len = %d, want 3", len(pkg.Resources))
	}
	if len(pkg.Observations) != 3 {
		t.Fatalf("Observations len = %d, want 3", len(pkg.Observations))
	}
	if len(pkg.Findings) != 3 {
		t.Fatalf("Findings len = %d, want 3", len(pkg.Findings))
	}
	if len(pkg.Recommendations) != 3 {
		t.Fatalf("Recommendations len = %d, want 3", len(pkg.Recommendations))
	}
	if pkg.Environment != "prod" {
		t.Fatalf("Environment = %q, want prod", pkg.Environment)
	}
	if pkg.ID == "" {
		t.Fatal("merged package ID is empty")
	}
}

// TestOrchestratePartialFailureReturnsSuccessfulResults verifies that when some
// sub-requests fail, the orchestrator still returns results for the successful
// ones (best-effort), and records the failures in the package.
func TestOrchestratePartialFailureReturnsSuccessfulResults(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{
		packages: map[string]diagnostics.Package{
			"kafka": samplePackage("kafka", "prod"),
		},
		errs: map[string]error{
			"minio": errors.New("minio endpoint unreachable"),
		},
	}
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	orch := orchestrator.New(runner, 2, func() time.Time { return now })

	requests := []diagnostics.Request{
		{Domain: "kafka", Environment: "prod", Runbook: "health"},
		{Domain: "minio", Environment: "prod", Runbook: "health"},
	}

	pkg, err := orch.Orchestrate(context.Background(), testUser(), requests)
	if err != nil {
		t.Fatalf("Orchestrate should not fail on partial failure: %v", err)
	}

	// Only kafka succeeded.
	if len(pkg.Domains) != 1 || pkg.Domains[0] != "kafka" {
		t.Fatalf("Domains = %v, want [kafka]", pkg.Domains)
	}
	if len(pkg.Observations) != 1 {
		t.Fatalf("Observations len = %d, want 1 (kafka only)", len(pkg.Observations))
	}
}

// TestOrchestrateAllFailsReturnsError verifies that when all sub-requests fail,
// the orchestrator returns an error.
func TestOrchestrateAllFailsReturnsError(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{
		errs: map[string]error{
			"kafka": errors.New("kafka down"),
			"minio": errors.New("minio down"),
		},
	}
	orch := orchestrator.New(runner, 2, time.Now)

	requests := []diagnostics.Request{
		{Domain: "kafka", Environment: "prod", Runbook: "health"},
		{Domain: "minio", Environment: "prod", Runbook: "health"},
	}

	_, err := orch.Orchestrate(context.Background(), testUser(), requests)
	if err == nil {
		t.Fatal("Orchestrate should return error when all sub-requests fail")
	}
}

// TestSplitMessageMultiDomain verifies that SplitMessage detects multiple
// domains in the user message and returns one sub-request per domain.
func TestSplitMessageMultiDomain(t *testing.T) {
	t.Parallel()
	orch := orchestrator.New(nil, 3, time.Now)

	base := diagnostics.Request{Environment: "prod", Runbook: "health"}
	requests := orch.SplitMessage("检查 prod 环境的 kafka 和 minio 健康状态", base)

	if len(requests) != 2 {
		t.Fatalf("SplitMessage returned %d requests, want 2", len(requests))
	}

	domains := map[string]bool{}
	for _, req := range requests {
		domains[req.Domain] = true
		if req.Environment != "prod" {
			t.Fatalf("Environment = %q, want prod", req.Environment)
		}
		if req.Runbook != "health" {
			t.Fatalf("Runbook = %q, want health", req.Runbook)
		}
	}
	if !domains["kafka"] || !domains["minio"] {
		t.Fatalf("domains = %v, want kafka+minio", domains)
	}
}

// TestSplitMessageSingleDomainReturnsOne verifies that a single-domain message
// returns exactly one request (no orchestration needed).
func TestSplitMessageSingleDomainReturnsOne(t *testing.T) {
	t.Parallel()
	orch := orchestrator.New(nil, 3, time.Now)

	base := diagnostics.Request{Environment: "prod", Runbook: "health"}
	requests := orch.SplitMessage("检查 prod 环境的 kafka 健康状态", base)

	if len(requests) != 1 {
		t.Fatalf("SplitMessage returned %d requests, want 1", len(requests))
	}
	if requests[0].Domain != "kafka" {
		t.Fatalf("Domain = %q, want kafka", requests[0].Domain)
	}
}

// TestSplitMessageMultiDomainClearsInheritedResource 是回归测试：当 base
// 诊断请求携带了某一域（如 glusterfs）的资源类型/名字，多域扇出到 minio/kafka
// 时，必须清空这些字段，否则 diagnostics 服务会把 volume 资源类型套到 minio/
// kafka 上并以 ErrInvalidRequest 拒绝，编排器 best-effort 丢域，只剩首个域。
func TestSplitMessageMultiDomainClearsInheritedResource(t *testing.T) {
	t.Parallel()
	orch := orchestrator.New(nil, 3, time.Now)

	// base 来自用户最先提到的 glusterfs 域：携带 volume / glusterfs-volume。
	base := diagnostics.Request{
		Environment:  "prod",
		Runbook:      "health",
		ResourceType: "volume",
		ResourceName: "glusterfs-volume",
	}
	requests := orch.SplitMessage("检查 prod 环境的 glusterfs volume、minio bucket 和 kafka consumer group 健康状态", base)

	if len(requests) != 3 {
		t.Fatalf("SplitMessage returned %d requests, want 3", len(requests))
	}
	for _, req := range requests {
		if req.ResourceType != "" || req.ResourceName != "" {
			t.Fatalf("domain %q inherited resource (type=%q name=%q), want cleared on multi-domain fan-out",
				req.Domain, req.ResourceType, req.ResourceName)
		}
		if req.Environment != "prod" || req.Runbook != "health" {
			t.Fatalf("domain %q lost inherited context (env=%q runbook=%q)", req.Domain, req.Environment, req.Runbook)
		}
	}
}

// TestSplitMessageSingleDomainKeepsInheritedResource 验证单域消息保留 base 的
// 资源字段（用户可能显式指定了具体资源），不被清空。
func TestSplitMessageSingleDomainKeepsInheritedResource(t *testing.T) {
	t.Parallel()
	orch := orchestrator.New(nil, 3, time.Now)

	base := diagnostics.Request{
		Environment:  "prod",
		Runbook:      "health",
		ResourceType: "volume",
		ResourceName: "data",
	}
	requests := orch.SplitMessage("检查 prod glusterfs data volume 健康状态", base)

	if len(requests) != 1 {
		t.Fatalf("SplitMessage returned %d requests, want 1", len(requests))
	}
	if requests[0].ResourceType != "volume" || requests[0].ResourceName != "data" {
		t.Fatalf("single-domain resource = (type=%q name=%q), want preserved (volume/data)",
			requests[0].ResourceType, requests[0].ResourceName)
	}
}

// TestSplitMessageNoDomainReturnsEmpty verifies that a message with no
// recognized domain returns an empty slice.
func TestSplitMessageNoDomainReturnsEmpty(t *testing.T) {
	t.Parallel()
	orch := orchestrator.New(nil, 3, time.Now)

	base := diagnostics.Request{Environment: "prod", Runbook: "health"}
	requests := orch.SplitMessage("检查 prod 环境的健康状态", base)

	if len(requests) != 0 {
		t.Fatalf("SplitMessage returned %d requests, want 0", len(requests))
	}
}

// TestSplitMessageRejectsBareSubstringDomain 是回归测试：修复前
// SplitMessage 用裸 strings.Contains，"kafkax" 误命中 "kafka" 并生成
// 诊断子请求。现要求词边界完整，裸子串不再匹配。
func TestSplitMessageRejectsBareSubstringDomain(t *testing.T) {
	t.Parallel()
	orch := orchestrator.New(nil, 3, time.Now)

	base := diagnostics.Request{Environment: "prod", Runbook: "health"}
	for _, message := range []string{
		"查看 prod kafkax 状态",
		"检查 minioadmin 配置",
		"glusterfsx 卷健康",
	} {
		requests := orch.SplitMessage(message, base)
		if len(requests) != 0 {
			t.Fatalf("SplitMessage(%q) = %d requests, want 0 — bare substring must not match", message, len(requests))
		}
	}
}

// TestSplitMessagePreservesOrderAcrossMultipleDomains 验证多域消息按文本
// 出现顺序返回子请求（"kafka 和 minio" → [kafka, minio]）。
func TestSplitMessagePreservesOrderAcrossMultipleDomains(t *testing.T) {
	t.Parallel()
	orch := orchestrator.New(nil, 3, time.Now)

	base := diagnostics.Request{Environment: "prod", Runbook: "health"}
	requests := orch.SplitMessage("检查 prod 环境的 kafka 和 minio 健康状态", base)

	if len(requests) != 2 {
		t.Fatalf("SplitMessage returned %d requests, want 2", len(requests))
	}
	if requests[0].Domain != "kafka" || requests[1].Domain != "minio" {
		t.Fatalf("domain order = [%s %s], want [kafka minio]", requests[0].Domain, requests[1].Domain)
	}
}

// TestRunSingleDomainDelegates verifies that Run with a single-domain message
// delegates to the underlying runner without orchestration overhead.
func TestRunSingleDomainDelegates(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{
		packages: map[string]diagnostics.Package{"kafka": samplePackage("kafka", "prod")},
	}
	orch := orchestrator.New(runner, 3, time.Now)

	request := diagnostics.Request{Domain: "kafka", Environment: "prod", Runbook: "health"}
	ctx := orchestrator.WithMessage(context.Background(), "检查 kafka 健康状态")
	pkg, err := orch.Run(ctx, testUser(), request)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if pkg.ID != "diag-kafka" {
		t.Fatalf("pkg ID = %q, want diag-kafka", pkg.ID)
	}
	runner.mu.Lock()
	if len(runner.calls) != 1 {
		t.Fatalf("runner calls = %d, want 1 (single domain, no orchestration)", len(runner.calls))
	}
	runner.mu.Unlock()
}

// TestRunMultiDomainOrchestrates verifies that Run with a multi-domain message
// automatically splits and orchestrates.
func TestRunMultiDomainOrchestrates(t *testing.T) {
	t.Parallel()
	runner := &fakeRunner{
		packages: map[string]diagnostics.Package{
			"kafka": samplePackage("kafka", "prod"),
			"minio": samplePackage("minio", "prod"),
		},
	}
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	orch := orchestrator.New(runner, 3, func() time.Time { return now })

	request := diagnostics.Request{Environment: "prod", Runbook: "health"}
	ctx := orchestrator.WithMessage(context.Background(), "检查 prod kafka 和 minio 健康状态")
	pkg, err := orch.Run(ctx, testUser(), request)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(pkg.Domains) != 2 {
		t.Fatalf("Domains len = %d, want 2", len(pkg.Domains))
	}
	runner.mu.Lock()
	if len(runner.calls) != 2 {
		t.Fatalf("runner calls = %d, want 2 (orchestrated)", len(runner.calls))
	}
	runner.mu.Unlock()
}

// TestOrchestrateMergedPackageCapped verifies that a multi-domain merged package
// whose observation data exceeds the assistant-response reserve is drained to
// fit, mirroring the single-domain diagnostics.Service.Run size governance.
func TestOrchestrateMergedPackageCapped(t *testing.T) {
	t.Parallel()

	// 每个观察塞入远超预算的原始数据，多域合并后必然超限。
	bigData := map[string]any{"blob": make([]byte, 256*1024)}
	bigAlias := func(domain string) diagnostics.Package {
		p := samplePackage(domain, "prod")
		p.Observations[0].Data = bigData
		return p
	}
	runner := &fakeRunner{
		packages: map[string]diagnostics.Package{
			"kafka":   bigAlias("kafka"),
			"minio":   bigAlias("minio"),
			"gluster": bigAlias("gluster"),
		},
	}
	now := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	orch := orchestrator.New(runner, 3, func() time.Time { return now })

	requests := []diagnostics.Request{
		{Domain: "kafka", Environment: "prod", Runbook: "health"},
		{Domain: "minio", Environment: "prod", Runbook: "health"},
		{Domain: "gluster", Environment: "prod", Runbook: "health"},
	}

	pkg, err := orch.Orchestrate(context.Background(), testUser(), requests)
	if err != nil {
		t.Fatalf("Orchestrate with oversized sub-packages: %v", err)
	}

	// 合并包结构仍完整（未被截断破坏）。
	if len(pkg.Observations) != 3 {
		t.Fatalf("merged Observations len = %d, want 3", len(pkg.Observations))
	}
	// 超限的原生数据被回收为 truncated 标记，而非整包报错或原样透传。
	for i, obs := range pkg.Observations {
		if _, ok := obs.Data["truncated"]; !ok {
			t.Fatalf("merged observation %d Data not drained to truncated marker: %v", i, obs.Data)
		}
	}
}
