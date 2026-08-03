package scheduler

import (
	"testing"
	"time"
)

// mustLoadLocation 加载时区，失败则 t.Fatalf。
func mustLoadLocation(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("load location %q: %v", name, err)
	}
	return loc
}

func TestComputeNextRunPreset5m(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 3, 0, 0, time.UTC)
	next, err := computeNextRun("preset", "5m", "", "UTC", now)
	if err != nil {
		t.Fatalf("computeNextRun 5m: %v", err)
	}
	want := time.Date(2026, time.July, 27, 10, 5, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}

	// 当前正好 10:05 → 下一次应为 10:10
	now = time.Date(2026, time.July, 27, 10, 5, 0, 0, time.UTC)
	next, err = computeNextRun("preset", "5m", "", "UTC", now)
	if err != nil {
		t.Fatalf("computeNextRun 5m on boundary: %v", err)
	}
	want = time.Date(2026, time.July, 27, 10, 10, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
}

func TestComputeNextRunPreset1h(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 30, 0, 0, time.UTC)
	next, err := computeNextRun("preset", "1h", "", "UTC", now)
	if err != nil {
		t.Fatalf("computeNextRun 1h: %v", err)
	}
	want := time.Date(2026, time.July, 27, 11, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}

	// 当前 11:00 → 下一次 12:00
	now = time.Date(2026, time.July, 27, 11, 0, 0, 0, time.UTC)
	next, err = computeNextRun("preset", "1h", "", "UTC", now)
	if err != nil {
		t.Fatalf("computeNextRun 1h on boundary: %v", err)
	}
	want = time.Date(2026, time.July, 27, 12, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
}

func TestComputeNextRunPresetDaily(t *testing.T) {
	t.Parallel()
	loc := mustLoadLocation(t, "Asia/Shanghai")
	// 2026-07-27 10:00 CST
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, loc)
	next, err := computeNextRun("preset", "daily", "", "Asia/Shanghai", now)
	if err != nil {
		t.Fatalf("computeNextRun daily: %v", err)
	}
	// 期望 2026-07-28 00:00 CST
	want := time.Date(2026, time.July, 28, 0, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
	if next.Location().String() != loc.String() {
		t.Fatalf("next location = %v, want %v", next.Location(), loc)
	}
}

func TestComputeNextRunPresetWeekly(t *testing.T) {
	t.Parallel()
	loc := mustLoadLocation(t, "Asia/Shanghai")
	// 2026-07-27 是周一
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, loc)
	next, err := computeNextRun("preset", "weekly", "", "Asia/Shanghai", now)
	if err != nil {
		t.Fatalf("computeNextRun weekly: %v", err)
	}
	// 期望下一个周一 2026-08-03 00:00 CST
	want := time.Date(2026, time.August, 3, 0, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}

	// 2026-07-26 周日 → 下周一 2026-07-27 00:00
	now = time.Date(2026, time.July, 26, 23, 59, 0, 0, loc)
	next, err = computeNextRun("preset", "weekly", "", "Asia/Shanghai", now)
	if err != nil {
		t.Fatalf("computeNextRun weekly sunday: %v", err)
	}
	want = time.Date(2026, time.July, 27, 0, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
}

func TestComputeNextRunCronWeekdayRange(t *testing.T) {
	t.Parallel()
	loc := mustLoadLocation(t, "Asia/Shanghai")
	// cron `0 2 * * 1-5`：周一 10:00 → 周二 02:00
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, loc) // 周一
	next, err := computeNextRun("cron", "", "0 2 * * 1-5", "Asia/Shanghai", now)
	if err != nil {
		t.Fatalf("computeNextRun cron weekday: %v", err)
	}
	want := time.Date(2026, time.July, 28, 2, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
}

func TestComputeNextRunCronEvery5Minutes(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 3, 0, 0, time.UTC)
	next, err := computeNextRun("cron", "", "*/5 * * * *", "UTC", now)
	if err != nil {
		t.Fatalf("computeNextRun */5: %v", err)
	}
	want := time.Date(2026, time.July, 27, 10, 5, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
}

func TestComputeNextRunCronFirstOfMonth(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	next, err := computeNextRun("cron", "", "0 0 1 * *", "UTC", now)
	if err != nil {
		t.Fatalf("computeNextRun 0 0 1 * *: %v", err)
	}
	want := time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
}

func TestComputeNextRunCronLeapYear(t *testing.T) {
	t.Parallel()
	// 2026 不是闰年，2 月只有 28 天。`0 0 29 2 *` 在 2026 年应跳到 2028 年 2 月 29 日
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	next, err := computeNextRun("cron", "", "0 0 29 2 *", "UTC", now)
	if err != nil {
		t.Fatalf("computeNextRun leap year: %v", err)
	}
	want := time.Date(2028, time.February, 29, 0, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
}

func TestComputeNextRunCronDailyDescriptor(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	next, err := computeNextRun("cron", "", "@daily", "UTC", now)
	if err != nil {
		t.Fatalf("computeNextRun @daily: %v", err)
	}
	want := time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
}

func TestComputeNextRunTimezoneCrossDay(t *testing.T) {
	t.Parallel()
	loc := mustLoadLocation(t, "Asia/Shanghai")
	// UTC 时间 2026-07-27 16:00，对应 CST 2026-07-28 00:00
	// daily 应返回 CST 次日 00:00，即 2026-07-29 00:00 CST
	now := time.Date(2026, time.July, 27, 16, 0, 0, 0, time.UTC)
	next, err := computeNextRun("preset", "daily", "", "Asia/Shanghai", now)
	if err != nil {
		t.Fatalf("computeNextRun daily cross-day: %v", err)
	}
	want := time.Date(2026, time.July, 29, 0, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
	if next.Location().String() != loc.String() {
		t.Fatalf("next location = %v, want %v", next.Location(), loc)
	}
}

func TestComputeNextRunEmptyTimezoneFallsBackToUTC(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	next, err := computeNextRun("preset", "daily", "", "", now)
	if err != nil {
		t.Fatalf("computeNextRun empty tz: %v", err)
	}
	want := time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
	if next.Location() != time.UTC {
		t.Fatalf("next location = %v, want UTC", next.Location())
	}
}

func TestComputeNextRunInvalidTimezoneFallsBackToUTC(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	next, err := computeNextRun("preset", "daily", "", "Invalid/Zone", now)
	if err != nil {
		t.Fatalf("computeNextRun invalid tz: %v", err)
	}
	want := time.Date(2026, time.July, 28, 0, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %v, want %v", next, want)
	}
}

func TestComputeNextRunInvalidCronReturnsError(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	_, err := computeNextRun("cron", "", "0 25 * * *", "UTC", now)
	if err == nil {
		t.Fatal("computeNextRun with invalid cron should return error")
	}
}

func TestComputeNextRunInvalidPresetReturnsError(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	_, err := computeNextRun("preset", "invalid", "", "UTC", now)
	if err == nil {
		t.Fatal("computeNextRun with invalid preset should return error")
	}
}

func TestComputeNextRunUnknownKindReturnsError(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 27, 10, 0, 0, 0, time.UTC)
	_, err := computeNextRun("unknown", "5m", "", "UTC", now)
	if err == nil {
		t.Fatal("computeNextRun with unknown kind should return error")
	}
}
