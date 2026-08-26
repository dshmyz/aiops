package marketplace

import (
	"context"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	db, err := store.OpenWithDriver("sqlite", "file:marketplace_test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Only the marketplace tables are needed here; the runtime markers (Future
	// Migration Ledger etc.) are brought in by store.ApplySQLiteMigrations.
	if err := store.ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	return NewService(db)
}

const sampleYAML = `schema_version: 1
name: k8s.pod.restart
status: published
domain: kubernetes
resource_type: pod
operation: write
risk: medium
backend:
    adapter: http
    method: POST
    path: /api/k8s/namespaces/{namespace}/pods/{pod_name}/restart
    timeout_ms: 10000
    base_url: http://127.0.0.1:19090
input_schema:
    namespace:
        type: string
        required: true
    pod_name:
        type: string
        required: true
output:
    kind: change
    summary_template: Restarted pod {pod_name} in {namespace}
governance:
    requires_action_plan: true
    requires_approval: true
    precheck_tools:
        - k8s.pod.status.read
    rollback:
        strategy: "manual"
        source: ""
auth:
    roles:
        - operator
        - admin
ai:
    description: Restart a Kubernetes pod.
    examples:
        - 重启 default 命名空间的 test-pod
`

func TestPublishAndSearch(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	org := "acme"
	registry, version, err := svc.Publish(ctx, PublishRequest{
		YAMLContent:    sampleYAML,
		Version:        "1.0.0",
		OwnerID:        "admin-1",
		Visibility:     VisibilityTeam,
		OrganizationID: &org,
		Category:       stringPtr("Infrastructure"),
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if registry.Name != "k8s.pod.restart" {
		t.Errorf("registry name = %q, want k8s.pod.restart", registry.Name)
	}
	if registry.Visibility != VisibilityTeam {
		t.Errorf("visibility = %q, want %q", registry.Visibility, VisibilityTeam)
	}
	if version.Version != "1.0.0" {
		t.Errorf("version = %q, want 1.0.0", version.Version)
	}
	if version.YAMLHash == "" {
		t.Error("version yaml_hash is empty")
	}

	// A second publish of the same name creates a new version on the same registry.
	_, v2, err := svc.Publish(ctx, PublishRequest{
		YAMLContent: sampleYAML,
		Version:     "1.1.0",
		OwnerID:     "admin-1",
		Visibility:  VisibilityTeam,
	})
	if err != nil {
		t.Fatalf("publish v2: %v", err)
	}
	if v2.ID == version.ID {
		t.Error("expected a distinct version ID for the second publish")
	}

	versions, err := svc.ListVersions(ctx, registry.ID)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("got %d versions, want 2", len(versions))
	}

	// Search by keyword and filter by domain.
	items, total, err := svc.Search(ctx, SearchRequest{Query: "restart", Domain: "kubernetes"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if total != 1 {
		t.Errorf("search total = %d, want 1", total)
	}
	if len(items) != 1 || items[0].Name != "k8s.pod.restart" {
		t.Errorf("search items = %+v, want one k8s.pod.restart", items)
	}
}

func TestRateUpsertRecalculatesAverage(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	registry, _, err := svc.Publish(ctx, PublishRequest{
		YAMLContent: sampleYAML,
		Version:     "1.0.0",
		OwnerID:     "admin-1",
		Visibility:  VisibilityPublic,
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	if err := svc.Rate(ctx, registry.ID, "user-a", 5, nil, nil); err != nil {
		t.Fatalf("rate 5: %v", err)
	}
	if err := svc.Rate(ctx, registry.ID, "user-b", 3, nil, nil); err != nil {
		t.Fatalf("rate 3: %v", err)
	}

	got, err := svc.Get(ctx, registry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RatingCount != 2 {
		t.Errorf("rating_count = %d, want 2", got.RatingCount)
	}
	if got.AvgRating == nil || *got.AvgRating != 4 {
		t.Errorf("avg_rating = %v, want 4.0", got.AvgRating)
	}

	// user-a re-rates 5 -> 2; the average becomes (2+3)/2 = 2.5.
	if err := svc.Rate(ctx, registry.ID, "user-a", 2, nil, nil); err != nil {
		t.Fatalf("re-rate user-a: %v", err)
	}
	got, err = svc.Get(ctx, registry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RatingCount != 2 {
		t.Errorf("rating_count after re-rate = %d, want 2 (no new row)", got.RatingCount)
	}
	if got.AvgRating == nil || *got.AvgRating != 2.5 {
		t.Errorf("avg_rating after re-rate = %v, want 2.5", got.AvgRating)
	}

	ratings, total, err := svc.ListRatings(ctx, registry.ID, 20, 0)
	if err != nil {
		t.Fatalf("list ratings: %v", err)
	}
	if total != 2 {
		t.Errorf("ratings total = %d, want 2", total)
	}
	if len(ratings) != 2 {
		t.Errorf("got %d ratings, want 2", len(ratings))
	}
}

func TestRecordDownloadAndUsageAndStats(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	registry, version, err := svc.Publish(ctx, PublishRequest{
		YAMLContent: sampleYAML,
		Version:     "1.0.0",
		OwnerID:     "admin-1",
		Visibility:  VisibilityPublic,
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	if err := svc.RecordDownload(ctx, registry.ID, version.ID, "user-a", nil, "cli"); err != nil {
		t.Fatalf("record download: %v", err)
	}

	ms := 3200
	if err := svc.RecordUsage(ctx, registry.ID, &version.ID, "user-a", nil, "success", &ms, nil); err != nil {
		t.Fatalf("record usage: %v", err)
	}
	if err := svc.RecordUsage(ctx, registry.ID, &version.ID, "user-b", nil, "failed", &ms, nil); err != nil {
		t.Fatalf("record usage failed: %v", err)
	}

	stats, err := svc.Stats(ctx, registry.ID)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.TotalDownloads != 1 {
		t.Errorf("total_downloads = %d, want 1", stats.TotalDownloads)
	}
	if stats.TotalExecutions != 2 {
		t.Errorf("total_executions = %d, want 2", stats.TotalExecutions)
	}
	if stats.SuccessRate != 0.5 {
		t.Errorf("success_rate = %v, want 0.5", stats.SuccessRate)
	}
}

func TestGetErrorsOnMissing(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.Get(context.Background(), "does-not-exist")
	if err != ErrNotFound {
		t.Errorf("Get error = %v, want ErrNotFound", err)
	}
}

func TestVersionRequiredAndInvalidYAML(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	if _, _, err := svc.Publish(ctx, PublishRequest{YAMLContent: sampleYAML, Version: "", OwnerID: "admin-1"}); err == nil {
		t.Error("expected error when version is empty")
	}
	if _, _, err := svc.Publish(ctx, PublishRequest{YAMLContent: "not: [valid yaml", Version: "1.0.0", OwnerID: "admin-1"}); err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func stringPtr(v string) *string { return &v }
