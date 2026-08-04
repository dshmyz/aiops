// Package policy evaluates server-side authorization for registered tools.
package policy

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// Reason explains the first policy gate that denied a request.
type Reason string

const (
	Permitted         Reason = "permitted"
	ToolNotRegistered Reason = "tool_not_registered"
	PermissionDenied  Reason = "permission_denied"
	InvalidInput      Reason = "invalid_input"
	EnvironmentDenied Reason = "environment_denied"
	ParameterDenied   Reason = "parameter_denied"
	RiskDenied        Reason = "risk_denied"
)

// Decision is the result of evaluating a single tool request. Allowed writes
// are never executable from this package; they require a later human
// confirmation flow before an execution service may act on them.
type Decision struct {
	Allowed              bool
	Reason               Reason
	RequiresConfirmation bool
	Tool                 tools.Tool
}

// rolePermissions is deliberately maintained in Go source. JWTs, request
// input, and model output can name a role or tool, but cannot add a mapping.
// 中间件读/写能力已外置为 YAML published 能力（examples/capabilities/published），
// 其角色权限由 capability auth.roles 经 RegisterDynamicRolePermissions 注入，
// 故此处仅保留平台内建元工具（系统态势/告警/事件/任务中心）。
var rolePermissions = map[string]map[string]struct{}{
	"viewer": {
		tools.ClusterStatusRead:  {},
		tools.QuerySystemPosture: {},
		tools.AlertQuery:         {},
		tools.EventQuery:         {},
		tools.TaskQuery:          {},
	},
	"operator": {
		tools.ClusterStatusRead:  {},
		tools.QuerySystemPosture: {},
		tools.AlertQuery:         {},
		tools.EventQuery:         {},
		tools.TaskQuery:          {},
	},
	"admin": {
		tools.ClusterStatusRead:  {},
		tools.QuerySystemPosture: {},
		tools.AlertQuery:         {},
		tools.EventQuery:         {},
		tools.TaskQuery:          {},
	},
}

var dynamicRolePermissions = map[string]map[string]struct{}{}
var dynamicRolePermissionsMu sync.RWMutex

// RegisterDynamicRolePermissions adds startup-loaded role permissions.
func RegisterDynamicRolePermissions(toolRoles map[string][]string) {
	dynamicRolePermissionsMu.Lock()
	defer dynamicRolePermissionsMu.Unlock()
	for tool, roles := range toolRoles {
		for _, role := range roles {
			if dynamicRolePermissions[role] == nil {
				dynamicRolePermissions[role] = map[string]struct{}{}
			}
			dynamicRolePermissions[role][tool] = struct{}{}
		}
	}
}

// ResetDynamicRolePermissionsForTest clears runtime permissions between tests.
func ResetDynamicRolePermissionsForTest() {
	dynamicRolePermissionsMu.Lock()
	defer dynamicRolePermissionsMu.Unlock()
	dynamicRolePermissions = map[string]map[string]struct{}{}
}

// Evaluate applies the fixed allowlist, role-to-tool mapping, input schema,
// environment, parameter, and risk controls. It resolves requestedTool by
// name again so caller-supplied metadata can never alter the decision.
func Evaluate(user identity.CurrentUser, requestedTool tools.Tool, input map[string]any) Decision {
	tool, ok := tools.Lookup(requestedTool.Name)
	if !ok {
		return deny(ToolNotRegistered)
	}

	if !hasToolPermission(user.Roles, tool.Name) {
		return deny(PermissionDenied)
	}

	if err := tools.ValidateInput(tool, input); err != nil {
		return deny(InvalidInput)
	}

	environment := input["environment"].(string)
	if !contains(user.AllowedEnvironments, environment) {
		return deny(EnvironmentDenied)
	}
	if err := validateParameters(tool, environment, input); err != nil {
		return deny(ParameterDenied)
	}
	if !riskAllowed(user.Roles, environment, tool.Risk) {
		return deny(RiskDenied)
	}

	return Decision{
		Allowed:              true,
		Reason:               Permitted,
		RequiresConfirmation: tool.Operation == tools.Write,
		Tool:                 tool,
	}
}

// productionParameterFloors are the minimum values a parameter may take in
// production, keyed by parameter name. They are matched on the parameter, not
// on the tool: the same operation is reachable both through a built-in static
// tool and through an equivalent published capability, and a tool-name check
// silently stops applying the moment the planner routes to the capability
// instead. Anything listed here is a value where too small a number destroys
// data (retention windows, replica counts), so the floor must hold on every
// route that can set it.
//
// Upper bounds and non-production limits stay in each tool's input schema
// (tools.ValidateInput for static tools, input_schema min/max for
// capabilities); this table is only the production floor that must not depend
// on which implementation happens to serve the request. Only writes are
// checked — the same name on a read is a filter ("topics under N hours"), not
// a value being set.
var productionParameterFloors = map[string]int64{
	"retention_hours": 24,
}

func deny(reason Reason) Decision {
	return Decision{Reason: reason}
}

func hasToolPermission(roles []string, toolName string) bool {
	dynamicRolePermissionsMu.RLock()
	defer dynamicRolePermissionsMu.RUnlock()
	for _, role := range roles {
		if _, ok := rolePermissions[role][toolName]; ok {
			return true
		}
		if _, ok := dynamicRolePermissions[role][toolName]; ok {
			return true
		}
	}
	return false
}

func validateParameters(tool tools.Tool, environment string, input map[string]any) error {
	if environment != "prod" || tool.Operation != tools.Write {
		return nil
	}
	for name, floor := range productionParameterFloors {
		value, present := input[name]
		if !present {
			continue
		}
		number, ok := integer(value)
		if !ok || number < floor {
			return fmt.Errorf("production %s must be at least %d", name, floor)
		}
	}
	return nil
}

func riskAllowed(roles []string, environment string, requested tools.RiskLevel) bool {
	requestedRank := riskRank(requested)
	for _, role := range roles {
		if riskRank(maximumRisk(role, environment)) >= requestedRank {
			return true
		}
	}
	return false
}

func maximumRisk(role, environment string) tools.RiskLevel {
	switch role {
	case "admin":
		return tools.High
	case "operator":
		if environment == "prod" {
			return tools.Low
		}
		return tools.Medium
	case "viewer":
		return tools.Low
	default:
		return ""
	}
}

func riskRank(risk tools.RiskLevel) int {
	switch risk {
	case tools.Low:
		return 1
	case tools.Medium:
		return 2
	case tools.High:
		return 3
	default:
		return 0
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func integer(value any) (int64, bool) {
	switch value := value.(type) {
	case int:
		return int64(value), true
	case int64:
		return value, true
	case float64:
		return int64(value), value == float64(int64(value))
	case json.Number:
		integer, err := value.Int64()
		return integer, err == nil
	default:
		return 0, false
	}
}
