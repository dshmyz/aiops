package capabilities

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// ReadRunner is the narrow execution contract shared with the read service.
type ReadRunner interface {
	Read(context.Context, tools.Tool, map[string]any) (map[string]any, error)
}

type PublishedCapabilityRuntime interface {
	AddPublishedCapability(Capability) error
}

func RegisterPublished(root string) ([]Capability, error) {
	loaded, err := LoadPublished(root)
	if err != nil {
		return nil, err
	}
	for _, capability := range loaded {
		if _, exists := tools.Lookup(capability.Name); exists {
			return nil, fmt.Errorf("published capability %q conflicts with an existing tool", capability.Name)
		}
	}
	for _, capability := range loaded {
		if err := RegisterPublishedCapability(capability); err != nil {
			return nil, err
		}
	}
	return loaded, nil
}

func RegisterPublishedCapability(capability Capability) error {
	if capability.Status != StatusPublished {
		return nil
	}
	tool, err := ToTool(capability)
	if err != nil {
		return err
	}
	schema := make(map[string]tools.DynamicInputField, len(capability.InputSchema))
	for name, field := range capability.InputSchema {
		schema[name] = tools.DynamicInputField{
			Type:        field.Type,
			Required:    field.Required,
			Min:         field.Min,
			Max:         field.Max,
			Description: field.Description,
			Examples:    field.Examples,
			Enum:        field.Enum,
		}
	}
	if err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{{Tool: tool, InputSchema: schema}}); err != nil {
		return err
	}
	policy.RegisterDynamicRolePermissions(map[string][]string{capability.Name: append([]string(nil), capability.Auth.Roles...)})
	return nil
}

type CapabilityReadRunner struct {
	mu           sync.RWMutex
	next         ReadRunner
	capabilities map[string]Capability
	adapter      *HTTPAdapter
}

func NewCapabilityReadRunner(next ReadRunner, loaded []Capability, adapter *HTTPAdapter) *CapabilityReadRunner {
	if adapter == nil {
		adapter = NewHTTPAdapter(nil)
	}
	byName := map[string]Capability{}
	for _, capability := range loaded {
		if capability.Status != StatusPublished || capability.Operation != tools.Read || capability.Backend.Method != http.MethodGet {
			continue
		}
		if tools.IsStatic(capability.Name) {
			continue
		}
		if registered, exists := tools.Lookup(capability.Name); exists {
			tool, err := ToTool(capability)
			if err != nil || !tools.SameDefinition(registered, tool) {
				continue
			}
		}
		byName[capability.Name] = capability
	}
	return &CapabilityReadRunner{next: next, capabilities: byName, adapter: adapter}
}

