package assistant

import "sync/atomic"

// killSwitch 全局原子开关。非零 = agent 已禁用。
var killSwitch int32

// DisableAgent 一键停止 agent：正在运行的循环会在下一步前终止。
func DisableAgent() {
	atomic.StoreInt32(&killSwitch, 1)
}

// EnableAgent 恢复 agent 执行。
func EnableAgent() {
	atomic.StoreInt32(&killSwitch, 0)
}

// AgentEnabled 返回 agent 是否允许执行。
func AgentEnabled() bool {
	return atomic.LoadInt32(&killSwitch) == 0
}
