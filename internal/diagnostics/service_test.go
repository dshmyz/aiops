package diagnostics_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestServiceRunsGlusterReadToolIntoDiagnosticPackage(t *testing.T) {
	t.Parallel()
	reads := &fakeReads{result: map[string]any{"status": "warning", "capacity_pct": 82.5, "heal_pending": 12}}
	service := diagnostics.NewService(reads, diagnostics.ClockFunc(func() time.Time {
		return time.Date(2026, time.July, 22, 9, 0, 0, 0, time.UTC)
	}))

	pkg, err := service.Run(context.Background(), user(), diagnostics.Request{Domain: "glusterfs", Environment: "prod", ResourceType: "volume", ResourceName: "data", Runbook: "health"})
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if reads.toolName != tools.GlusterVolumeHealthRead {
		t.Fatalf("tool = %q, want %q", reads.toolName, tools.GlusterVolumeHealthRead)
	}
	if pkg.ID == "" || pkg.Environment != "prod" || len(pkg.Resources) != 1 || len(pkg.Observations) != 1 || len(pkg.Findings) != 1 || len(pkg.Recommendations) != 1 {
		t.Fatalf("package = %+v, want populated diagnostic", pkg)
	}
	if pkg.Resources[0].Domain != "glusterfs" || pkg.Resources[0].Name != "data" {
		t.Fatalf("resource = %+v, want glusterfs data volume", pkg.Resources[0])
	}
	if err := diagnostics.ValidatePackage(pkg); err != nil {
		t.Fatalf("invalid package: %v", err)
	}
}

func TestServiceRejectsUnknownDomain(t *testing.T) {
	t.Parallel()
	reads := &fakeReads{}
	service := diagnostics.NewService(reads, nil)

	_, err := service.Run(context.Background(), user(), diagnostics.Request{Domain: "shell", Environment: "prod", ResourceName: "root"})
	if !errors.Is(err, diagnostics.ErrUnsupportedDomain) {
		t.Fatalf("error = %v, want unsupported domain", err)
	}
	if reads.calls != 0 {
		t.Fatalf("read calls = %d, want 0", reads.calls)
	}
}

func TestServiceAcceptsSchemaRunbookValues(t *testing.T) {
	t.Parallel()
	// The Eino planner schema (eino_planner.go prompt) advertises runbook
	// health | capacity | consumer_lag. Each maps to the same domain read tool;
	// all three must be accepted instead of rejected.
	cases := []struct {
		name    string
		request diagnostics.Request
	}{
		{name: "capacity", request: diagnostics.Request{Domain: "minio", Environment: "prod", ResourceType: "bucket", ResourceName: "archive", Runbook: "capacity"}},
		{name: "consumer_lag", request: diagnostics.Request{Domain: "kafka", Environment: "prod", ResourceType: "consumer_group", ResourceName: "orders", Runbook: "consumer_lag"}},
		{name: "empty", request: diagnostics.Request{Domain: "minio", Environment: "prod", ResourceType: "bucket", ResourceName: "archive", Runbook: ""}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			reads := &fakeReads{result: map[string]any{"status": "ok"}}
			service := diagnostics.NewService(reads, nil)

			_, err := service.Run(context.Background(), user(), testCase.request)

			if err != nil {
				t.Fatalf("Run returned %v, want accepted", err)
			}
			if reads.calls != 1 {
				t.Fatalf("read calls = %d, want 1", reads.calls)
			}
		})
	}
}

