package execution

import (
	"context"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// VerificationStatus describes the outcome of a post-execution verification
// read call.
const (
	VerificationStatusSuccess = "success"
	VerificationStatusFailed  = "failed"
	VerificationStatusSkipped = "skipped"
	VerificationStatusDenied  = "denied"
)

// VerificationResult is returned by a Verifier after a write capability
// succeeds. The runtime surfaces it on Execution so callers can confirm
// the change took effect without a separate manual read.
type VerificationResult struct {
	ToolName  string         `json:"tool_name,omitempty"`
	Status    string         `json:"status"`
	Answer    map[string]any `json:"answer,omitempty"`
	Error     string         `json:"error,omitempty"`
	ElapsedMs int64          `json:"elapsed_ms,omitempty"`
}

// Verifier verifies a confirmed write execution by calling a related read
// capability. The verifier is invoked only after the executor succeeds and
// only for fresh executions (not reused ones). A nil return is allowed when
// the capability does not declare a verification read.
type Verifier interface {
	Verify(ctx context.Context, plan store.PlanRecord, input map[string]any) (*VerificationResult, error)
}

// runVerifier invokes the verifier with a timeout derived from the capability
// verify spec. A verifier error or timeout does not fail the execution; the
// failure is recorded on the returned VerificationResult instead.
func runVerifier(ctx context.Context, verifier Verifier, plan store.PlanRecord, input map[string]any) *VerificationResult {
	start := time.Now()
	result, err := verifier.Verify(ctx, plan, input)
	elapsed := time.Since(start).Milliseconds()
	if result == nil {
		result = &VerificationResult{Status: VerificationStatusSkipped}
	}
	result.ElapsedMs = elapsed
	if err != nil && result.Status == "" {
		result.Status = VerificationStatusFailed
	}
	if result.Error == "" && err != nil {
		result.Error = err.Error()
	}
	return result
}
