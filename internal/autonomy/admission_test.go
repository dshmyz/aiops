package autonomy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func testUser() identity.CurrentUser {
	return identity.CurrentUser{Subject: "admin-1", Roles: []string{"admin"}}
}

func lowRiskWriteTool() tools.Tool {
	return tools.Tool{Name: "demo.lowrisk.write", Operation: tools.Write, Risk: tools.Low}
}

func permittedDecision(tool tools.Tool) policy.Decision {
	return policy.Decision{Allowed: true, RequiresConfirmation: true, Tool: tool}
}

func enabledConfig() Config {
	return Config{
		Enabled:      true,
		DailyLimit:   100,
		LowRiskTools: map[string]bool{"demo.lowrisk.write": true},
	}
}

// fakeLimiter is a configurable DailyLimiter for unit tests. today returned by
// CountToday governs the daily-limit gate; count increments on Increment.
type fakeLimiter struct {
	count   int
	days    map[string]int
	err     error
	lastKey string
}

func (f *fakeLimiter) CountToday(_ context.Context, subject string, today time.Time) (int, error) {
	if f.err != nil {
		return 0, f.err
	}
	f.lastKey = subject + "_" + today.Format("20060102")
	return f.days[f.lastKey], nil
}

func (f *fakeLimiter) Increment(_ context.Context, subject string, today time.Time) (int, error) {
	k := subject + "_" + today.Format("20060102")
	f.days[k]++
	return f.days[k], nil
}

func TestAdmitFailsClosedWhenMasterSwitchOff(t *testing.T) {
	cfg := enabledConfig()
	cfg.Enabled = false
	c := NewController(cfg, nil)
	if err := c.Admit(context.Background(), testUser(), lowRiskWriteTool(), permittedDecision(lowRiskWriteTool())); !errors.Is(err, ErrDenied) {
		t.Fatalf("Admit = %v, want ErrDenied (master switch off)", err)
	}
}

func TestAdmitDeniesNonWriteOperation(t *testing.T) {
	read := tools.Tool{Name: "demo.read", Operation: tools.Read, Risk: tools.Low}
	cfg := enabledConfig()
	cfg.LowRiskTools = map[string]bool{"demo.read": true}
	c := NewController(cfg, nil)
	if err := c.Admit(context.Background(), testUser(), read, permittedDecision(read)); !errors.Is(err, ErrDenied) {
		t.Fatalf("Admit = %v, want ErrDenied (read is not a write)", err)
	}
}

func TestAdmitDeniesToolNotInWhitelist(t *testing.T) {
	c := NewController(enabledConfig(), nil)
	tool := lowRiskWriteTool()
	if err := c.Admit(context.Background(), testUser(), tool, permittedDecision(tool)); err != nil {
		t.Fatalf("Admit should allow whitelisted low-risk tool, got %v", err)
	}
	other := tools.Tool{Name: "other.lowrisk.write", Operation: tools.Write, Risk: tools.Low}
	if err := c.Admit(context.Background(), testUser(), other, permittedDecision(other)); !errors.Is(err, ErrDenied) {
		t.Fatalf("Admit = %v, want ErrDenied (tool not whitelisted)", err)
	}
}

func TestAdmitDeniesNonLowRiskTool(t *testing.T) {
	medium := tools.Tool{Name: "demo.lowrisk.write", Operation: tools.Write, Risk: tools.Medium}
	cfg := enabledConfig()
	cfg.LowRiskTools = map[string]bool{"demo.lowrisk.write": true}
	c := NewController(cfg, nil)
	if err := c.Admit(context.Background(), testUser(), medium, permittedDecision(medium)); !errors.Is(err, ErrDenied) {
		t.Fatalf("Admit = %v, want ErrDenied (tool risk not low)", err)
	}
}

func TestAdmitDeniesWhenPolicyDenies(t *testing.T) {
	c := NewController(enabledConfig(), nil)
	tool := lowRiskWriteTool()
	denied := policy.Decision{Allowed: false, RequiresConfirmation: true, Tool: tool}
	if err := c.Admit(context.Background(), testUser(), tool, denied); !errors.Is(err, ErrDenied) {
		t.Fatalf("Admit = %v, want ErrDenied (policy denies)", err)
	}
}

func TestAdmitAllowsWithinDailyLimit(t *testing.T) {
	fl := &fakeLimiter{days: map[string]int{}}
	c := NewController(enabledConfig(), fl)
	c.WithClock(func() time.Time { return time.Date(2026, time.August, 7, 10, 0, 0, 0, time.UTC) })
	tool := lowRiskWriteTool()
	if err := c.Admit(context.Background(), testUser(), tool, permittedDecision(tool)); err != nil {
		t.Fatalf("Admit = %v, want allow within daily limit", err)
	}
}