func TestServiceTruncatesLargeReadResultBeforePackaging(t *testing.T) {
	t.Parallel()
	reads := &fakeReads{result: map[string]any{
		"status":  "warning",
		"details": strings.Repeat("x", 20*1024),
	}}
	service := diagnostics.NewService(reads, nil)

	pkg, err := service.Run(context.Background(), user(), diagnostics.Request{Domain: "glusterfs", Environment: "prod", ResourceName: "data"})
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	encoded, err := json.Marshal(pkg)
	if err != nil {
		t.Fatalf("json.Marshal returned %v", err)
	}
	if len(encoded) >= 9*1024 {
		t.Fatalf("serialized diagnostic package = %d bytes, want under 9 KB", len(encoded))
	}
	if truncated, ok := pkg.Observations[0].Data["truncated"].(bool); !ok || !truncated {
		t.Fatalf("truncated marker = %#v, want true", pkg.Observations[0].Data["truncated"])
	}
	if marker, ok := pkg.Observations[0].Data["truncation"].(string); !ok || marker == "" {
		t.Fatalf("truncation marker = %#v, want non-empty string", pkg.Observations[0].Data["truncation"])
	}
}

func TestServiceRejectsInvalidDiagnosticCandidatesBeforeRead(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		request diagnostics.Request
	}{
		{name: "invalid runbook", request: diagnostics.Request{Domain: "glusterfs", Environment: "prod", Runbook: "repair"}},
		{name: "invalid resource type", request: diagnostics.Request{Domain: "glusterfs", Environment: "prod", ResourceType: "bucket"}},
		{name: "missing environment", request: diagnostics.Request{Domain: "glusterfs", Environment: "   "}},
		{name: "oversized environment", request: diagnostics.Request{Domain: "glusterfs", Environment: strings.Repeat("e", 128)}},
		{name: "oversized resource name", request: diagnostics.Request{Domain: "glusterfs", Environment: "prod", ResourceName: strings.Repeat("r", 128)}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			reads := &fakeReads{result: map[string]any{"status": "warning"}}
			service := diagnostics.NewService(reads, nil)

			_, err := service.Run(context.Background(), user(), testCase.request)

			if !errors.Is(err, diagnostics.ErrInvalidRequest) {
				t.Fatalf("error = %v, want invalid diagnostic request", err)
			}
			if reads.calls != 0 {
				t.Fatalf("read calls = %d, want 0", reads.calls)
			}
		})
	}
}

func TestServiceBoundsSerializedPackageWithEscapedMultibyteRequestStrings(t *testing.T) {
	t.Parallel()
	requestText := strings.Repeat("\u4e2d\"\\\\", 32)
	reads := &fakeReads{result: map[string]any{
		"status":  requestText,
		"details": strings.Repeat("x", 8_000),
	}}
	service := diagnostics.NewService(reads, nil)

	pkg, err := service.Run(context.Background(), user(), diagnostics.Request{
		Domain:       "glusterfs",
		Environment:  "prod",
		ResourceName: "data",
	})
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	encoded, err := json.Marshal(pkg)
	if err != nil {
		t.Fatalf("json.Marshal returned %v", err)
	}
	if len(encoded) >= 9*1024 {
		t.Fatalf("serialized diagnostic package = %d bytes, want under 9 KB", len(encoded))
	}
	if err := diagnostics.ValidatePackage(pkg); err != nil {
		t.Fatalf("invalid package: %v", err)
	}
	got := pkg.Observations[0].Data
	if got["truncated"] != true {
		t.Fatalf("observation data = %#v, want truncated marker", got)
	}
}

type fakeReads struct {
	calls    int
	toolName string
	input    map[string]any
	result   map[string]any
}

func (f *fakeReads) ExecuteRead(_ context.Context, _ identity.CurrentUser, toolName string, input map[string]any) (map[string]any, error) {
	f.calls++
	f.toolName = toolName
	f.input = input
	return f.result, nil
}

func user() identity.CurrentUser {
	return identity.CurrentUser{Subject: "operator-1", Roles: []string{"viewer"}, AllowedEnvironments: []string{"prod"}, RequestID: "request-1"}
}

type mockCapabilityResolver struct {
	toolName         string
	resourceType     string
	schema           map[string]any
	ok               bool
	callDomain       string
	callResourceType string
	callOperation    string
}

