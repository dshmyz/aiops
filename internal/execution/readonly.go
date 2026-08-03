package execution

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// tracer returns the execution package's instrumentation scope.
func tracer() trace.Tracer {
	return otel.Tracer("github.com/gracegaoya/ai-operations-copilot/internal/execution")
}

var (
	ErrReadToolDenied = errors.New("read tool denied")
	ErrWriteTool      = errors.New("write tools are not allowed on the read endpoint")
)

type ReadRunner interface {
	Read(context.Context, tools.Tool, map[string]any) (map[string]any, error)
}

// defaultReadTimeout 是工具执行层的默认单次读取超时（§5 工具执行边界）。
// HTTP 路由的 readTimeout=5s 经 request ctx 传入时会被 context.WithTimeout
// 取 min 合并，因此不会放宽既有 HTTP 超时。
const defaultReadTimeout = 5 * time.Second

type ReadOnlyService struct {
	runner  ReadRunner
	audit   *audit.Service
	now     func() time.Time
	timeout time.Duration // <= 0 时不套超时（向后兼容）
}

func NewReadOnlyService(runner ReadRunner, auditService *audit.Service) *ReadOnlyService {
	return &ReadOnlyService{
		runner:  runner,
		audit:   auditService,
		now:     func() time.Time { return time.Now().UTC() },
		timeout: defaultReadTimeout,
	}
}

// WithTimeout 覆盖单次读取的 deadline。d <= 0 关闭超时封装。
func (s *ReadOnlyService) WithTimeout(d time.Duration) *ReadOnlyService {
	s.timeout = d
	return s
}

// readCtx 在 s.timeout > 0 时对 ctx 施加 deadline。context.WithTimeout 保留
// 父 ctx 更短的 deadline，因此 HTTP request ctx 已有的 5s 超时不会被放大。
func (s *ReadOnlyService) readCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	if s.timeout > 0 {
		return context.WithTimeout(ctx, s.timeout)
	}
	return ctx, func() {}
}

func (s *ReadOnlyService) ExecuteRead(ctx context.Context, user identity.CurrentUser, toolName string, input map[string]any) (map[string]any, error) {
	ctx, span := tracer().Start(ctx, "execution.ExecuteRead",
		trace.WithAttributes(attribute.String("tool.name", toolName)))
	defer span.End()
	tool, ok := tools.Lookup(toolName)
	if !ok {
		_ = s.record(ctx, user, toolName, audit.ActionReadonlyToolRejected, string(policy.ToolNotRegistered), nil)
		return nil, ErrReadToolDenied
	}
	if tool.Operation != tools.Read {
		_ = s.record(ctx, user, toolName, audit.ActionReadonlyToolRejected, audit.DecisionWriteToolNotAllowedOnRead, nil)
		return nil, ErrWriteTool
	}
	decision := policy.Evaluate(user, tool, input)
	if !decision.Allowed {
		_ = s.record(ctx, user, toolName, audit.ActionReadonlyToolRejected, string(decision.Reason), nil)
		return nil, ErrReadToolDenied
	}
	if s.runner == nil {
		_ = s.record(ctx, user, toolName, audit.ActionReadonlyToolFailed, audit.DecisionExecutorMissing, nil)
		return nil, errors.New("read runner is required")
	}
	readCtx, cancel := s.readCtx(ctx)
	defer cancel()
	result, err := s.runner.Read(readCtx, decision.Tool, input)
	if err != nil {
		metadata := map[string]any{}
		if errors.Is(err, context.DeadlineExceeded) {
			metadata["reason"] = "timeout"
			metadata["timeout"] = s.timeout.String()
		}
		_ = s.record(ctx, user, toolName, audit.ActionReadonlyToolFailed, audit.DecisionExecutionError, metadata)
		return nil, err
	}
	if err := s.record(ctx, user, toolName, audit.ActionReadonlyToolExecuted, audit.DecisionPermitted, map[string]any{"result": "returned_to_caller"}); err != nil {
		return nil, err
	}
	return result, nil
}

// ExecuteTrustedRead 直接调用 runner 执行只读 capability，跳过 policy 鉴权。
// 仅供可信内部调用方（如 scheduler）使用：定时任务由 admin 配置时已完成鉴权，
// 此处不再重复校验。未注册的 capability 会构造一个最小只读 Tool 描述符传给 runner。
func (s *ReadOnlyService) ExecuteTrustedRead(ctx context.Context, toolName string, input map[string]any) (map[string]any, error) {
	if s.runner == nil {
		return nil, errors.New("read runner is required")
	}
	tool, ok := tools.Lookup(toolName)
	if !ok {
		tool = tools.Tool{Name: toolName, Operation: tools.Read, Risk: tools.Low}
	}
	readCtx, cancel := s.readCtx(ctx)
	defer cancel()
	return s.runner.Read(readCtx, tool, input)
}

func (s *ReadOnlyService) record(ctx context.Context, user identity.CurrentUser, toolName, action, decision string, metadata map[string]any) error {
	if s.audit == nil {
		return nil
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	return s.audit.Record(ctx, audit.Event{
		ID:        newAuditID(),
		RequestID: user.RequestID,
		Subject:   user.Subject,
		ToolName:  toolName,
		Action:    action,
		Decision:  decision,
		Metadata:  metadata,
		CreatedAt: s.now(),
	})
}

func newAuditID() string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		panic("secure random source unavailable: " + err.Error())
	}
	return hex.EncodeToString(value)
}
