package capabilities_test

import (
	"strings"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// drainRestartChain builds the canonical three-capability scenario: restarting
// a service requires draining traffic first and restoring it afterwards.
func drainRestartChain() []capabilities.Capability {
	drain := validReadCapability()
	drain.Name = "lb.backend.drain"
	drain.Operation = tools.Write
	drain.Risk = tools.Medium
	drain.Backend.Method = "POST"
	drain.Governance = writeGovernance()
	drain.Output = capabilities.OutputSpec{Kind: "action", SummaryTemplate: "drained"}

	restore := drain
	restore.Name = "lb.backend.restore"

	restart := drain
	restart.Name = "service.restart"
	restart.DependsOn = []capabilities.DependencySpec{
		{Capability: "lb.backend.drain", Phase: capabilities.DependencyPhasePre},
		{Capability: "lb.backend.restore", Phase: capabilities.DependencyPhasePost},
	}

	return []capabilities.Capability{drain, restore, restart}
}

func writeGovernance() capabilities.GovernanceSpec {
	return capabilities.GovernanceSpec{
		RequiresActionPlan: true,
		RequiresApproval:   true,
		PrecheckTools:      []string{"minio.bucket.capacity.read"},
		Rollback:           capabilities.RollbackSpec{Strategy: "restore_previous"},
	}
}

func stepNames(chain *capabilities.ExecutionChain) []string {
	names := make([]string, 0, len(chain.Steps))
	for _, step := range chain.Steps {
		names = append(names, step.Capability)
	}
	return names
}

func TestResolveOrdersPreAndPostDependencies(t *testing.T) {
	t.Parallel()
	resolver := capabilities.NewDependencyResolver(drainRestartChain())

	chain, err := resolver.Resolve("service.restart", map[string]any{"environment": "prod"})
	if err != nil {
		t.Fatalf("Resolve returned %v", err)
	}

	got := stepNames(chain)
	want := []string{"lb.backend.drain", "service.restart", "lb.backend.restore"}
	if len(got) != len(want) {
		t.Fatalf("steps = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("steps = %v, want %v", got, want)
		}
	}
	if idx := chain.RootIndex(); idx != 1 {
		t.Fatalf("RootIndex = %d, want 1", idx)
	}
}

func TestResolveRunsDeepestPrerequisiteFirst(t *testing.T) {
	t.Parallel()
	loaded := drainRestartChain()
	// drain itself now requires a health check to run first.
	health := validReadCapability()
	health.Name = "service.health.check"
	for i := range loaded {
		if loaded[i].Name == "lb.backend.drain" {
			loaded[i].DependsOn = []capabilities.DependencySpec{{Capability: "service.health.check"}}
		}
	}
	loaded = append(loaded, health)

	chain, err := capabilities.NewDependencyResolver(loaded).Resolve("service.restart", map[string]any{"environment": "prod"})
	if err != nil {
		t.Fatalf("Resolve returned %v", err)
	}

	got := stepNames(chain)
	want := []string{"service.health.check", "lb.backend.drain", "service.restart", "lb.backend.restore"}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("steps = %v, want %v", got, want)
		}
	}
}

func TestResolveSchedulesSharedDependencyOnce(t *testing.T) {
	t.Parallel()
	loaded := drainRestartChain()
	health := validReadCapability()
	health.Name = "service.health.check"
	// Both drain and restore depend on the same health check (diamond).
	for i := range loaded {
		switch loaded[i].Name {
		case "lb.backend.drain", "lb.backend.restore":
			loaded[i].DependsOn = []capabilities.DependencySpec{{Capability: "service.health.check"}}
		}
	}
	loaded = append(loaded, health)

	chain, err := capabilities.NewDependencyResolver(loaded).Resolve("service.restart", map[string]any{"environment": "prod"})
	if err != nil {
		t.Fatalf("Resolve returned %v", err)
	}

	count := 0
	for _, step := range chain.Steps {
		if step.Capability == "service.health.check" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("health check scheduled %d times, want 1", count)
	}
}

func TestResolveRejectsCycle(t *testing.T) {
	t.Parallel()
	a := validReadCapability()
	a.Name = "a.capability"
	a.DependsOn = []capabilities.DependencySpec{{Capability: "b.capability"}}
	b := validReadCapability()
	b.Name = "b.capability"
	b.DependsOn = []capabilities.DependencySpec{{Capability: "a.capability"}}

	_, err := capabilities.NewDependencyResolver([]capabilities.Capability{a, b}).Resolve("a.capability", nil)
	if err == nil {
		t.Fatal("Resolve accepted a dependency cycle")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("error = %v, want a cycle error", err)
	}
}

func TestResolveRejectsMissingRequiredDependency(t *testing.T) {
	t.Parallel()
	root := validReadCapability()
	root.Name = "root.capability"
	root.DependsOn = []capabilities.DependencySpec{{Capability: "absent.capability"}}

	_, err := capabilities.NewDependencyResolver([]capabilities.Capability{root}).Resolve("root.capability", nil)
	if err == nil {
		t.Fatal("Resolve accepted a missing required dependency")
	}
}

func TestResolveSkipsMissingOptionalDependency(t *testing.T) {
	t.Parallel()
	root := validReadCapability()
	root.Name = "root.capability"
	root.DependsOn = []capabilities.DependencySpec{
		{Capability: "absent.capability", Type: capabilities.DependencyOptional},
	}

	chain, err := capabilities.NewDependencyResolver([]capabilities.Capability{root}).Resolve("root.capability", nil)
	if err != nil {
		t.Fatalf("Resolve returned %v for a missing optional dependency", err)
	}
	if got := stepNames(chain); len(got) != 1 || got[0] != "root.capability" {
		t.Fatalf("steps = %v, want just the root capability", got)
	}
}

