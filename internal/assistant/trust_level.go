package assistant

import (
	"os"
	"strings"
	"sync/atomic"
)

// TrustLevel 控制 agent 的执行权限梯度。
type TrustLevel string

const (
	// TrustReadonly：只读模式，写工具一律拒绝。
	TrustReadonly TrustLevel = "readonly"
	// TrustConfirm：可写但必须人工确认（默认）。
	TrustConfirm TrustLevel = "confirm"
)

// trustLevel 当前信任等级（原子读写，支持运行时调整）。
var trustLevel atomic.Value

func init() {
	trustLevel.Store(TrustConfirm)
	// 启动时从环境变量读取
	if v := strings.TrimSpace(os.Getenv("COPILOT_AGENT_TRUST_LEVEL")); v != "" {
		SetTrustLevel(TrustLevel(strings.ToLower(v)))
	}
}

// SetTrustLevel 设置信任等级（readonly/confirm）。未知值回落 confirm。
func SetTrustLevel(level TrustLevel) {
	switch level {
	case TrustReadonly, TrustConfirm:
		trustLevel.Store(level)
	default:
		trustLevel.Store(TrustConfirm)
	}
}

// GetTrustLevel 返回当前信任等级。
func GetTrustLevel() TrustLevel {
	return trustLevel.Load().(TrustLevel)
}

// AllowWrite 判断当前信任等级是否允许写操作。
func AllowWrite() bool {
	return GetTrustLevel() != TrustReadonly
}
