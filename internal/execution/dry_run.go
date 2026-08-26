package execution

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// ErrDryRunNotSupported is returned when a tool does not support dry-run
// preview. This covers three cases: the tool is not registered, the tool is a
// read-only tool (side-effect free, no preview needed), or the tool declares
// SupportsDryRun but no handler has been registered for it.
var ErrDryRunNotSupported = errors.New("dry-run not supported for this tool")

// DryRunResult is the preview of an intended write operation. It describes
// what would happen without actually executing it, so the operator can review
// the impact before confirming the action plan.
type DryRunResult struct {
	// Summary is a human-readable description of the intended operation.
	Summary string `json:"summary"`
	// AffectedResources lists the resources that would be impacted (e.g.
	// "topic:orders@prod"). Used by the frontend to render a resource chip list.
	AffectedResources []string `json:"affected_resources,omitempty"`
	// Commands lists the commands or API calls that would be executed.
	Commands []string `json:"commands,omitempty"`
	// Warnings surfaces risk notices (e.g. "shortening retention may delete
	// messages"). The frontend renders these as risk_notice blocks.
	Warnings []string `json:"warnings,omitempty"`
	// SuggestedStrategy is the auto-inferred execution strategy (借鉴-3: 任务
	// 草稿自动补齐执行策略). DryRunService fills it via SuggestStrategy when
	// the handler does not provide one, so every write plan carries a complete
	// "how to run" hint (timeout/retry/concurrency/target hosts/risk level).
	// A handler may set it to override the default inference.
	SuggestedStrategy *SuggestedStrategy `json:"suggested_strategy,omitempty"`
}

// SuggestedStrategy is the execution strategy auto-inferred for a write plan.
// It answers "how should this run?" so the operator confirms a complete plan,
// not just "what to do" but also timeout, retry, concurrency, target hosts and
// risk level. SuggestStrategy produces the default; handlers may override.
type SuggestedStrategy struct {
	// Timeout is the recommended per-operation timeout. Long commands
	// (alter/delete/rebalance/restart) get 60s; others default to 30s.
	Timeout time.Duration `json:"timeout,omitempty"`
	// Retry is the recommended retry count. Defaults to 0 for write tools to
	// avoid repeating side effects on failure.
	Retry int `json:"retry,omitempty"`
	// Concurrency is the recommended parallelism for batch operations. Single
	// resource → 1; multiple resources → min(len, 5) to protect the target.
	Concurrency int `json:"concurrency,omitempty"`
	// TargetHosts lists the hosts the operation targets, transparently
	// forwarded from input["hosts"] so the operator can confirm the scope.
	TargetHosts []string `json:"target_hosts,omitempty"`
	// RiskLevel mirrors tool.Risk (low/medium/high) so the frontend can render
	// a risk badge without a separate tool lookup.
	RiskLevel string `json:"risk_level,omitempty"`
}

// DryRunHandler produces a DryRunResult for a specific tool without executing
// the write operation. Handlers must be pure: no side effects, no network calls
// against the target system. They derive the preview solely from the input.
type DryRunHandler func(ctx context.Context, input map[string]any) (DryRunResult, error)

// DryRunService maps registered tools to their dry-run handlers and runs
// previews. It is the single entry point for plan-time dry-run: the assistant
// service calls DryRun after creating a pending write plan and attaches the
// result as a risk_notice block to the confirmation_required response.
type DryRunService struct {
	mu       sync.RWMutex
	handlers map[string]DryRunHandler
}

// NewDryRunService returns an empty DryRunService. Register handlers with
// Register before calling DryRun.
func NewDryRunService() *DryRunService {
	return &DryRunService{handlers: map[string]DryRunHandler{}}
}

