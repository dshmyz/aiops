package audit_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// flakyAuditStore 包装 ActionPlanStore，通过 failFn 控制 AppendAudit 的失败行为。
// failFn 接收当前调用序号（从 1 开始），返回非 nil error 则直接返回不落库。
// 用于 R6 防丢失机制的测试：模拟 DB 短暂抖动 / 长时间不可达 / 恢复。
type flakyAuditStore struct {
	store.ActionPlanStore
	mu     sync.Mutex
	failFn func(callCount int) error
	count  int
}

func (f *flakyAuditStore) AppendAudit(ctx context.Context, event store.AuditEvent) error {
	f.mu.Lock()
	f.count++
	n := f.count
	fn := f.failFn
	f.mu.Unlock()
	if fn != nil {
		if err := fn(n); err != nil {
			return err
		}
	}
	return f.ActionPlanStore.AppendAudit(ctx, event)
}

func (f *flakyAuditStore) setFailFn(fn func(callCount int) error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failFn = fn
}

func validEvent(id string) audit.Event {
	return audit.Event{
		ID:        id,
		PlanID:    "plan-test",
		ToolName:  "topic.retention.set",
		Action:    audit.ActionPlanCreated,
		Decision:  audit.DecisionPermitted,
		CreatedAt: time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC),
	}
}

// fallbackFiles 返回 dir 下的 fallback 文件名列表，用于断言落盘/重放结果。
func fallbackFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read fallback dir %q: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// writeFallbackFile 按实现约定的格式写入一个 fallback 文件：<dir>/<id>.json。
// 用于 TestRecordReplaysFallbackOnStartup，模拟上次进程崩溃前积压的事件。
func writeFallbackFile(t *testing.T, dir string, event audit.Event) {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, event.ID+".json"), data, 0o644); err != nil {
		t.Fatalf("write fallback file: %v", err)
	}
}

// TestRecordRetriesOnTransientStoreError 验证：DB 第一次写入失败、第二次成功时，
// Record 通过同步重试把事件写入 DB，不落盘 fallback 文件（R6 防丢失 - 重试路径）。
func TestRecordRetriesOnTransientStoreError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	repo := store.NewMemoryActionPlanStore()
	transient := errors.New("transient db error")
	flaky := &flakyAuditStore{
		ActionPlanStore: repo,
		failFn: func(n int) error {
			if n == 1 {
				return transient
			}
			return nil
		},
	}
	svc := audit.NewService(flaky).WithFallback(audit.FallbackConfig{
		Dir:            dir,
		MaxRetries:     3,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
		ReplayInterval: time.Hour, // 不触发周期重放，只测同步重试
	})
	defer svc.Close()

	if err := svc.Record(context.Background(), validEvent("audit-retry-1")); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if got := len(repo.AuditEvents()); got != 1 {
		t.Fatalf("audit events = %d, want 1 (retry should succeed)", got)
	}
	if files := fallbackFiles(t, dir); len(files) != 0 {
		t.Fatalf("fallback files = %v, want 0 (retry succeeded, no fallback expected)", files)
	}
}

// TestRecordFallsBackToLocalFileWhenAllRetriesFail 验证：重试全部失败后，
// Record 把事件序列化写入 fallback 文件并返回 nil（不向上抛错），
// 调用方无感知，事件不丢失（R6 防丢失 - 落盘路径）。
func TestRecordFallsBackToLocalFileWhenAllRetriesFail(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	repo := store.NewMemoryActionPlanStore()
	dbDown := errors.New("db down")
	flaky := &flakyAuditStore{
		ActionPlanStore: repo,
		failFn:          func(n int) error { return dbDown }, // 永久失败
	}
	svc := audit.NewService(flaky).WithFallback(audit.FallbackConfig{
		Dir:            dir,
		MaxRetries:     2,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
		ReplayInterval: time.Hour,
	})
	defer svc.Close()

	if err := svc.Record(context.Background(), validEvent("audit-fb-1")); err != nil {
		t.Fatalf("Record should not surface error when fallback succeeds: %v", err)
	}
	if got := len(repo.AuditEvents()); got != 0 {
		t.Fatalf("audit events = %d, want 0 (DB was down)", got)
	}
	files := fallbackFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("fallback files = %v, want 1", files)
	}
	// 文件内容必须是合法的 Event JSON
	data, err := os.ReadFile(filepath.Join(dir, files[0]))
	if err != nil {
		t.Fatalf("read fallback file: %v", err)
	}
	var got audit.Event
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal fallback file: %v", err)
	}
	if got.ID != "audit-fb-1" {
		t.Fatalf("fallback event ID = %q, want %q", got.ID, "audit-fb-1")
	}
}

// TestRecordReplaysFallbackFileOnSubsequentSuccess 验证：DB 恢复后，
// 后台重放 goroutine 把 fallback 文件中的事件写回 DB 并删除文件（R6 防丢失 - 重放路径）。
func TestRecordReplaysFallbackFileOnSubsequentSuccess(t *testing.T) {
	// 不并行：涉及后台 goroutine + 动态切换 failFn + 轮询等待。
	dir := t.TempDir()
	repo := store.NewMemoryActionPlanStore()
	dbDown := errors.New("db down")
	flaky := &flakyAuditStore{
		ActionPlanStore: repo,
		failFn:          func(n int) error { return dbDown },
	}
	svc := audit.NewService(flaky).WithFallback(audit.FallbackConfig{
		Dir:            dir,
		MaxRetries:     1,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
		ReplayInterval: 20 * time.Millisecond, // 短间隔，快速触发重放
	})
	defer svc.Close()

	// DB 宕机时 Record，事件落盘
	if err := svc.Record(context.Background(), validEvent("audit-replay-1")); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if len(repo.AuditEvents()) != 0 {
		t.Fatalf("audit events = %d, want 0 before replay", len(repo.AuditEvents()))
	}

	// DB 恢复：failFn 改为永远成功
	flaky.setFailFn(nil)

	// 轮询等待后台重放（最多 1 秒）
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(repo.AuditEvents()) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := len(repo.AuditEvents()); got != 1 {
		t.Fatalf("audit events = %d, want 1 after replay", got)
	}
	if files := fallbackFiles(t, dir); len(files) != 0 {
		t.Fatalf("fallback files = %v, want 0 after replay", files)
	}
}

// TestRecordReplaysFallbackOnStartup 验证：Service 启动时立即扫描 fallback 目录，
// 把上次进程崩溃前积压的事件重放回 DB（R6 防丢失 - 跨进程不丢）。
func TestRecordReplaysFallbackOnStartup(t *testing.T) {
	// 不并行：依赖启动时首次重放的时序。
	dir := t.TempDir()
	// 预先写入一个 fallback 文件，模拟上次进程崩溃前的积压
	writeFallbackFile(t, dir, validEvent("audit-startup-1"))

	repo := store.NewMemoryActionPlanStore() // DB 已正常
	svc := audit.NewService(repo).WithFallback(audit.FallbackConfig{
		Dir:            dir,
		MaxRetries:     1,
		InitialBackoff: time.Millisecond,
		MaxBackoff:     time.Millisecond,
		ReplayInterval: time.Hour, // 只靠启动时首次重放，不靠周期
	})
	defer svc.Close()

	// 启动时首次重放是异步的，轮询等待（最多 1 秒）
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(repo.AuditEvents()) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := len(repo.AuditEvents()); got != 1 {
		t.Fatalf("audit events = %d, want 1 after startup replay", got)
	}
	if files := fallbackFiles(t, dir); len(files) != 0 {
		t.Fatalf("fallback files = %v, want 0 after startup replay", files)
	}
}
