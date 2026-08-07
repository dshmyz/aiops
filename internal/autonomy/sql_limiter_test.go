package autonomy

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// testSQLite opens an in-memory SQLite DB with the copilot schema applied.
func testSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", "file:"+t.Name()+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open sqlite test database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.ApplySQLiteMigrations(db); err != nil {
		t.Fatalf("apply sqlite migrations: %v", err)
	}
	return db
}

func day(t time.Time) time.Time { return t.UTC() }

func TestSQLDailyLimiterCountStartsAtZero(t *testing.T) {
	db := testSQLite(t)
	l := NewSQLDailyLimiter(db)
	now := time.Date(2026, time.August, 7, 10, 0, 0, 0, time.UTC)

	n, err := l.CountToday(context.Background(), "admin-1", now)
	if err != nil {
		t.Fatalf("CountToday: %v", err)
	}
	if n != 0 {
		t.Fatalf("CountToday = %d, want 0 (empty table)", n)
	}
}

func TestSQLDailyLimiterIncrementAndCount(t *testing.T) {
	db := testSQLite(t)
	l := NewSQLDailyLimiter(db)
	now := time.Date(2026, time.August, 7, 10, 0, 0, 0, time.UTC)

	for i := 1; i <= 3; i++ {
		n, err := l.Increment(context.Background(), "admin-1", now)
		if err != nil {
			t.Fatalf("Increment %d: %v", i, err)
		}
		if n != i {
			t.Fatalf("Increment %d returned %d, want %d", i, n, i)
		}
	}
	// CountToday 反映累计值（不含本闪前的计数一致）。
	n, err := l.CountToday(context.Background(), "admin-1", now)
	if err != nil {
		t.Fatalf("CountToday: %v", err)
	}
	if n != 3 {
		t.Fatalf("CountToday = %d, want 3", n)
	}
}

func TestSQLDailyLimiterSubjectIsolation(t *testing.T) {
	db := testSQLite(t)
	l := NewSQLDailyLimiter(db)
	now := time.Date(2026, time.August, 7, 10, 0, 0, 0, time.UTC)

	if _, err := l.Increment(context.Background(), "admin-1", now); err != nil {
		t.Fatalf("Increment admin-1: %v", err)
	}
	if _, err := l.Increment(context.Background(), "admin-2", now); err != nil {
		t.Fatalf("Increment admin-2: %v", err)
	}
	n1, _ := l.CountToday(context.Background(), "admin-1", now)
	n2, _ := l.CountToday(context.Background(), "admin-2", now)
	if n1 != 1 || n2 != 1 {
		t.Fatalf("subjects not isolated: admin-1=%d admin-2=%d, want 1 1", n1, n2)
	}
}

func TestSQLDailyLimiterResetsPerNaturalDay(t *testing.T) {
	db := testSQLite(t)
	l := NewSQLDailyLimiter(db)
	day1 := time.Date(2026, time.August, 7, 23, 59, 0, 0, time.UTC)
	day2 := time.Date(2026, time.August, 8, 0, 1, 0, 0, time.UTC)

	if _, err := l.Increment(context.Background(), "admin-1", day1); err != nil {
		t.Fatalf("Increment day1: %v", err)
	}
	// 次日 —— day key 不同，计数从 0 重新开始（count=1）。
	n2, err := l.Increment(context.Background(), "admin-1", day2)
	if err != nil {
		t.Fatalf("Increment day2: %v", err)
	}
	if n2 != 1 {
		t.Fatalf("next-natural-day Increment = %d, want 1 (reset)", n2)
	}
	n1, _ := l.CountToday(context.Background(), "admin-1", day1)
	if n1 != 1 {
		t.Fatalf("day1 count = %d, want 1 (unchanged)", n1)
	}
}

func TestSQLDailyLimiterDayKeyUsesUTCNotLocal(t *testing.T) {
	// 传入带非 UTC offset 的时间，day key 必须按 UTC 归一。
	local := time.Date(2026, time.August, 7, 10, 0, 0, 0, time.FixedZone("UTC+8", 8*3600))
	if k := dayKey(local); k != "20260807" {
		t.Fatalf("dayKey(local) = %q, want 20260807 (UTC-normalized)", k)
	}
}
