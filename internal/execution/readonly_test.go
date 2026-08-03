package execution_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/audit"
	"github.com/gracegaoya/ai-operations-copilot/internal/execution"
	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// blockingReadRunner blocks until ctx is cancelled, simulating a slow tool.
type blockingReadRunner struct{ result map[string]any }

func (b blockingReadRunner) Read(ctx context.Context, _ tools.Tool, _ map[string]any) (map[string]any, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(10 * time.Second):
		return b.result, nil
	}
}

// immediateReadRunner returns immediately, proving the wrapper does not break
// the happy path.
type immediateReadRunner struct{ result map[string]any }

func (i immediateReadRunner) Read(_ context.Context, _ tools.Tool, _ map[string]any) (map[string]any, error) {
	return i.result, nil
}

func viewerUser() identity.CurrentUser {
	return identity.CurrentUser{Subject: "viewer-1", Roles: []string{"viewer"}, AllowedEnvironments: []string{"prod"}, RequestID: "req-readonly"}
}

func TestExecuteReadTimesOutBlockingRunner(t *testing.T) {
	t.Parallel()
	repository := store.NewMemoryActionPlanStore()
	auditService := audit.NewService(repository)
	service := execution.NewReadOnlyService(blockingReadRunner{}, auditService).WithTimeout(50 * time.Millisecond)

	started := time.Now()
	_, err := service.ExecuteRead(context.Background(), viewerUser(), tools.ClusterStatusRead, map[string]any{"environment": "prod"})
	elapsed := time.Since(started)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > time.Second {
		t.Fatalf("elapsed = %v, want < 1s (timeout not honored)", elapsed)
	}

	// 超时应记 readonly_tool_failed / execution_error 审计
	events := repository.AuditEvents()
	found := false
	for _, ev := range events {
		if ev.Action == audit.ActionReadonlyToolFailed && ev.Decision == audit.DecisionExecutionError {
			found = true
			if ev.Metadata["reason"] != "timeout" {
				t.Errorf("metadata reason = %v, want timeout", ev.Metadata["reason"])
			}
		}
	}
	if !found {
		t.Errorf("no readonly_tool_failed/execution_error audit; events = %+v", events)
	}
}

func TestExecuteTrustedReadTimesOut(t *testing.T) {
	t.Parallel()
	service := execution.NewReadOnlyService(blockingReadRunner{}, nil).WithTimeout(50 * time.Millisecond)

	started := time.Now()
	_, err := service.ExecuteTrustedRead(context.Background(), tools.ClusterStatusRead, map[string]any{"environment": "prod"})
	elapsed := time.Since(started)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > time.Second {
		t.Fatalf("elapsed = %v, want < 1s", elapsed)
	}
}

func TestExecuteReadRespectsExistingShorterDeadline(t *testing.T) {
	t.Parallel()
	repository := store.NewMemoryActionPlanStore()
	auditService := audit.NewService(repository)
	// 服务超时 5s，但父 ctx 只有 30ms → 应 ~30ms 超时而非 5s
	service := execution.NewReadOnlyService(blockingReadRunner{}, auditService).WithTimeout(5 * time.Second)

	parent, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := service.ExecuteRead(parent, viewerUser(), tools.ClusterStatusRead, map[string]any{"environment": "prod"})
	elapsed := time.Since(started)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > time.Second {
		t.Fatalf("elapsed = %v, want ~30ms (parent deadline honored)", elapsed)
	}
}

func TestExecuteReadCompletesWithinTimeout(t *testing.T) {
	t.Parallel()
	repository := store.NewMemoryActionPlanStore()
	auditService := audit.NewService(repository)
	service := execution.NewReadOnlyService(immediateReadRunner{result: map[string]any{"status": "ok"}}, auditService)

	result, err := service.ExecuteRead(context.Background(), viewerUser(), tools.ClusterStatusRead, map[string]any{"environment": "prod"})
	if err != nil {
		t.Fatalf("ExecuteRead: %v", err)
	}
	if result["status"] != "ok" {
		t.Fatalf("result = %+v, want status ok", result)
	}
	events := repository.AuditEvents()
	if len(events) != 1 || events[0].Action != audit.ActionReadonlyToolExecuted || events[0].Decision != audit.DecisionPermitted {
		t.Fatalf("audit events = %+v, want readonly_tool_executed/permitted", events)
	}
}

func TestExecuteReadNoTimeoutWhenDisabled(t *testing.T) {
	t.Parallel()
	service := execution.NewReadOnlyService(immediateReadRunner{result: map[string]any{"status": "ok"}}, nil).WithTimeout(0)
	result, err := service.ExecuteRead(context.Background(), viewerUser(), tools.ClusterStatusRead, map[string]any{"environment": "prod"})
	if err != nil {
		t.Fatalf("ExecuteRead: %v", err)
	}
	if result["status"] != "ok" {
		t.Fatalf("result = %+v", result)
	}
}