func TestAdmitDeniesWhenDailyLimitReached(t *testing.T) {
	fl := &fakeLimiter{days: map[string]int{"admin-1_20260807": 100}}
	c := NewController(enabledConfig(), fl)
	c.WithClock(func() time.Time { return time.Date(2026, time.August, 7, 10, 0, 0, 0, time.UTC) })
	tool := lowRiskWriteTool()
	if err := c.Admit(context.Background(), testUser(), tool, permittedDecision(tool)); !errors.Is(err, ErrDenied) {
		t.Fatalf("Admit = %v, want ErrDenied (daily limit 100 reached)", err)
	}
}

func TestAdmitAllowsWhenDailyLimitDisabled(t *testing.T) {
	cfg := enabledConfig()
	cfg.DailyLimit = 0 // 不限制
	c := NewController(cfg, nil)
	tool := lowRiskWriteTool()
	if err := c.Admit(context.Background(), testUser(), tool, permittedDecision(tool)); err != nil {
		t.Fatalf("Admit = %v, want allow when daily limit disabled", err)
	}
}

func TestAdmitDailyLimitResetsPerNaturalDay(t *testing.T) {
	fl := &fakeLimiter{days: map[string]int{"admin-1_20260807": 100}}
	c := NewController(enabledConfig(), fl)
	// 次日 —— 计数 key 不同，准入放行
	c.WithClock(func() time.Time { return time.Date(2026, time.August, 8, 9, 0, 0, 0, time.UTC) })
	tool := lowRiskWriteTool()
	if err := c.Admit(context.Background(), testUser(), tool, permittedDecision(tool)); err != nil {
		t.Fatalf("Admit = %v, want allow on a new natural day", err)
	}
}

func TestRecordIncrementsDailyCount(t *testing.T) {
	fl := &fakeLimiter{days: map[string]int{}}
	c := NewController(enabledConfig(), fl)
	c.WithClock(func() time.Time { return time.Date(2026, time.August, 7, 10, 0, 0, 0, time.UTC) })
	n := c.Record(context.Background(), testUser())
	if n != 1 {
		t.Fatalf("Record = %d, want 1", n)
	}
	if fl.days["admin-1_20260807"] != 1 {
		t.Fatalf("daily count = %d, want 1", fl.days["admin-1_20260807"])
	}
}

func TestConfigFromEnvDefaults(t *testing.T) {
	cfg, err := ConfigFromEnv(func(string) string { return "" })
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if cfg.Enabled {
		t.Error("Enabled should default false (fail-closed)")
	}
	if cfg.DailyLimit != 100 {
		t.Errorf("DailyLimit = %d, want 100", cfg.DailyLimit)
	}
	if len(cfg.LowRiskTools) != 0 {
		t.Error("empty env should produce an empty whitelist")
	}
}

func TestConfigFromEnvParses(t *testing.T) {
	env := map[string]string{
		"COPILOT_AUTONOMY_ENABLED":        "1",
		"COPILOT_AUTONOMY_DAILY_LIMIT":    "50",
		"COPILOT_AUTONOMY_LOW_RISK_TOOLS": "a.b.write,c.d.write",
	}
	cfg, err := ConfigFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("ConfigFromEnv: %v", err)
	}
	if !cfg.Enabled {
		t.Error("Enabled should be true")
	}
	if cfg.DailyLimit != 50 {
		t.Errorf("DailyLimit = %d, want 50", cfg.DailyLimit)
	}
	if !cfg.LowRiskTools["a.b.write"] || !cfg.LowRiskTools["c.d.write"] {
		t.Errorf("whitelist = %v, want both tools", cfg.LowRiskTools)
	}
}

func TestConfigFromEnvRejectsInvalid(t *testing.T) {
	if _, err := ConfigFromEnv(func(k string) string {
		if k == "COPILOT_AUTONOMY_ENABLED" {
			return "notabool"
		}
		return ""
	}); err == nil {
		t.Fatal("invalid COPILOT_AUTONOMY_ENABLED should error")
	}
	if _, err := ConfigFromEnv(func(k string) string {
		if k == "COPILOT_AUTONOMY_DAILY_LIMIT" {
			return "abc"
		}
		return ""
	}); err == nil {
		t.Fatal("invalid COPILOT_AUTONOMY_DAILY_LIMIT should error")
	}
}