func (m *mockCapabilityResolver) ResolveDiagnosticTool(domain, resourceType, operation string) (string, string, map[string]any, bool) {
	m.callDomain = domain
	m.callResourceType = resourceType
	m.callOperation = operation
	return m.toolName, m.resourceType, m.schema, m.ok
}

func TestServicePrefersCapabilityResolverOverHardcodedSwitch(t *testing.T) {
	t.Parallel()
	reads := &fakeReads{result: map[string]any{"status": "warning"}}
	resolver := &mockCapabilityResolver{
		toolName:     "custom.domain.health.read",
		resourceType: "custom-resource",
		schema:       map[string]any{"environment": "string", "name": "string"},
		ok:           true,
	}
	service := diagnostics.NewService(reads, nil).WithCapabilityResolver(resolver)

	pkg, err := service.Run(context.Background(), user(), diagnostics.Request{
		Domain:       "custom-domain",
		Environment:  "prod",
		ResourceType: "custom-resource",
		ResourceName: "my-resource",
	})
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if reads.toolName != "custom.domain.health.read" {
		t.Fatalf("tool = %q, want custom.domain.health.read from resolver", reads.toolName)
	}
	if reads.calls != 1 {
		t.Fatalf("read calls = %d, want 1", reads.calls)
	}
	if reads.input["environment"] != "prod" || reads.input["name"] != "my-resource" {
		t.Fatalf("input = %#v, want environment=prod and name=my-resource", reads.input)
	}
	if resolver.callDomain != "custom-domain" {
		t.Fatalf("resolver domain = %q, want custom-domain", resolver.callDomain)
	}
	if resolver.callResourceType != "custom-resource" {
		t.Fatalf("resolver resource type = %q, want custom-resource", resolver.callResourceType)
	}
	if resolver.callOperation != "read" {
		t.Fatalf("resolver operation = %q, want read", resolver.callOperation)
	}
	if err := diagnostics.ValidatePackage(pkg); err != nil {
		t.Fatalf("invalid package: %v", err)
	}
}

func TestServiceCapabilityResolverOverridesKnownDomainTool(t *testing.T) {
	t.Parallel()
	reads := &fakeReads{result: map[string]any{"status": "warning"}}
	resolver := &mockCapabilityResolver{
		toolName:     "kafka.enhanced.health.read",
		resourceType: "consumer_group",
		schema:       map[string]any{"environment": "string", "name": "string"},
		ok:           true,
	}
	service := diagnostics.NewService(reads, nil).WithCapabilityResolver(resolver)

	pkg, err := service.Run(context.Background(), user(), diagnostics.Request{
		Domain:       "kafka",
		Environment:  "prod",
		ResourceType: "consumer_group",
		ResourceName: "orders-consumer",
	})
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if reads.toolName != "kafka.enhanced.health.read" {
		t.Fatalf("tool = %q, want kafka.enhanced.health.read (from resolver, not switch)", reads.toolName)
	}
	if pkg.Resources[0].Type != "consumer_group" {
		t.Fatalf("resource type = %q, want consumer_group", pkg.Resources[0].Type)
	}
}

func TestServiceFallsBackToSwitchWhenResolverMisses(t *testing.T) {
	t.Parallel()
	reads := &fakeReads{result: map[string]any{"status": "warning"}}
	resolver := &mockCapabilityResolver{ok: false}
	service := diagnostics.NewService(reads, nil).WithCapabilityResolver(resolver)

	pkg, err := service.Run(context.Background(), user(), diagnostics.Request{
		Domain:       "kafka",
		Environment:  "prod",
		ResourceName: "orders-consumer",
	})
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if reads.toolName != tools.KafkaConsumerLagRead {
		t.Fatalf("tool = %q, want %q (hardcoded switch fallback)", reads.toolName, tools.KafkaConsumerLagRead)
	}
	if err := diagnostics.ValidatePackage(pkg); err != nil {
		t.Fatalf("invalid package: %v", err)
	}
}

