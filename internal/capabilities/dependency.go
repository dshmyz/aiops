package capabilities

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ExecutionStep is one capability invocation in a resolved dependency chain,
// carrying the input the runtime should pass to it.
type ExecutionStep struct {
	// Capability is the capability name to invoke.
	Capability string
	// Input is the mapped input for this step, derived from the root
	// capability's input via the declared input_mapping.
	Input map[string]any
	// Phase is DependencyPhasePre, DependencyPhasePost, or "" for the root
	// capability itself.
	Phase string
	// Type is DependencyRequired, DependencyOptional, or DependencySuggested.
	// The root capability is always DependencyRequired.
	Type string
	// DependedOnBy names the capability that pulled this step into the chain.
	// Empty for the root. Useful for audit trails and error messages.
	DependedOnBy string
}

// Required reports whether a failure of this step must abort the chain.
func (s ExecutionStep) Required() bool { return s.Type == DependencyRequired }

// ExecutionChain is a fully ordered plan for running a capability together
// with its transitive dependencies. Steps are in execution order: pre-phase
// dependencies first (deepest first), then the root capability, then
// post-phase dependencies.
type ExecutionChain struct {
	// Root is the capability the caller asked for.
	Root string
	// Steps is the ordered execution plan, including Root.
	Steps []ExecutionStep
}

// RootIndex returns the position of the root capability within Steps, or -1
// when the chain is empty. Callers use this to distinguish "setup failed"
// (before the root ran) from "cleanup failed" (after).
func (c ExecutionChain) RootIndex() int {
	for i, step := range c.Steps {
		if step.Capability == c.Root && step.Phase == "" {
			return i
		}
	}
	return -1
}

// DependencyResolver turns a capability's declared depends_on edges into a
// flat, ordered ExecutionChain. It is read-only after construction and safe
// for concurrent use.
type DependencyResolver struct {
	byName map[string]Capability
}

// NewDependencyResolver indexes the supplied capabilities by name. Later
// entries win on duplicate names, matching the loader's behaviour.
func NewDependencyResolver(loaded []Capability) *DependencyResolver {
	byName := make(map[string]Capability, len(loaded))
	for _, capability := range loaded {
		byName[capability.Name] = capability
	}
	return &DependencyResolver{byName: byName}
}

// Resolve builds the execution chain for name, mapping input down to each
// dependency. It fails on unknown capabilities, cycles, and unsatisfiable
// input mappings so the caller never starts a chain it cannot finish.
func (r *DependencyResolver) Resolve(name string, input map[string]any) (*ExecutionChain, error) {
	if r == nil || len(r.byName) == 0 {
		return nil, fmt.Errorf("capability %q is not registered", name)
	}
	chain := &ExecutionChain{Root: name}
	// visiting tracks the current DFS path for cycle detection; scheduled
	// dedupes diamond dependencies so a shared prerequisite runs once.
	visiting := map[string]bool{}
	scheduled := map[string]bool{}
	if err := r.visit(name, input, "", DependencyRequired, "", visiting, scheduled, chain); err != nil {
		return nil, err
	}
	return chain, nil
}

func (r *DependencyResolver) visit(name string, input map[string]any, phase, depType, dependedOnBy string, visiting, scheduled map[string]bool, chain *ExecutionChain) error {
	if visiting[name] {
		return fmt.Errorf("dependency cycle detected at capability %q", name)
	}
	if scheduled[name] {
		return nil
	}
	capability, ok := r.byName[name]
	if !ok {
		if dependedOnBy == "" {
			return fmt.Errorf("capability %q is not registered", name)
		}
		// A missing optional or suggested dependency degrades to a skip so a
		// partially-installed registry still runs the capability the operator
		// asked for; a missing required one is fatal.
		if depType != DependencyRequired {
			return nil
		}
		return fmt.Errorf("capability %q depends on unregistered capability %q", dependedOnBy, name)
	}

	visiting[name] = true
	defer delete(visiting, name)

	pre, post := splitDependencies(capability.DependsOn)

	for _, dep := range pre {
		depInput, err := buildDependencyInput(dep, input)
		if err != nil {
			return fmt.Errorf("capability %q: %w", name, err)
		}
		if err := r.visit(dep.Capability, depInput, DependencyPhasePre, dependencyType(dep), name, visiting, scheduled, chain); err != nil {
			return err
		}
	}

	scheduled[name] = true
	chain.Steps = append(chain.Steps, ExecutionStep{
		Capability:   name,
		Input:        input,
		Phase:        phase,
		Type:         depType,
		DependedOnBy: dependedOnBy,
	})

	for _, dep := range post {
		depInput, err := buildDependencyInput(dep, input)
		if err != nil {
			return fmt.Errorf("capability %q: %w", name, err)
		}
		if err := r.visit(dep.Capability, depInput, DependencyPhasePost, dependencyType(dep), name, visiting, scheduled, chain); err != nil {
			return err
		}
	}
	return nil
}

