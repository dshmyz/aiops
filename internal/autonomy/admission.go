// Package autonomy 承载「AI 让渡部分写操作自主执行」的准入控制（E2）。
//
// 设计目标（见 specs/2026-08-07-ops-overview-autonomy-design.md §5）：
//   - 所有自动执行源（直接对话低风险 runbook / 定时触发 / agent loop 低风险写）
//     共用同一组准入语义：总开关 + 低风险判定 + 工具白名单 + 每日上限。
//   - 默认 fail-closed：COPILOT_AUTONOMY_ENABLED 未明确开启时，任何机器写都
//     不自动执行，退回人工确认。
package autonomy

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// Source 标识一次自动执行的来源，写进执行/审计用于回溯。
type Source string

const (
	SourceDirect    Source = "direct"     // 直接对话低风险 runbook
	SourceScheduler Source = "scheduler"  // 定时触发的低风险 runbook（Phase 3）
	SourceAgentLoop Source = "agent-loop" // agent loop 内低风险写
)

// ErrDenied 表示一次自动执行被准入门拒绝（总开关/风险/白名单/每日上限）。调用方
// 应把它当作「退回人工确认」，而不是错误回滚整个请求。
var ErrDenied = errors.New("autonomous execution denied")

// DailyLimiter 记录每个主体当日自动执行次数，用于每日上限防刷。生产用带持久化的
// 实现（SQL 计数按自然日滚动）；测试用内存实现注入。
type DailyLimiter interface {
	// CountToday 返回主体 today 的自动执行次数（不含本闪）。消费方在放行前调用。
	CountToday(ctx context.Context, subject string, today time.Time) (int, error)
	// Increment 原子地在自然日 key 上 +1。返回当天最新的累计值。
	Increment(ctx context.Context, subject string, today time.Time) (int, error)
}

// Config 是准入控制的静态配置，来自环境变量（cmd/copilot-api/main.go 装配）。
type Config struct {
	// Enabled 是否开启自动执行。false（默认）= 一切自动执行禁用（fail-closed）。
	Enabled bool
	// DailyLimit 每个主体每日自动执行上限。<=0 表示不限制（不建议生产使用）。
	DailyLimit int
	// LowRiskTools 白名单：仅这些工具在判定低风险时允许自动执行。为空表示
	// 未配置任何自动执行工具（安全默认：不自动执行）。
	LowRiskTools map[string]bool
}

// Controller 是 Low-Risk Admission Controller。所有自动执行源先过 Admit 再执行。
type Controller struct {
	cfg     Config
	limiter DailyLimiter
	clock   func() time.Time
}

func NewController(cfg Config, limiter DailyLimiter) *Controller {
	if limiter == nil {
		limiter = NopLimiter{}
	}
	return &Controller{cfg: cfg, limiter: limiter, clock: time.Now}
}

// WithClock 覆写时钟（测试缝）。nil 被忽略。
func (c *Controller) WithClock(f func() time.Time) *Controller {
	if f != nil {
		c.clock = f
	}
	return c
}

// Admit 对一次低风险自动执行做准入判定。decision 由调用方用 policy.Evaluate 得到
// （与持久化边界一致）。tool 必须是 tools.Lookup 解析出的规范工具（非调用方伪造）。
//
// 返回 nil 表示放行（调用方可安全自动执行）；返回 ErrDenied 表示退回人工确认。
// 判定顺序（任一不过即拒，fail-closed）：
//  1. 总开关未开 → 拒
//  2. 非写操作 → 拒（只读不需要也不该自动「写」准入）
//  3. 工具不在白名单 → 拒
//  4. 工具风险非 low → 拒
//  5. policy 判定不写允许 → 拒
//  6. 每日上限已满 → 拒
//
// Admit 是裸写（无 runbook 模板）的严格门：工具自身风险必须为 low。需要按模板评审
// 风险放宽（工具可 Medium+，模板已评审为 low）的路径用 AdmitRunbook。
func (c *Controller) Admit(ctx context.Context, user identity.CurrentUser, tool tools.Tool, decision policy.Decision) error {
	return c.admit(ctx, user, tool, decision, func() error {
		if tool.Risk != tools.Low {
			return fmt.Errorf("%w: %s risk is %q, want low", ErrDenied, tool.Name, tool.Risk)
		}
		return nil
	})
}

