package audit

// Action enumerates the audit events recorded by the copilot pipeline.
// Stored verbatim in copilot_audit_events.action; only these values are
// accepted by Service.Record so downstream queries and analytics can rely on
// a closed set rather than parsing free-form strings.
const (
	ActionPlanCreated         = "plan_created"
	ActionPlanConfirmed       = "plan_confirmed"
	ActionPlanRejected        = "plan_rejected"
	ActionExecutionStarted    = "execution_started"
	ActionExecutionReused     = "execution_reused"
	ActionExecutionSucceeded  = "execution_succeeded"
	ActionExecutionFailed     = "execution_failed"
	ActionExecutionRejected   = "execution_rejected"
	ActionReadonlyToolExecuted  = "readonly_tool_executed"
	ActionReadonlyToolRejected  = "readonly_tool_rejected"
	// ActionToolExecuted 记录 agent 执行路径（AgentExecutor 的 LLM 工具调用）
	// 的一次工具执行。修复前 CapabilityTool 用字符串字面量 "tool_executed"
	// 记账，但该值不在本枚举里，Record 校验直接拒绝、调用方 `_ =` 吞错——
	// 主执行路径的工具执行从未真正落过审计。
	ActionToolExecuted = "tool_executed"
	ActionReadonlyToolFailed    = "readonly_tool_failed"
	ActionScheduledTaskSucceeded = "scheduled_task_succeeded"
	ActionScheduledTaskFailed    = "scheduled_task_failed"
	// ActionScheduledTaskSkipped 表示任务到期超过 maxLag 被跳过执行（专项：任务准-并发安全）。
	// 仅推进 next_run_at + 记 audit，不执行 capability，避免执行过时巡检。
	ActionScheduledTaskSkipped = "scheduled_task_skipped"
	// MCP 相关事件（借鉴-6: 外部 MCP 健康检查）。
	ActionMCPHealthUnhealthy = "mcp_health_unhealthy"
	ActionMCPHealthDegraded  = "mcp_health_degraded"
	ActionMCPToolsChanged    = "mcp_tools_changed"
	// 告警 webhook 接入事件（告警准专项）：收到/拒绝外部系统推送的告警。
	// 无用户 JWT 的接入点用 Subject 记录来源系统名，保证可追溯。
	ActionAlertIngested = "alert_ingested"
	ActionAlertRejected = "alert_rejected"
	// ActionLLMInvoked 记录一次 LLM 调用（缺口-5 / R1：模型调用与成本审计）。
	// Metadata 携带 model、prompt_tokens、completion_tokens、latency_ms，
	// 用于按模型聚合 AIOps 的 LLM 成本。
	ActionLLMInvoked = "llm_invoked"
	// ActionHTTPForbidden 记录一次 HTTP 403 权限拒绝（R2：HTTP 权限拒绝审计）。
	// Metadata 携带 path 与 reason；Subject 为被拒调用者。
	ActionHTTPForbidden = "http_forbidden"
	// ActionAuthLogin / ActionAuthLogout 记录登录/登出（R3：登录登出审计）。
	// Subject 为登录/登出的用户；logout 从 session cookie 恢复身份。
	ActionAuthLogin  = "auth_login"
	ActionAuthLogout = "auth_logout"
)

var allowedActions = map[string]struct{}{
	ActionPlanCreated:            {},
	ActionPlanConfirmed:          {},
	ActionPlanRejected:           {},
	ActionExecutionStarted:       {},
	ActionExecutionReused:        {},
	ActionExecutionSucceeded:     {},
	ActionExecutionFailed:        {},
	ActionExecutionRejected:      {},
	ActionToolExecuted:           {},
	ActionReadonlyToolExecuted:   {},
	ActionReadonlyToolRejected:   {},
	ActionReadonlyToolFailed:     {},
	ActionScheduledTaskSucceeded: {},
	ActionScheduledTaskFailed:    {},
	ActionScheduledTaskSkipped:   {},
	ActionMCPHealthUnhealthy:     {},
	ActionMCPHealthDegraded:      {},
	ActionMCPToolsChanged:        {},
	ActionAlertIngested:          {},
	ActionAlertRejected:          {},
	ActionLLMInvoked:             {},
	ActionHTTPForbidden:          {},
	ActionAuthLogin:              {},
	ActionAuthLogout:             {},
}

// Decision records why an action was allowed or denied. Stored verbatim in
// copilot_audit_events.decision.
const (
	DecisionPermitted                  = "permitted"
	DecisionDenied                     = "denied"
	DecisionImmutableInput             = "immutable_input"
	DecisionConfirmationRequired       = "confirmation_required"
	DecisionPlanExpired                = "plan_expired"
	DecisionExecutionError             = "execution_error"
	DecisionWriteToolNotAllowedOnRead  = "write_tool_not_allowed_on_read_endpoint"
	DecisionExecutorMissing            = "executor_missing"
	DecisionToolNotRegistered          = "tool_not_registered"
	DecisionPermissionDenied           = "permission_denied"
	DecisionInvalidInput               = "invalid_input"
	DecisionParameterDenied            = "parameter_denied"
	DecisionRiskDenied                 = "risk_denied"
)

var allowedDecisions = map[string]struct{}{
	DecisionPermitted:                 {},
	DecisionDenied:                    {},
	DecisionImmutableInput:            {},
	DecisionConfirmationRequired:      {},
	DecisionPlanExpired:               {},
	DecisionExecutionError:            {},
	DecisionWriteToolNotAllowedOnRead: {},
	DecisionExecutorMissing:           {},
	DecisionToolNotRegistered:         {},
	DecisionPermissionDenied:          {},
	DecisionInvalidInput:              {},
	DecisionParameterDenied:           {},
	DecisionRiskDenied:                {},
}

// IsValidAction reports whether action is a recognized audit action enum.
func IsValidAction(action string) bool {
	_, ok := allowedActions[action]
	return ok
}

// IsValidDecision reports whether decision is a recognized audit decision enum.
func IsValidDecision(decision string) bool {
	_, ok := allowedDecisions[decision]
	return ok
}