// splitDependencies partitions declared dependencies by phase, preserving
// declaration order within each phase so operators control the sequence of
// sibling prerequisites.
func splitDependencies(deps []DependencySpec) (pre, post []DependencySpec) {
	for _, dep := range deps {
		if dependencyPhase(dep) == DependencyPhasePost {
			post = append(post, dep)
			continue
		}
		pre = append(pre, dep)
	}
	return pre, post
}

func dependencyType(dep DependencySpec) string {
	if strings.TrimSpace(dep.Type) == "" {
		return DependencyRequired
	}
	return dep.Type
}

func dependencyPhase(dep DependencySpec) string {
	if strings.TrimSpace(dep.Phase) == "" {
		return DependencyPhasePre
	}
	return dep.Phase
}

// buildDependencyInput maps the parent capability's input to the dependency's
// input. It reuses the verify-spec template form ("{field}" pulls from the
// parent input, anything else is a literal) so operators only learn one
// mapping syntax. Without a mapping the parent input is forwarded unchanged
// and the dependency's own input schema rejects any mismatch. With a mapping,
// the parent input is carried through as the base and mapping targets
// override, so unmapped fields (cluster, resource names) reach the dependency
// unchanged.
func buildDependencyInput(dep DependencySpec, parentInput map[string]any) (map[string]any, error) {
	if len(dep.InputMapping) == 0 {
		return parentInput, nil
	}
	result := make(map[string]any, len(parentInput)+len(dep.InputMapping))
	for name, value := range parentInput {
		result[name] = value
	}
	for target, template := range dep.InputMapping {
		value, err := applyTemplate(template, parentInput)
		if err != nil {
			return nil, fmt.Errorf("map input %q for dependency %q: %w", target, dep.Capability, err)
		}
		result[target] = value
	}
	return result, nil
}

// ValidateDependencies checks the whole registry at load time: every declared
// dependency resolves to a known capability, no capability depends on itself,
// and there are no cycles. Catching this at startup keeps a broken graph out
// of the registry instead of failing on an operator's first request.
func ValidateDependencies(loaded []Capability) error {
	byName := make(map[string]Capability, len(loaded))
	for _, capability := range loaded {
		byName[capability.Name] = capability
	}

	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		capability := byName[name]
		for _, dep := range capability.DependsOn {
			target, ok := byName[dep.Capability]
			if !ok {
				if dependencyType(dep) != DependencyRequired {
					continue
				}
				return fmt.Errorf("capability %q depends on unregistered capability %q", name, dep.Capability)
			}
			// A published capability that depends on a draft would pass load
			// and then fail at execution time, so reject it up front.
			if capability.Status == StatusPublished && target.Status != StatusPublished {
				return fmt.Errorf("published capability %q depends on non-published capability %q", name, dep.Capability)
			}
		}
	}

	// Cycle detection over the full graph, independent of any single root.
	state := map[string]int{} // 0 unvisited, 1 visiting, 2 done
	var walk func(string, []string) error
	walk = func(name string, path []string) error {
		switch state[name] {
		case 1:
			return fmt.Errorf("dependency cycle detected: %s", strings.Join(append(path, name), " -> "))
		case 2:
			return nil
		}
		state[name] = 1
		for _, dep := range byName[name].DependsOn {
			if _, ok := byName[dep.Capability]; !ok {
				continue
			}
			if err := walk(dep.Capability, append(path, name)); err != nil {
				return err
			}
		}
		state[name] = 2
		return nil
	}
	for _, name := range names {
		if err := walk(name, nil); err != nil {
			return err
		}
	}
	return nil
}

// validateDependencySpecs checks a single capability's depends_on entries for
// structural problems. Called from Validate so a malformed declaration is
// rejected with the rest of the capability's schema errors.
func validateDependencySpecs(capability Capability) error {
	seen := make(map[string]bool, len(capability.DependsOn))
	for _, dep := range capability.DependsOn {
		target := strings.TrimSpace(dep.Capability)
		if target == "" {
			return errors.New("depends_on entries require a capability name")
		}
		if target == capability.Name {
			return fmt.Errorf("capability %q cannot depend on itself", capability.Name)
		}
		if seen[target] {
			return fmt.Errorf("duplicate dependency on %q", target)
		}
		seen[target] = true

		switch dependencyType(dep) {
		case DependencyRequired, DependencyOptional, DependencySuggested:
		default:
			return fmt.Errorf("dependency %q has invalid type %q", target, dep.Type)
		}
		switch dependencyPhase(dep) {
		case DependencyPhasePre, DependencyPhasePost:
		default:
			return fmt.Errorf("dependency %q has invalid phase %q", target, dep.Phase)
		}
	}
	return nil
}