// Register associates a dry-run handler with a tool name. A tool may override
// its handler by registering again. Register panics when name is empty or
// handler is nil to catch wiring mistakes early.
func (s *DryRunService) Register(name string, handler DryRunHandler) {
	if name == "" {
		panic("dry-run register: tool name is required")
	}
	if handler == nil {
		panic(fmt.Sprintf("dry-run register: handler for %q is nil", name))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[name] = handler
}

// DryRun runs the registered preview handler for toolName. It returns
// ErrDryRunNotSupported when the tool is not registered, does not declare
// SupportsDryRun, or has no handler wired.
//
// After the handler returns, DryRun auto-fills SuggestedStrategy via
// SuggestStrategy when the handler did not set one (借鉴-3). A handler that
// knows tool-specific strategy may pre-set SuggestedStrategy to override.
func (s *DryRunService) DryRun(ctx context.Context, toolName string, input map[string]any) (DryRunResult, error) {
	tool, ok := tools.Lookup(toolName)
	if !ok {
		return DryRunResult{}, ErrDryRunNotSupported
	}
	if !tool.SupportsDryRun {
		return DryRunResult{}, ErrDryRunNotSupported
	}
	s.mu.RLock()
	handler, ok := s.handlers[toolName]
	s.mu.RUnlock()
	if !ok {
		return DryRunResult{}, ErrDryRunNotSupported
	}
	result, err := handler(ctx, input)
	if err != nil {
		return DryRunResult{}, err
	}
	if result.SuggestedStrategy == nil {
		strategy := SuggestStrategy(tool, result, input)
		result.SuggestedStrategy = &strategy
	}
	return result, nil
}

// SuggestStrategy infers a default execution strategy from the tool's risk
// level and the dry-run result. It is the single, reusable inference ruleset
// shared by every write tool, so handlers stay focused on "what" and the
// service layers "how" uniformly:
//
//   - RiskLevel: mirrors tool.Risk so the frontend renders a risk badge
//     without an extra tool lookup.
//   - Concurrency: single resource → 1; multiple resources → min(len, 5)
//     to parallelize batch work without overwhelming the target system.
//   - Timeout: commands containing alter/delete/rebalance/restart get 60s;
//     otherwise 30s. These verbs typically touch more state or wait for
//     cluster propagation.
//   - Retry: 0 for write tools. Retrying a side-effecting operation on
//     failure can duplicate writes, so the default is conservative.
//   - TargetHosts: forwarded from input["hosts"] when the caller supplies
//     them, so the operator can confirm the execution scope at a glance.
//
// Handlers may return a pre-filled SuggestedStrategy to override any of these
// defaults for tool-specific needs.
func SuggestStrategy(tool tools.Tool, result DryRunResult, input map[string]any) SuggestedStrategy {
	strategy := SuggestedStrategy{
		RiskLevel: string(tool.Risk),
		Retry:     0,
	}

	resourceCount := len(result.AffectedResources)
	if resourceCount <= 1 {
		strategy.Concurrency = 1
	} else {
		strategy.Concurrency = resourceCount
		if strategy.Concurrency > 5 {
			strategy.Concurrency = 5
		}
	}

	strategy.Timeout = 30 * time.Second
	for _, cmd := range result.Commands {
		lower := strings.ToLower(cmd)
		if strings.Contains(lower, "alter") ||
			strings.Contains(lower, "delete") ||
			strings.Contains(lower, "rebalance") ||
			strings.Contains(lower, "restart") {
			strategy.Timeout = 60 * time.Second
			break
		}
	}

	if hosts, ok := input["hosts"].([]string); ok {
		strategy.TargetHosts = hosts
	}
	return strategy
}

// DryRunTemplate carries the operator-facing preview data for a write tool.
// It is sourced from the capability YAML (dry_run section + backend), so
// previews stay data-driven: the concrete command and risk warnings a tool
// shows are declared by its capability, never hardcoded per-component in Go.
type DryRunTemplate struct {
	// Summary is the preview summary template rendered from input. Empty →
	// the handler falls back to the generic "will execute a write" summary.
	Summary string
	// Commands are command templates rendered from input (e.g. the actual
	// HTTP endpoint or a CLI command the operator would run). Empty → no
	// concrete command shown.
	Commands []string
	// Warnings are risk-notice templates rendered from input.
	Warnings []string
	// ResourceType labels the affected resource in the preview (e.g. "topic").
	ResourceType string
	// ResourceKey is the input field that names the affected resource (e.g.
	// "topic"), derived from the capability's backend path variable — the path
	// itself declares which input is the resource, so callers never hardcode a
	// field-name list. Empty → no affected resource is shown.
	ResourceKey string
}

// TemplateDryRunHandler renders a capability-declared dry-run preview. It is
// the single, domain-agnostic dry-run handler: which command, risk warning and
// affected resource a write tool shows is decided by its capability (dry_run
// template + backend path), not by Go code.
func TemplateDryRunHandler(tmpl DryRunTemplate) DryRunHandler {
	return func(_ context.Context, input map[string]any) (DryRunResult, error) {
		result := DryRunResult{}

		if tmpl.Summary != "" {
			result.Summary = renderTemplate(tmpl.Summary, input)
		}
		for _, command := range tmpl.Commands {
			result.Commands = append(result.Commands, renderTemplate(command, input))
		}
		for _, warning := range tmpl.Warnings {
			result.Warnings = append(result.Warnings, renderTemplate(warning, input))
		}

		if tmpl.ResourceKey != "" {
			if resourceName, ok := input[tmpl.ResourceKey].(string); ok && resourceName != "" {
				result.AffectedResources = []string{fmt.Sprintf("%s:%s", tmpl.ResourceType, resourceName)}
			}
		}
		return result, nil
	}
}

// renderTemplate fills {field} placeholders in a template from the input map.
// Unknown placeholders are left as-is so a missing field degrades to a readable
// preview instead of failing the dry-run.
func renderTemplate(template string, input map[string]any) string {
	result := template
	for key, value := range input {
		result = strings.ReplaceAll(result, "{"+key+"}", fmt.Sprint(value))
	}
	return result
}
