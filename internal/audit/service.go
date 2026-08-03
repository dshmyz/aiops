// Package audit records correlated, structured audit events.
package audit

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// tracer returns the audit package's instrumentation scope.
func tracer() trace.Tracer {
	return otel.Tracer("github.com/gracegaoya/ai-operations-copilot/internal/audit")
}

type Event = store.AuditEvent
type Filter = store.AuditFilter
type Page = store.AuditPage
type Cursor = store.AuditCursor

// Service 记录审计事件。启用 fallback 后（WithFallback），DB 写入失败会自动
// 重试 + 落盘兜底 + 后台重放，确保事件不丢失（R6 事件准 - 防丢失）。
type Service struct {
	store store.ActionPlanStore

	// fallback 为 nil 时退化为原始行为（直接 AppendAudit，不重试不兜底），
	// 保持未启用 fallback 场景的向后兼容。
	fallback *fallbackState

	// stopReplay 取消后台重放 goroutine 的 context。
	// 为 nil 表示未启用 fallback，Close 是 no-op。
	stopReplay context.CancelFunc
	// wg 跟踪后台重放 goroutine，Close 时 Wait 确保优雅退出。
	wg sync.WaitGroup
}

// NewService 创建一个不启用 fallback 的审计服务（向后兼容）。
// 启用防丢失机制需链式调用 WithFallback。
func NewService(repository store.ActionPlanStore) *Service {
	return &Service{store: repository}
}

// WithFallback 注入防丢失兜底配置并启动后台重放 goroutine。
// 返回 Service 自身以支持链式调用。重复调用会先停止旧 goroutine 再启动新的。
// 启用后必须调用 Close 释放 goroutine。
func (s *Service) WithFallback(cfg FallbackConfig) *Service {
	// 重复调用：先停止旧 goroutine，避免泄漏。
	if s.stopReplay != nil {
		s.stopReplay()
		s.wg.Wait()
	}
	s.fallback = &fallbackState{cfg: cfg.withDefaults(), clock: time.Now}
	ctx, cancel := context.WithCancel(context.Background())
	s.stopReplay = cancel
	s.startReplayLoop(ctx)
	return s
}

// Close 停止后台重放 goroutine 并等待其退出。
// 未启用 fallback 时为 no-op。可安全多次调用。
func (s *Service) Close() error {
	if s.stopReplay != nil {
		s.stopReplay()
		s.wg.Wait()
		s.stopReplay = nil
	}
	return nil
}

func (s *Service) Record(ctx context.Context, event Event) error {
	// Capture the caller's trace ID before creating the audit.Record span.
	// If we extracted from the post-Start ctx, every context without a parent
	// span would synthesize a brand-new trace ID that nobody else will ever
	// observe — making the correlation meaningless. Empty TraceID is the
	// honest signal that no trace exists for this event.
	if sc := trace.SpanContextFromContext(ctx); sc.HasTraceID() {
		event.TraceID = sc.TraceID().String()
	}

	ctx, span := tracer().Start(ctx, "audit.Record",
		trace.WithAttributes(
			attribute.String("audit.action", event.Action),
			attribute.String("audit.decision", event.Decision),
			attribute.String("audit.trace_id", event.TraceID),
		))
	defer span.End()
	if !IsValidAction(event.Action) {
		return fmt.Errorf("audit: invalid action %q", event.Action)
	}
	if !IsValidDecision(event.Decision) {
		return fmt.Errorf("audit: invalid decision %q", event.Decision)
	}
	if event.ID == "" {
		return errors.New("audit: event id is required")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}

	// 未启用 fallback：保持原始行为，直接写 DB。
	if s.fallback == nil {
		return s.store.AppendAudit(ctx, event)
	}

	// 启用 fallback：先同步重试，全失败则落盘兜底。
	// 重试覆盖 DB 短暂抖动；落盘覆盖 DB 较长时间不可达；后台重放在 DB 恢复后补齐。
	if err := s.appendWithRetry(ctx, event); err == nil {
		return nil
	}
	if writeErr := writeFallbackFile(s.fallback.cfg.Dir, event); writeErr != nil {
		// DB 写失败 + 落盘也失败：事件真的丢了，向上抛错让调用方感知。
		return fmt.Errorf("audit: persist failed and fallback write failed: %w", writeErr)
	}
	return nil
}

func (s *Service) List(ctx context.Context, filter Filter) (Page, error) {
	ctx, span := tracer().Start(ctx, "audit.List")
	defer span.End()
	return s.store.ListAudit(ctx, filter)
}
