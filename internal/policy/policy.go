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
		tools.IncidentView:       {},
		tools.PrometheusQuery:    {},
	},
	"operator": {
		tools.ClusterStatusRead:  {},
		tools.QuerySystemPosture: {},
		tools.AlertQuery:         {},
		tools.EventQuery:         {},
		tools.TaskQuery:          {},
		tools.IncidentView:       {},
		tools.PrometheusQuery:    {},
	},
	"admin": {
		tools.ClusterStatusRead:  {},
		tools.QuerySystemPosture: {},
		tools.AlertQuery:         {},
		tools.EventQuery:         {},
		tools.TaskQuery:          {},
		tools.IncidentView:       {},
		tools.PrometheusQuery:    {},
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

// UnregisterDynamicRolePermissions removes role permissions for the given tools.
// 与 RegisterDynamicRolePermissions 对称：下架已发布能力时调用，避免策略层残留
// 对已下线工具的角色放行。未注册的工具/角色被静默忽略（幂等）。
func UnregisterDynamicRolePermissions(toolRoles map[string][]string) {
	dynamicRolePermissionsMu.Lock()
	defer dynamicRolePermissionsMu.Unlock()
	for tool := range toolRoles {
		for role, tools := range dynamicRolePermissions {
			delete(tools, tool)
			if len(tools) == 0 {
				delete(dynamicRolePermissions, role)
			}
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
// parameter, and risk controls. It resolves requestedTool by
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

	if err := validateParameters(tool, input); err != nil {
		return deny(ParameterDenied)
	}
	if !riskAllowed(user.Roles, tool.Risk) {
		return deny(RiskDenied)
	}

	return Decision{
		Allowed:              true,
		Reason:               Permitted,
		RequiresConfirmation: tool.Operation == tools.Write,
		Tool:                 tool,
	}
}

// parameterFloors are the minimum values a parameter may take on any route,
// keyed by parameter name. They are matched on the parameter, not
// on the tool: the same operation is reachable both through a built-in static
// tool and through an equivalent published capability, and a tool-name check
// silently stops applying the moment the planner routes to the capability
// instead. Anything listed here is a value where too small a number destroys
// data (retention windows, replica counts), so the floor must hold on every
// route that can set it.
//
// Upper bounds and non-production limits stay in each tool's input schema
// (tools.ValidateInput for static tools, input_schema min/max for
// capabilities); this table is only the safety floor that must not depend
// on which implementation happens to serve the request. Only writes are
// checked — the same name on a read is a filter ("topics under N hours"), not
// a value being set.
var parameterFloors = map[string]int64{
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

func validateParameters(tool tools.Tool, input map[string]any) error {
	if tool.Operation != tools.Write {
		return nil
	}
	for name, floor := range parameterFloors {
		value, present := input[name]
		if !present {
			continue
		}
		number, ok := integer(value)
		if !ok || number < floor {
			return fmt.Errorf("%s must be at least %d", name, floor)
		}
	}
	return nil
}

func riskAllowed(roles []string, requested tools.RiskLevel) bool {
	requestedRank := riskRank(requested)
	for _, role := range roles {
		if riskRank(maximumRisk(role)) >= requestedRank {
			return true
		}
	}
	return false
}

// maximumRisk 返回角色在任何请求上可承担的最大风险。环境概念移除后，
// 所有请求都视为生产操作（原 prod 分支语义），operator 保持保守：生产
// 写入（medium 及以上）仅 admin 可发起。
func maximumRisk(role string) tools.RiskLevel {
	switch role {
	case "admin":
		return tools.High
	case "operator":
		return tools.Low
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