func TestResolveAppliesInputMapping(t *testing.T) {
	t.Parallel()
	loaded := drainRestartChain()
	for i := range loaded {
		if loaded[i].Name == "service.restart" {
			loaded[i].DependsOn = []capabilities.DependencySpec{{
				Capability:   "lb.backend.drain",
				InputMapping: map[string]string{"backend": "{instance}"},
			}}
		}
	}

	chain, err := capabilities.NewDependencyResolver(loaded).Resolve("service.restart", map[string]any{
		"environment": "prod",
		"instance":    "10.0.1.5:8080",
	})
	if err != nil {
		t.Fatalf("Resolve returned %v", err)
	}

	drainStep := chain.Steps[0]
	if drainStep.Capability != "lb.backend.drain" {
		t.Fatalf("first step = %q, want lb.backend.drain", drainStep.Capability)
	}
	if got := drainStep.Input["backend"]; got != "10.0.1.5:8080" {
		t.Fatalf("mapped backend = %v, want 10.0.1.5:8080", got)
	}
	// environment gates policy, so it must survive a mapping that omits it.
	if got := drainStep.Input["environment"]; got != "prod" {
		t.Fatalf("environment = %v, want prod to be carried through", got)
	}
}

func TestResolveRejectsInputMappingWithMissingField(t *testing.T) {
	t.Parallel()
	loaded := drainRestartChain()
	for i := range loaded {
		if loaded[i].Name == "service.restart" {
			loaded[i].DependsOn = []capabilities.DependencySpec{{
				Capability:   "lb.backend.drain",
				InputMapping: map[string]string{"backend": "{nonexistent}"},
			}}
		}
	}

	_, err := capabilities.NewDependencyResolver(loaded).Resolve("service.restart", map[string]any{"environment": "prod"})
	if err == nil {
		t.Fatal("Resolve accepted a mapping that references a missing input field")
	}
}

func TestResolveRejectsUnregisteredRoot(t *testing.T) {
	t.Parallel()
	_, err := capabilities.NewDependencyResolver(drainRestartChain()).Resolve("nope.capability", nil)
	if err == nil {
		t.Fatal("Resolve accepted an unregistered root capability")
	}
}

func TestValidateRejectsSelfDependency(t *testing.T) {
	t.Parallel()
	capability := validReadCapability()
	capability.DependsOn = []capabilities.DependencySpec{{Capability: capability.Name}}

	if err := capabilities.Validate(capability); err == nil {
		t.Fatal("Validate accepted a self-dependency")
	}
}

func TestValidateRejectsDuplicateDependency(t *testing.T) {
	t.Parallel()
	capability := validReadCapability()
	capability.DependsOn = []capabilities.DependencySpec{
		{Capability: "other.capability"},
		{Capability: "other.capability"},
	}

	if err := capabilities.Validate(capability); err == nil {
		t.Fatal("Validate accepted a duplicate dependency")
	}
}

func TestValidateRejectsInvalidDependencyTypeAndPhase(t *testing.T) {
	t.Parallel()

	badType := validReadCapability()
	badType.DependsOn = []capabilities.DependencySpec{{Capability: "other.capability", Type: "mandatory"}}
	if err := capabilities.Validate(badType); err == nil {
		t.Fatal("Validate accepted an invalid dependency type")
	}

	badPhase := validReadCapability()
	badPhase.DependsOn = []capabilities.DependencySpec{{Capability: "other.capability", Phase: "during"}}
	if err := capabilities.Validate(badPhase); err == nil {
		t.Fatal("Validate accepted an invalid dependency phase")
	}
}

func TestValidateDependenciesAcceptsValidGraph(t *testing.T) {
	t.Parallel()
	if err := capabilities.ValidateDependencies(drainRestartChain()); err != nil {
		t.Fatalf("ValidateDependencies returned %v", err)
	}
}

func TestValidateDependenciesRejectsCycle(t *testing.T) {
	t.Parallel()
	a := validReadCapability()
	a.Name = "a.capability"
	a.DependsOn = []capabilities.DependencySpec{{Capability: "b.capability"}}
	b := validReadCapability()
	b.Name = "b.capability"
	b.DependsOn = []capabilities.DependencySpec{{Capability: "a.capability"}}

	if err := capabilities.ValidateDependencies([]capabilities.Capability{a, b}); err == nil {
		t.Fatal("ValidateDependencies accepted a cycle")
	}
}

func TestValidateDependenciesRejectsPublishedDependingOnDraft(t *testing.T) {
	t.Parallel()
	draft := validReadCapability()
	draft.Name = "draft.capability"
	draft.Status = capabilities.StatusNeedsReview

	published := validReadCapability()
	published.Name = "published.capability"
	published.DependsOn = []capabilities.DependencySpec{{Capability: "draft.capability"}}

	err := capabilities.ValidateDependencies([]capabilities.Capability{draft, published})
	if err == nil {
		t.Fatal("ValidateDependencies accepted a published capability depending on a draft")
	}
}

func TestStepRequiredReflectsDependencyType(t *testing.T) {
	t.Parallel()
	if !(capabilities.ExecutionStep{Type: capabilities.DependencyRequired}).Required() {
		t.Fatal("required step reported as not required")
	}
	if (capabilities.ExecutionStep{Type: capabilities.DependencyOptional}).Required() {
		t.Fatal("optional step reported as required")
	}
}