// AdmitRunbook 对一次**模板评审过**的低风险 runbook 写做准入判定。与 Admit 的唯一
// 差异在第 4 步风险门：此处不要求工具自身 risk=low，而是接受调用方已验证的
// `templateRiskLow`——runbook 模板被显式评审为 low 即满足自动化授权。
//
// 理由（见设计 §5）：写能力被验证器强制 Medium+（internal/capabilities/validation.go：
// write risk must be medium or high），工具自身永远不可能是 low。因此低风险自动写
// 只能落在一个**评审单元**上——runbook 模板。模板 low + 工具在低风险白名单 + 工具
// 仍带 precheck/action-plan/approval governance（工具 Medium 不意味着绕过这些），
// 由模板评审负责把"这类动作够不够低风险去自动化"这条线守住。
//
// 其余门（总开关 / write / 白名单 / policy / 每日上限）与 Admit 完全一致，fail-closed。
func (c *Controller) AdmitRunbook(ctx context.Context, user identity.CurrentUser, tool tools.Tool, decision policy.Decision, templateRiskLow bool) error {
	return c.admit(ctx, user, tool, decision, func() error {
		if !templateRiskLow {
			return fmt.Errorf("%w: %s runbook template is not low risk (want reviewed-low for auto-execution)", ErrDenied, tool.Name)
		}
		return nil
	})
}

// admit 是共享的准入门序列（判定顺序见 Admit 注释）。riskGate 决定「风险门」如何判定：
// Admit 用它强制工具 low；AdmitRunbook 用它接受模板评审的 low。
func (c *Controller) admit(ctx context.Context, user identity.CurrentUser, tool tools.Tool, decision policy.Decision, riskGate func() error) error {
	if !c.cfg.Enabled {
		return fmt.Errorf("%w: autonomy master switch off", ErrDenied)
	}
	if tool.Operation != tools.Write {
		return fmt.Errorf("%w: %s is not a write tool", ErrDenied, tool.Name)
	}
	if !c.cfg.LowRiskTools[tool.Name] {
		return fmt.Errorf("%w: %s not in low-risk autowrite whitelist", ErrDenied, tool.Name)
	}
	if err := riskGate(); err != nil {
		return err
	}
	if !decision.Allowed {
		return fmt.Errorf("%w: policy denies %s", ErrDenied, tool.Name)
	}
	if c.cfg.DailyLimit > 0 {
		today := c.clock().UTC()
		count, err := c.limiter.CountToday(ctx, user.Subject, today)
		if err != nil {
			return fmt.Errorf("%w: count today: %v", ErrDenied, err)
		}
		if count >= c.cfg.DailyLimit {
			return fmt.Errorf("%w: daily limit %d reached", ErrDenied, c.cfg.DailyLimit)
		}
	}
	return nil
}

// Record 在自动执行后记录一次（每日计数 +1）。不阻塞：失败仅记录，不影响已执行的
// 结果。返回当天累计值（放行时已 +1）。
func (c *Controller) Record(ctx context.Context, user identity.CurrentUser) int {
	if c.cfg.DailyLimit <= 0 {
		return 0
	}
	n, err := c.limiter.Increment(ctx, user.Subject, c.clock().UTC())
	if err != nil {
		return 0
	}
	return n
}

// Enabled 返回总开关状态（供 dashboard/告警呈现「自动执行是否开启」）。
func (c *Controller) Enabled() bool { return c.cfg.Enabled }

// ConfigFromEnv 从环境变量构造 Config。失败（非法布尔/整数）返回 error，由
// main 在启动时退化处理（保持 fail-closed 默认）。
func ConfigFromEnv(lookup func(string) string) (Config, error) {
	cfg := Config{
		Enabled:      false, // fail-closed 默认
		DailyLimit:   100,   // 设计默认
		LowRiskTools: map[string]bool{},
	}
	if v := lookup("COPILOT_AUTONOMY_ENABLED"); v != "" {
		on, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("COPILOT_AUTONOMY_ENABLED: %w", err)
		}
		cfg.Enabled = on
	}
	if v := lookup("COPILOT_AUTONOMY_DAILY_LIMIT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return Config{}, fmt.Errorf("COPILOT_AUTONOMY_DAILY_LIMIT: %w", err)
		}
		cfg.DailyLimit = n
	}
	// 白名单：默认为空（不自动执行任何工具）。当前低风险 runbook 自动执行工具
	// 由 COPILOT_AUTONOMY_LOW_RISK_TOOLS（逗号分隔）显式声明，全得名单一来源。
	if v := lookup("COPILOT_AUTONOMY_LOW_RISK_TOOLS"); v != "" {
		for _, t := range splitCSV(v) {
			if t != "" {
				cfg.LowRiskTools[t] = true
			}
		}
	}
	return cfg, nil
}

func splitCSV(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}

// NopLimiter 是不做计数的 limiter（用于未装配/测试）。每日上限视为永不触发。
type NopLimiter struct{}

func (NopLimiter) CountToday(context.Context, string, time.Time) (int, error) { return 0, nil }
func (NopLimiter) Increment(context.Context, string, time.Time) (int, error)  { return 1, nil }
