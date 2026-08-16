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
	return s.DispatchStream(ctx, message, history, onStep, nil, nil)
}

// DispatchStream 与 Dispatch 相同，额外把最终答案的流式 token 转发给 onDelta、
// 把模型推理 token 实时转发给 onThinking。两路都由 executor 承担：onDelta 在
// 终轮 flush（每轮都流式），onThinking 把推理逐 chunk 转发，前端实时显示思考。
func (s *Supervisor) DispatchStream(ctx context.Context, message string, history []Turn, onStep func(AgentStepEvent), onDelta func(string), onThinking func(string)) *AgentRunResult {
	if s == nil || s.exec == nil {
		return nil
	}
	return s.exec.RunWithRoleCallbackStream(ctx, RoleSupervisor, message, history, onStep, onDelta, onThinking)
}