func (r *CapabilityReadRunner) AddPublishedCapability(capability Capability) error {
	if capability.Status != StatusPublished || capability.Operation != tools.Read || capability.Backend.Method != http.MethodGet {
		return nil
	}
	if tools.IsStatic(capability.Name) {
		return nil
	}
	registered, exists := tools.Lookup(capability.Name)
	if !exists {
		return fmt.Errorf("published capability %q is not registered as a tool", capability.Name)
	}
	tool, err := ToTool(capability)
	if err != nil {
		return err
	}
	if !tools.SameDefinition(registered, tool) {
		return fmt.Errorf("published capability %q does not match registered tool metadata", capability.Name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.capabilities[capability.Name] = capability
	return nil
}

func (r *CapabilityReadRunner) Read(ctx context.Context, tool tools.Tool, input map[string]any) (map[string]any, error) {
	r.mu.RLock()
	if capability, ok := r.capabilities[tool.Name]; ok {
		r.mu.RUnlock()
		result, err := r.adapter.Execute(ctx, capability, input)
		if err != nil {
			return nil, err
		}
		return map[string]any{"kind": result.Kind, "resource": result.Resource, "severity": result.Severity, "summary": result.Summary, "data": result.Data}, nil
	}
	r.mu.RUnlock()
	return r.next.Read(ctx, tool, input)
}

// CapabilityWriteRunner routes write-tool executions for published dynamic
// capabilities through the HTTPAdapter. Static tools and unknown names fall
// through to the supplied fallback executor (which may be nil). It also
// implements execution.Verifier: after a write succeeds, it optionally calls
// a related read capability to confirm the change took effect.
type CapabilityWriteRunner struct {
	mu           sync.RWMutex
	next         execution.Executor
	capabilities map[string]Capability
	adapter      *HTTPAdapter
	readRunner   ReadRunner
}

func NewCapabilityWriteRunner(next execution.Executor, loaded []Capability, adapter *HTTPAdapter) *CapabilityWriteRunner {
	return NewCapabilityWriteRunnerWithVerifier(next, loaded, adapter, nil)
}

// NewCapabilityWriteRunnerWithVerifier wires an optional ReadRunner used by
// Verify to invoke the declared post-execution read capability. Pass nil to
// disable verification for this runner.
func NewCapabilityWriteRunnerWithVerifier(next execution.Executor, loaded []Capability, adapter *HTTPAdapter, readRunner ReadRunner) *CapabilityWriteRunner {
	if adapter == nil {
		adapter = NewHTTPAdapter(nil)
	}
	byName := map[string]Capability{}
	for _, capability := range loaded {
		if !isPublishedWriteCapability(capability) {
			continue
		}
		if tools.IsStatic(capability.Name) {
			continue
		}
		if registered, exists := tools.Lookup(capability.Name); exists {
			tool, err := ToTool(capability)
			if err != nil || !tools.SameDefinition(registered, tool) {
				continue
			}
		}
		byName[capability.Name] = capability
	}
	return &CapabilityWriteRunner{next: next, capabilities: byName, adapter: adapter, readRunner: readRunner}
}

func (r *CapabilityWriteRunner) AddPublishedCapability(capability Capability) error {
	if !isPublishedWriteCapability(capability) {
		return nil
	}
	if tools.IsStatic(capability.Name) {
		return nil
	}
	registered, exists := tools.Lookup(capability.Name)
	if !exists {
		return fmt.Errorf("published capability %q is not registered as a tool", capability.Name)
	}
	tool, err := ToTool(capability)
	if err != nil {
		return err
	}
	if !tools.SameDefinition(registered, tool) {
		return fmt.Errorf("published capability %q does not match registered tool metadata", capability.Name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.capabilities[capability.Name] = capability
	return nil
}

func (r *CapabilityWriteRunner) Execute(ctx context.Context, name string, input map[string]any) (map[string]any, error) {
	r.mu.RLock()
	if capability, ok := r.capabilities[name]; ok {
		r.mu.RUnlock()
		result, err := r.adapter.Execute(ctx, capability, input)
		if err != nil {
			return nil, err
		}
		return map[string]any{"kind": result.Kind, "resource": result.Resource, "severity": result.Severity, "summary": result.Summary, "data": result.Data}, nil
	}
	r.mu.RUnlock()
	if r.next == nil {
		return nil, fmt.Errorf("no executor registered for tool %q", name)
	}
	return r.next.Execute(ctx, name, input)
}

func isPublishedWriteCapability(capability Capability) bool {
	if capability.Status != StatusPublished || capability.Operation != tools.Write {
		return false
	}
	switch capability.Backend.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// Verify implements execution.Verifier. After a write capability succeeds,
// it looks up the declared verify.read_capability, maps the write input to
// the read input via the declared templates, evaluates policy with the
// confirmed subject's identity, and invokes the configured ReadRunner.
// The returned *VerificationResult is never nil when the capability declares
// a verify spec; a nil return means "no verification declared".
func (r *CapabilityWriteRunner) Verify(ctx context.Context, plan store.PlanRecord, input map[string]any) (*execution.VerificationResult, error) {
	r.mu.RLock()
	capability, ok := r.capabilities[plan.ToolName]
	r.mu.RUnlock()
	if !ok || capability.Verify == nil || capability.Verify.ReadCapability == "" {
		return nil, nil
	}

	readName := capability.Verify.ReadCapability
	readTool, registered := tools.Lookup(readName)
	if !registered || readTool.Operation != tools.Read {
		return &execution.VerificationResult{
			ToolName: readName,
			Status:   execution.VerificationStatusFailed,
			Error:    fmt.Sprintf("verify read capability %q is not registered as a read tool", readName),
		}, nil
	}

	readInput, err := buildVerifyInput(capability.Verify.InputMapping, input)
	if err != nil {
		return &execution.VerificationResult{
			ToolName: readName,
			Status:   execution.VerificationStatusFailed,
			Error:    err.Error(),
		}, nil
	}

	user := verifyIdentity(plan, input)
	decision := policy.Evaluate(user, readTool, readInput)
	if !decision.Allowed {
		return &execution.VerificationResult{
			ToolName: readName,
			Status:   execution.VerificationStatusDenied,
			Error:    string(decision.Reason),
		}, nil
	}

	if r.readRunner == nil {
		return &execution.VerificationResult{
			ToolName: readName,
			Status:   execution.VerificationStatusFailed,
			Error:    "no read runner configured for verification",
		}, nil
	}

	verifyCtx := ctx
	if capability.Verify.TimeoutMS > 0 {
		var cancel context.CancelFunc
		verifyCtx, cancel = context.WithTimeout(ctx, time.Duration(capability.Verify.TimeoutMS)*time.Millisecond)
		defer cancel()
	}

	answer, err := r.readRunner.Read(verifyCtx, decision.Tool, readInput)
	if err != nil {
		return &execution.VerificationResult{
			ToolName: readName,
			Status:   execution.VerificationStatusFailed,
			Error:    err.Error(),
		}, nil
	}

	return &execution.VerificationResult{
		ToolName: readName,
		Status:   execution.VerificationStatusSuccess,
		Answer:   answer,
	}, nil
}

// buildVerifyInput maps the write capability input to the read capability
// input using the declared templates. Templates use the form "{field_name}"
// where field_name is a key in the write input. A literal value (no braces)
// is passed through unchanged. If a template references a missing field, the
// mapping fails.
func buildVerifyInput(mapping map[string]string, writeInput map[string]any) (map[string]any, error) {
	if len(mapping) == 0 {
		// Without an explicit mapping, default to forwarding all write input
		// fields. The read capability's input schema will reject mismatches.
		return writeInput, nil
	}
	result := make(map[string]any, len(mapping))
	for target, template := range mapping {
		value, err := applyTemplate(template, writeInput)
		if err != nil {
			return nil, fmt.Errorf("map verify input %q: %w", target, err)
		}
		result[target] = value
	}
	return result, nil
}

func applyTemplate(template string, writeInput map[string]any) (any, error) {
	if !strings.HasPrefix(template, "{") || !strings.HasSuffix(template, "}") {
		return template, nil
	}
	key := strings.TrimSpace(template[1 : len(template)-1])
	value, ok := writeInput[key]
	if !ok {
		return nil, fmt.Errorf("write input field %q is missing", key)
	}
	return value, nil
}

// verifyIdentity derives the identity used for the post-execution verify
// call. The write has already passed its own policy gate, so verification
// uses an internal admin identity scoped to the write's environment. This
// keeps the read governed (policy + audit) without forcing the plan record
// to carry the operator's full role set.
func verifyIdentity(plan store.PlanRecord, writeInput map[string]any) identity.CurrentUser {
	env, _ := writeInput["environment"].(string)
	if env == "" {
		env = "prod"
	}
	return identity.CurrentUser{
		Subject:             plan.ConfirmedBy,
		Roles:               []string{"admin"},
		AllowedEnvironments: []string{env},
		RequestID:           plan.RequestID,
	}
}
