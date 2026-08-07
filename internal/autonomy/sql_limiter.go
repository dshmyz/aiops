package autonomy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// SQLDailyLimiter 是 DailyLimiter 的持久化实现，按 (subject, 自然日) 计数，
// 存于 autonomy_daily_limit 表（迁移 017）。多实例部署下靠 PK(subject, day)
// 的原子 upsert 保证并发自增不丢计数。
//
// day key 用 UTC 日期 YYYYMMDD（由调用方传入的 today 决定；Controller 以
// c.clock().UTC() 传入），因此按自然日滚动，跨天自动清零。
type SQLDailyLimiter struct {
	db     *sql.DB
	sqlite bool
}

// NewSQLDailyLimiter 创建基于 *sql.DB 的每日上限 limiter。
func NewSQLDailyLimiter(db *sql.DB) *SQLDailyLimiter {
	return &SQLDailyLimiter{db: db, sqlite: isSQLiteDriver(db)}
}

// isSQLiteDriver 通过探测 SQLite 特有的 PRAGMA 判断驱动，据此选用 upsert 方言。
// MySQL 执行 `PRAGMA` 会报错，据此区分两种写法。
func isSQLiteDriver(db *sql.DB) bool {
	row := db.QueryRow("PRAGMA journal_mode")
	var mode string
	if err := row.Scan(&mode); err != nil {
		return false
	}
	return true
}

// CountToday 返回主体 subject 在 today 的自然日内的自动执行次数。无记录返回 0。
func (l *SQLDailyLimiter) CountToday(ctx context.Context, subject string, today time.Time) (int, error) {
	day := dayKey(today)
	var count int
	err := l.db.QueryRowContext(ctx,
		`SELECT count FROM autonomy_daily_limit WHERE subject = ? AND day = ?`,
		subject, day).Scan(&count)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("count today: %w", err)
	}
	return count, nil
}

// Increment 原子地为 subject 在 today 的自然日 on 计数 +1，返回当天最新累计值。
// 首次（无记录）插入 count=1；已存在则 +1。多实例并发由 PK/subject,day) upsert 保证。
func (l *SQLDailyLimiter) Increment(ctx context.Context, subject string, today time.Time) (int, error) {
	day := dayKey(today)
	if l.sqlite {
		_, err := l.db.ExecContext(ctx,
			`INSERT INTO autonomy_daily_limit (subject, day, count, updated_at)
			 VALUES (?, ?, 1, CURRENT_TIMESTAMP)
			 ON CONFLICT(subject, day) DO UPDATE SET
				count = autonomy_daily_limit.count + 1,
				updated_at = CURRENT_TIMESTAMP`,
			subject, day)
		if err != nil {
			return 0, fmt.Errorf("increment daily limit (sqlite): %w", err)
		}
	} else {
		_, err := l.db.ExecContext(ctx,
			`INSERT INTO autonomy_daily_limit (subject, day, count, updated_at)
			 VALUES (?, ?, 1, CURRENT_TIMESTAMP(6))
			 ON DUPLICATE KEY UPDATE
				count = count + 1,
				updated_at = CURRENT_TIMESTAMP(6)`,
			subject, day)
		if err != nil {
			return 0, fmt.Errorf("increment daily limit: %w", err)
		}
	}
	return l.CountToday(ctx, subject, today)
}

// dayKey 把 UTC 时间格式化为 YYYYMMDD，作为自然日分桶键。
func dayKey(t time.Time) string {
	u := t.UTC()
	return fmt.Sprintf("%04d%02d%02d", u.Year(), int(u.Month()), u.Day())
}
