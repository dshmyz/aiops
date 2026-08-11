package capabilities

import "github.com/gracegaoya/ai-operations-copilot/internal/tools"

const (
	StatusDiscovered  = "discovered"
	StatusNeedsReview = "needs_review"
	StatusPublished   = "published"
	StatusDeprecated  = "deprecated"
)

// Dependency types. Required is the default when a DependencySpec omits Type.
const (
	DependencyRequired  = "required"
	DependencyOptional  = "optional"
	DependencySuggested = "suggested"
)

// Dependency phases. Pre is the default when a DependencySpec omits Phase.
const (
	DependencyPhasePre  = "pre"
	DependencyPhasePost = "post"
)

type Capability struct {
	SchemaVersion int                   `yaml:"schema_version" json:"schema_version"`
	Name          string                `yaml:"name" json:"name"`
	Status        string                `yaml:"status" json:"status"`
	Domain        string                `yaml:"domain" json:"domain"`
	ResourceType  string                `yaml:"resource_type" json:"resource_type"`
	Operation     tools.Operation       `yaml:"operation" json:"operation"`
	Risk          tools.RiskLevel       `yaml:"risk" json:"risk"`
	Backend       BackendSpec           `yaml:"backend" json:"backend"`
	InputSchema   map[string]InputField `yaml:"input_schema" json:"input_schema"`
	Output        OutputSpec            `yaml:"output" json:"output"`
	Governance    GovernanceSpec        `yaml:"governance" json:"governance"`
	Auth          AuthSpec              `yaml:"auth" json:"auth"`
	AI            AISpec                `yaml:"ai" json:"ai"`
	Verify        *VerifySpec           `yaml:"verify,omitempty" json:"verify,omitempty"`
	DependsOn     []DependencySpec      `yaml:"depends_on,omitempty" json:"depends_on,omitempty"`
}

// DependencySpec declares that this capability requires another capability to
// run first. The resolver walks these edges to build an execution order, so a
// "restart service" capability can declare that traffic must be drained before
// it runs and restored afterwards.
type DependencySpec struct {
	// Capability is the name of the capability this one depends on.
	Capability string `yaml:"capability" json:"capability"`
	// Type is one of DependencyRequired, DependencyOptional, DependencySuggested.
	// Required dependencies abort the chain on failure; optional ones only warn.
	Type string `yaml:"type,omitempty" json:"type,omitempty"`
	// Phase controls when the dependency runs relative to this capability:
	// DependencyPhasePre (default) or DependencyPhasePost.
	Phase string `yaml:"phase,omitempty" json:"phase,omitempty"`
	// InputMapping maps this capability's input names to the dependency's
	// input names, mirroring VerifySpec.InputMapping. Unmapped inputs with
	// matching names are passed through.
	InputMapping map[string]string `yaml:"input_mapping,omitempty" json:"input_mapping,omitempty"`
}

// VerifySpec declares an optional post-execution read capability that the
// runtime calls after a write capability succeeds, so operators can confirm
// the change took effect without manually re-running a read.
type VerifySpec struct {
	ReadCapability string            `yaml:"read_capability" json:"read_capability"`
	InputMapping   map[string]string `yaml:"input_mapping,omitempty" json:"input_mapping,omitempty"`
	TimeoutMS      int               `yaml:"timeout_ms,omitempty" json:"timeout_ms,omitempty"`
}

type BackendSpec struct {
	Adapter   string `yaml:"adapter" json:"adapter"`
	Method    string `yaml:"method" json:"method"`
	Path      string `yaml:"path" json:"path"`
	TimeoutMS int    `yaml:"timeout_ms" json:"timeout_ms"`
	BaseURL   string `yaml:"base_url,omitempty" json:"base_url,omitempty"`
}

// InputField declares one capability input. Min/Max are inclusive numeric
// bounds for integer/number fields (omitted = unbounded). Declare them for any
// value where an out-of-range write would be destructive — retention windows,
// replica counts, quotas — because the runtime otherwise only checks the type.
type InputField struct {
	Type     string   `yaml:"type" json:"type"`
	Required bool     `yaml:"required" json:"required"`
	In       string   `yaml:"in,omitempty" json:"in,omitempty"`
	Min      *float64 `yaml:"min,omitempty" json:"min,omitempty"`
	Max      *float64 `yaml:"max,omitempty" json:"max,omitempty"`
	// Description 是人（及 LLM）可读的字段说明，帮助判断该参数含义与填什么。
	// Examples 提供可选示例值。Enum 声明该参数的合法取值（UI 渲染为下拉，LLM
	// 据此收敛取值）。
	Description string   `yaml:"description,omitempty" json:"description,omitempty"`
	Examples    []string `yaml:"examples,omitempty" json:"examples,omitempty"`
	Enum        []string `yaml:"enum,omitempty" json:"enum,omitempty"`
}

type OutputSpec struct {
	Kind            string            `yaml:"kind" json:"kind"`
	SeverityPath    string            `yaml:"severity_path" json:"severity_path"`
	SummaryTemplate string            `yaml:"summary_template" json:"summary_template"`
	Fields          map[string]string `yaml:"fields" json:"fields"`
}

type GovernanceSpec struct {
	RequiresActionPlan bool         `yaml:"requires_action_plan" json:"requires_action_plan"`
	RequiresApproval   bool         `yaml:"requires_approval" json:"requires_approval"`
	PrecheckTools      []string     `yaml:"precheck_tools" json:"precheck_tools"`
	Rollback           RollbackSpec `yaml:"rollback" json:"rollback"`
}

type RollbackSpec struct {
	Strategy string `yaml:"strategy" json:"strategy"`
	Source   string `yaml:"source" json:"source"`
}

type AuthSpec struct {
	Roles             []string `yaml:"roles" json:"roles"`
	EnvironmentScoped bool     `yaml:"environment_scoped" json:"environment_scoped"`
}

type AISpec struct {
	Description string   `yaml:"description" json:"description"`
	Examples    []string `yaml:"examples" json:"examples"`
}

type NormalizedResult struct {
	Kind     string         `json:"kind"`
	Resource ResourceRef    `json:"resource"`
	Severity string         `json:"severity"`
	Summary  string         `json:"summary"`
	Data     map[string]any `json:"data"`
}

type ResourceRef struct {
	Domain      string `json:"domain"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Environment string `json:"environment"`
}