func TestServiceUsesCapabilityInputSchemaToBuildReadInput(t *testing.T) {
	t.Parallel()
	reads := &fakeReads{result: map[string]any{"status": "warning"}}
	resolver := &mockCapabilityResolver{
		toolName:     "custom.domain.health.read",
		resourceType: "custom-resource",
		schema:       map[string]any{"environment": "string", "volume": "string"},
		ok:           true,
	}
	service := diagnostics.NewService(reads, nil).WithCapabilityResolver(resolver)

	_, err := service.Run(context.Background(), user(), diagnostics.Request{
		Domain:       "custom-domain",
		Environment:  "prod",
		ResourceType: "custom-resource",
		ResourceName: "my-vol",
	})
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if reads.input["environment"] != "prod" {
		t.Fatalf("input environment = %#v, want prod", reads.input["environment"])
	}
	if reads.input["volume"] != "my-vol" {
		t.Fatalf("input volume = %#v, want my-vol (resource name mapped to schema field)", reads.input["volume"])
	}
	if _, hasName := reads.input["name"]; hasName {
		t.Fatal("input should not contain 'name' when schema uses 'volume' as the resource field")
	}
}

func TestServiceFillsRecommendationToolNameForKafkaCritical(t *testing.T) {
	t.Parallel()
	reads := &fakeReads{result: map[string]any{"status": "critical"}}
	service := diagnostics.NewService(reads, nil)

	pkg, err := service.Run(context.Background(), user(), diagnostics.Request{
		Domain:       "kafka",
		Environment:  "prod",
		ResourceName: "orders-consumer",
	})
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	if len(pkg.Recommendations) != 1 {
		t.Fatalf("expected 1 recommendation, got %d", len(pkg.Recommendations))
	}
	rec := pkg.Recommendations[0]
	if rec.ToolName != tools.TopicRetentionSet {
		t.Fatalf("toolName = %q, want %q", rec.ToolName, tools.TopicRetentionSet)
	}
	if !rec.Actionable {
		t.Fatal("expected actionable recommendation")
	}
	if rec.CandidateInput["environment"] != "prod" {
		t.Fatalf("candidate input environment = %#v, want prod", rec.CandidateInput["environment"])
	}
	if rec.CandidateInput["topic"] != "orders-consumer" {
		t.Fatalf("candidate input topic = %#v, want orders-consumer", rec.CandidateInput["topic"])
	}
	if rec.CandidateInput["retention_hours"] != 24 {
		t.Fatalf("candidate input retention_hours = %#v, want 24", rec.CandidateInput["retention_hours"])
	}
	if !pkg.HasActionableRecommendations() {
		t.Fatal("expected package to have actionable recommendations")
	}
	planTool, planInput, actionable := rec.ToPlanInput()
	if !actionable || planTool != tools.TopicRetentionSet {
		t.Fatalf("ToPlanInput = %q, actionable=%v, want %q, true", planTool, actionable, tools.TopicRetentionSet)
	}
	if planInput["retention_hours"] != 24 {
		t.Fatalf("plan input retention_hours = %#v, want 24", planInput["retention_hours"])
	}
}

func TestServiceFillsRecommendationToolNameForKafkaWarning(t *testing.T) {
	t.Parallel()
	reads := &fakeReads{result: map[string]any{"status": "warning"}}
	service := diagnostics.NewService(reads, nil)

	pkg, err := service.Run(context.Background(), user(), diagnostics.Request{
		Domain:       "kafka",
		Environment:  "staging",
		ResourceName: "events-consumer",
	})
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	rec := pkg.Recommendations[0]
	if rec.ToolName != tools.TopicRetentionSet {
		t.Fatalf("toolName = %q, want %q", rec.ToolName, tools.TopicRetentionSet)
	}
	if rec.CandidateInput["retention_hours"] != 48 {
		t.Fatalf("retention_hours = %#v, want 48 for warning severity", rec.CandidateInput["retention_hours"])
	}
}

