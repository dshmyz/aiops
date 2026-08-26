package diagnostics

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

var ErrInvalidRequest = errors.New("无效的诊断请求")

type Severity string

const (
	SeverityOK       Severity = "ok"
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

type Confidence string

const (
	ConfidenceLow    Confidence = "low"
	ConfidenceMedium Confidence = "medium"
	ConfidenceHigh   Confidence = "high"
)

type Request struct {
	Domain       string
	ResourceType string
	ResourceName string
	Runbook      string
}

type ResourceRef struct {
	Domain string            `json:"domain"`
	Type   string            `json:"type"`
	ID     string            `json:"id"`
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
}

type Observation struct {
	ID          string         `json:"id"`
	ResourceID  string         `json:"resource_id"`
	Kind        string         `json:"kind"`
	Severity    Severity       `json:"severity"`
	Summary     string         `json:"summary"`
	Data        map[string]any `json:"data,omitempty"`
	CollectedAt time.Time      `json:"collected_at"`
}

type Finding struct {
	ID          string     `json:"id"`
	Severity    Severity   `json:"severity"`
	Summary     string     `json:"summary"`
	EvidenceIDs []string   `json:"evidence_ids"`
	Confidence  Confidence `json:"confidence"`
}

type Recommendation struct {
	ID             string          `json:"id"`
	Summary        string          `json:"summary"`
	Rationale      string          `json:"rationale"`
	Risk           tools.RiskLevel `json:"risk"`
	Actionable     bool            `json:"actionable"`
	ToolName       string          `json:"tool_name,omitempty"`
	CandidateInput map[string]any  `json:"candidate_input,omitempty"`
}

type Package struct {
	ID              string           `json:"id"`
	Domains         []string         `json:"domains"`
	Resources       []ResourceRef    `json:"resources"`
	Observations    []Observation    `json:"observations"`
	Findings        []Finding        `json:"findings"`
	Recommendations []Recommendation `json:"recommendations"`
	PlanIDs         []string         `json:"plan_ids,omitempty"`
	CreatedAt       time.Time        `json:"created_at"`
}

func ValidatePackage(pkg Package) error {
	if strings.TrimSpace(pkg.ID) == "" {
		return errors.New("诊断ID为必填项")
	}
	if len(pkg.Domains) == 0 {
		return errors.New("至少需要一个领域")
	}
	resources := map[string]struct{}{}
	for _, resource := range pkg.Resources {
		if strings.TrimSpace(resource.ID) == "" || strings.TrimSpace(resource.Domain) == "" {
			return errors.New("资源ID和领域为必填项")
		}
		resources[resource.ID] = struct{}{}
	}
	observations := map[string]struct{}{}
	for _, observation := range pkg.Observations {
		if strings.TrimSpace(observation.ID) == "" || strings.TrimSpace(observation.Kind) == "" || strings.TrimSpace(observation.Summary) == "" {
			return errors.New("观察ID、类型和摘要为必填项")
		}
		if _, ok := resources[observation.ResourceID]; !ok {
			return fmt.Errorf("观察 %q 引用了未知资源 %q", observation.ID, observation.ResourceID)
		}
		if !validSeverity(observation.Severity) {
			return fmt.Errorf("观察 %q 的严重级别 %q 无效", observation.ID, observation.Severity)
		}
		observations[observation.ID] = struct{}{}
	}
	for _, finding := range pkg.Findings {
		if strings.TrimSpace(finding.ID) == "" || strings.TrimSpace(finding.Summary) == "" {
			return errors.New("结论ID和摘要为必填项")
		}
		if !validSeverity(finding.Severity) {
			return fmt.Errorf("结论 %q 的严重级别 %q 无效", finding.ID, finding.Severity)
		}
		if !validConfidence(finding.Confidence) {
			return fmt.Errorf("结论 %q 的置信度 %q 无效", finding.ID, finding.Confidence)
		}
		for _, evidenceID := range finding.EvidenceIDs {
			if _, ok := observations[evidenceID]; !ok {
				return fmt.Errorf("结论 %q 引用了未知证据 %q", finding.ID, evidenceID)
			}
		}
	}
	return nil
}

func validSeverity(value Severity) bool {
	return value == SeverityOK || value == SeverityInfo || value == SeverityWarning || value == SeverityCritical
}

func validConfidence(value Confidence) bool {
	return value == ConfidenceLow || value == ConfidenceMedium || value == ConfidenceHigh
}

// ToPlanInput returns the tool name and candidate input that can be used
// directly to construct an action plan. If ToolName is empty, actionable is
// false and the returned values should not be used for planning.
func (r Recommendation) ToPlanInput() (toolName string, input map[string]any, actionable bool) {
	if strings.TrimSpace(r.ToolName) == "" {
		return "", nil, false
	}
	return r.ToolName, r.CandidateInput, true
}

// HasActionableRecommendations reports whether any recommendation in the
// package carries a non-empty ToolName and can therefore be turned into an
// action plan without manual tool selection.
func (p Package) HasActionableRecommendations() bool {
	for _, rec := range p.Recommendations {
		if strings.TrimSpace(rec.ToolName) != "" {
			return true
		}
	}
	return false
}
