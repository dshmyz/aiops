package assistant

import (
	"context"
)

// Supervisor 是通用助手入口：把用户消息交给 supervisor 角色的执行循环。
//
// 设计原则：
//   - 角色是执行期显式注入的（RunWithRole），不在此处按消息关键词分派——
//     关键词→角色映射是写死的组件/意图词，已移除；需要特定角色时调用方
//     直接走 RunWithRole。
//   - 复用同一 AgentExecutor 的循环，supervisor 角色即通用助手提示词
//     （见 agent_role.go），行为与直接调用 AgentExecutor.Run 完全一致。
//   - Dispatch 内只做只读路由，不持有可变共享状态，可安全并发调用。
type Supervisor struct {
	exec *AgentExecutor
}

// NewSupervisor 创建编排者。exec 为 nil 时 Dispatch 返回 nil 结果
// （调用方应像对待未注入 agentExecutor 一样回退旧路径）。
func NewSupervisor(exec *AgentExecutor) *Supervisor {
	return &Supervisor{exec: exec}
}

// Dispatch 执行一次通用分派：以 supervisor 角色交给执行循环。
// onStep 为 nil 时静默执行（等价于 Run）。
func (s *Supervisor) Dispatch(ctx context.Context, message string, history []Turn, onStep func(AgentStepEvent)) *AgentRunResult {
	if s == nil || s.exec == nil {
		return nil
	}
	return s.exec.RunWithRoleCallback(ctx, RoleSupervisor, message, history, onStep)
}
