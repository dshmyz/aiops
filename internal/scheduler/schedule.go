// Package scheduler 周期性调度管理员配置的定时巡检任务，到期后调用只读 capability。
package scheduler

import (
	"errors"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

// computeNextRun 根据 schedule 配置计算 next_run_at。
//
// kind 取值 'preset' 或 'cron'：
//   - preset 模式读 preset（5m / 1h / daily / weekly）
//   - cron 模式读 cronExpr（标准 5 字段或 @daily 等描述符）
//
// timezone 为空或无效时回退 UTC。返回的 time.Time 携带目标时区信息。
func computeNextRun(kind, preset, cronExpr, timezone string, now time.Time) (time.Time, error) {
	loc, err := loadLocation(timezone)
	if err != nil {
		return time.Time{}, err
	}
	localNow := now.In(loc)
	switch kind {
	case "preset":
		return computePresetNextRun(preset, localNow)
	case "cron":
		return computeCronNextRun(cronExpr, localNow)
	default:
		return time.Time{}, fmt.Errorf("scheduler: unknown schedule kind %q", kind)
	}
}

func computePresetNextRun(preset string, now time.Time) (time.Time, error) {
	switch preset {
	case "5m":
		return now.Truncate(5 * time.Minute).Add(5 * time.Minute), nil
	case "1h":
		return now.Truncate(time.Hour).Add(time.Hour), nil
	case "daily":
		// 次日 00:00（按 timezone）
		year, month, day := now.Date()
		start := time.Date(year, month, day, 0, 0, 0, 0, now.Location())
		return start.Add(24 * time.Hour), nil
	case "weekly":
		// 下一个周一 00:00
		year, month, day := now.Date()
		start := time.Date(year, month, day, 0, 0, 0, 0, now.Location())
		daysSinceMonday := int(start.Weekday()) - int(time.Monday)
		if daysSinceMonday < 0 {
			daysSinceMonday += 7
		}
		// 若现在正好是周一 00:00（now == start），下一次应是 7 天后，而不是当下
		if now.Equal(start) {
			return start.Add(7 * 24 * time.Hour), nil
		}
		nextMonday := start.Add(time.Duration(7-daysSinceMonday) * 24 * time.Hour)
		if !nextMonday.After(now) {
			nextMonday = nextMonday.Add(7 * 24 * time.Hour)
		}
		return nextMonday, nil
	default:
		return time.Time{}, fmt.Errorf("scheduler: invalid preset %q", preset)
	}
}

func computeCronNextRun(cronExpr string, now time.Time) (time.Time, error) {
	if cronExpr == "" {
		return time.Time{}, errors.New("scheduler: cron expression is required for cron kind")
	}
	schedule, err := cron.ParseStandard(cronExpr)
	if err != nil {
		return time.Time{}, fmt.Errorf("scheduler: parse cron %q: %w", cronExpr, err)
	}
	next := schedule.Next(now)
	if next.IsZero() {
		return time.Time{}, fmt.Errorf("scheduler: cron %q produced no next run after %v", cronExpr, now)
	}
	return next, nil
}

func loadLocation(timezone string) (*time.Location, error) {
	if timezone == "" {
		return time.UTC, nil
	}
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		// 失败回退 UTC，与 spec 一致
		return time.UTC, nil
	}
	return loc, nil
}