func TestServiceLeavesToolNameEmptyForNonKafkaDomains(t *testing.T) {
	t.Parallel()
	reads := &fakeReads{result: map[string]any{"status": "critical"}}
	service := diagnostics.NewService(reads, nil)

	pkg, err := service.Run(context.Background(), user(), diagnostics.Request{
		Domain:       "glusterfs",
		Environment:  "prod",
		ResourceName: "data",
	})
	if err != nil {
		t.Fatalf("Run returned %v", err)
	}
	rec := pkg.Recommendations[0]
	if rec.ToolName != "" {
		t.Fatalf("toolName = %q, want empty for glusterfs domain", rec.ToolName)
	}
	if rec.Actionable {
		t.Fatal("expected non-actionable recommendation for glusterfs")
	}
	if pkg.HasActionableRecommendations() {
		t.Fatal("expected no actionable recommendations for glusterfs")
	}
	_, _, actionable := rec.ToPlanInput()
	if actionable {
		t.Fatal("expected ToPlanInput to return actionable=false for empty ToolName")
	}
}

func TestRecommendationToPlanInput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		rec        diagnostics.Recommendation
		wantTool   string
		wantAction bool
		wantInput  map[string]any
	}{
		{
			name: "actionable recommendation",
			rec: diagnostics.Recommendation{
				ToolName:       "topic.retention.set",
				CandidateInput: map[string]any{"environment": "prod", "topic": "orders", "retention_hours": 24},
			},
			wantTool:   "topic.retention.set",
			wantAction: true,
			wantInput:  map[string]any{"environment": "prod", "topic": "orders", "retention_hours": 24},
		},
		{
			name:       "empty tool name",
			rec:        diagnostics.Recommendation{ToolName: ""},
			wantTool:   "",
			wantAction: false,
			wantInput:  nil,
		},
		{
			name:       "whitespace tool name",
			rec:        diagnostics.Recommendation{ToolName: "  "},
			wantTool:   "",
			wantAction: false,
			wantInput:  nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			toolName, input, actionable := tc.rec.ToPlanInput()
			if actionable != tc.wantAction {
				t.Fatalf("actionable = %v, want %v", actionable, tc.wantAction)
			}
			if toolName != tc.wantTool {
				t.Fatalf("toolName = %q, want %q", toolName, tc.wantTool)
			}
			if !actionable && input != nil {
				t.Fatalf("expected nil input for non-actionable recommendation, got %#v", input)
			}
			if actionable {
				if input["retention_hours"] != tc.wantInput["retention_hours"] {
					t.Fatalf("input retention_hours = %#v, want %#v", input["retention_hours"], tc.wantInput["retention_hours"])
				}
			}
		})
	}
}

func TestPackageHasActionableRecommendations(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		pkg  diagnostics.Package
		want bool
	}{
		{
			name: "with actionable recommendation",
			pkg: diagnostics.Package{
				Recommendations: []diagnostics.Recommendation{
					{ToolName: ""},
					{ToolName: "topic.retention.set"},
				},
			},
			want: true,
		},
		{
			name: "without actionable recommendation",
			pkg: diagnostics.Package{
				Recommendations: []diagnostics.Recommendation{
					{ToolName: ""},
					{ToolName: "  "},
				},
			},
			want: false,
		},
		{
			name: "empty recommendations",
			pkg: diagnostics.Package{
				Recommendations: []diagnostics.Recommendation{},
			},
			want: false,
		},
		{
			name: "nil recommendations",
			pkg: diagnostics.Package{},
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := tc.pkg.HasActionableRecommendations()
			if got != tc.want {
				t.Fatalf("HasActionableRecommendations = %v, want %v", got, tc.want)
			}
		})
	}
}
