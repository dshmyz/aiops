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
	// RemovePublishedCapability 在下架已发布能力时从运行时移除对应 capability，
	// 使执行路径不再路由到该工具。与 AddPublishedCapability 对称。
	RemovePublishedCapability(name string)
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
	// resolver 把 depends_on 声明解析为可执行的依赖链。随 loaded 构造，
	// Add/Remove 时重建以反映运行时增减。
	resolver *DependencyResolver
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
	return &CapabilityReadRunner{next: next, capabilities: byName, adapter: adapter, resolver: NewDependencyResolver(loaded)}
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
	r.rebuildResolverLocked()
	return nil
}

func (r *CapabilityReadRunner) Read(ctx context.Context, tool tools.Tool, input map[string]any) (map[string]any, error) {
	r.mu.RLock()
	if capability, ok := r.capabilities[tool.Name]; ok {
		r.mu.RUnlock()
		return executeCapabilityChain(ctx, r.resolver, r.adapter, capability, input)
	}
	r.mu.RUnlock()
	return r.next.Read(ctx, tool, input)
}

// RemovePublishedCapability 从读 runner 移除已下架能力，使其不再被路由。
func (r *CapabilityReadRunner) RemovePublishedCapability(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.capabilities, name)
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
	// resolver 把 depends_on 声明解析为可执行的依赖链。随 loaded 构造，
	// Add/Remove 时重建以反映运行时增减。
	resolver *DependencyResolver
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
	return &CapabilityWriteRunner{next: next, capabilities: byName, adapter: adapter, readRunner: readRunner, resolver: NewDependencyResolver(loaded)}
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
	r.rebuildResolverLocked()
	return nil
}

func (r *CapabilityWriteRunner) Execute(ctx context.Context, name string, input map[string]any) (map[string]any, error) {
	r.mu.RLock()
	if capability, ok := r.capabilities[name]; ok {
		r.mu.RUnlock()
		return executeCapabilityChain(ctx, r.resolver, r.adapter, capability, input)
	}
	r.mu.RUnlock()
	if r.next == nil {
		return nil, fmt.Errorf("no executor registered for tool %q", name)
	}
	return r.next.Execute(ctx, name, input)
}

// RemovePublishedCapability 从写 runner 移除已下架能力，使其不再被路由。
func (r *CapabilityWriteRunner) RemovePublishedCapability(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.capabilities, name)
	r.rebuildResolverLocked()
}

// rebuildResolverLocked 从当前 capabilities 快照重建依赖解析器，使 hot-add/remove
// 后 depends_on 声明仍然可解析。调用方必须持有 r.mu（写锁）。
func (r *CapabilityReadRunner) rebuildResolverLocked() {
	loaded := make([]Capability, 0, len(r.capabilities))
	for _, capability := range r.capabilities {
		loaded = append(loaded, capability)
	}
	r.resolver = NewDependencyResolver(loaded)
}

// rebuildResolverLocked 从当前 capabilities 快照重建依赖解析器（写 runner 版）。
func (r *CapabilityWriteRunner) rebuildResolverLocked() {
	loaded := make([]Capability, 0, len(r.capabilities))
	for _, capability := range r.capabilities {
		loaded = append(loaded, capability)
	}
	r.resolver = NewDependencyResolver(loaded)
}

// executeCapabilityChain 按 depends_on 声明的依赖链顺序执行能力：先执行
// pre 阶段依赖（最深的先跑），再执行 root 能力，最后执行 post 阶段依赖。
// required 依赖失败会中止整条链；optional/suggested 依赖失败只记录并跳过，
// 不阻断 root 执行。返回 root 的规范化结果，依赖执行结果聚合进 data 的
// dependencies 字段供审计与排障。
//
// 依赖 step 与 root 使用同一 adapter（HTTPAdapter 不区分 operation），因此
// 读 runner 也能编排写依赖、写 runner 也能编排读依赖；依赖是 operator 在
// 能力 YAML 中声明的可信编排，root 已通过策略层鉴权。
func executeCapabilityChain(ctx context.Context, resolver *DependencyResolver, adapter *HTTPAdapter, root Capability, input map[string]any) (map[string]any, error) {
	chain, err := resolver.Resolve(root.Name, input)
	if err != nil {
		return nil, err
	}
	executed := make([]map[string]any, 0, len(chain.Steps))
	var rootResult *NormalizedResult
	for _, step := range chain.Steps {
		capability, ok := resolver.byName[step.Capability]
		if !ok {
			return nil, fmt.Errorf("capability %q in dependency chain is not registered", step.Capability)
		}
		result, err := adapter.Execute(ctx, capability, step.Input)
		if err != nil {
			if step.Required() {
				return nil, fmt.Errorf("dependency %q (%s phase): %w", step.Capability, step.Phase, err)
			}
			// optional/suggested 依赖失败：记录并继续，不阻断 root。
			executed = append(executed, map[string]any{
				"capability": step.Capability,
				"phase":      step.Phase,
				"skipped":    true,
				"error":      err.Error(),
			})
			continue
		}
		executed = append(executed, map[string]any{
			"capability": step.Capability,
			"phase":      step.Phase,
			"summary":    result.Summary,
		})
		if step.Capability == root.Name && step.Phase == "" {
			rootResult = &result
		}
	}
	if rootResult == nil {
		return nil, fmt.Errorf("root capability %q did not execute in dependency chain", root.Name)
	}
	response := map[string]any{
		"kind":     rootResult.Kind,
		"resource": rootResult.Resource,
		"severity": rootResult.Severity,
		"summary":  rootResult.Summary,
		"data":     rootResult.Data,
	}
	if len(executed) > 1 {
		response["dependencies"] = executed
	}
	return response, nil
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
// uses an internal admin identity. This keeps the read governed (policy +
// audit) without forcing the plan record to carry the operator's full role
// set.
func verifyIdentity(plan store.PlanRecord, _ map[string]any) identity.CurrentUser {
	return identity.CurrentUser{
		Subject:   plan.ConfirmedBy,
		Roles:     []string{"admin"},
		RequestID: plan.RequestID,
	}
}
