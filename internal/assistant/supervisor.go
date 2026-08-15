package assistant

import (
	"context"
)

// Supervisor 是顶层编排者：把用户消息路由到 Action，按 Action 的
// AgentRole 分派给对应角色的执行循环。
//
// 设计原则：
//   - 复用同一 AgentExecutor 的 ReAct 循环，角色只改变 system prompt 边界
//     （角色提示词见 agent_role.go），不引入独立进程/模型，无额外调度开销
//   - 未命中任何 Action 或角色为空时回退 RoleSupervisor（通用助手），
//     行为与直接调用 AgentExecutor 完全一致（向后兼容）
//   - Dispatch 内只做只读路由，不持有可变共享状态，可安全并发调用
type Supervisor struct {
	exec *AgentExecutor
}

// NewSupervisor 创建编排者。exec 为 nil 时 Dispatch 返回 nil 结果
// （调用方应像对待未注入 agentExecutor 一样回退旧路径）。
func NewSupervisor(exec *AgentExecutor) *Supervisor {
	return &Supervisor{exec: exec}
}

// Dispatch 执行一次分派：路由消息 → 取角色 → 交给执行循环。
// onStep 为 nil 时静默执行（等价于 Run）。
func (s *Supervisor) Dispatch(ctx context.Context, message string, history []Turn, onStep func(AgentStepEvent)) *AgentRunResult {
	if s == nil || s.exec == nil {
		return nil
	}
	role := RoleSupervisor
	if action, ok := LookupAction(message); ok && action.AgentRole != "" {
		role = action.AgentRole
	}
	return s.exec.RunWithRoleCallback(ctx, role, message, history, onStep)
}
