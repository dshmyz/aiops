package capabilities

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"sort"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

type ImportRecommendation string

const (
	RecommendationRecommended     ImportRecommendation = "recommended"
	RecommendationNeedsAdjustment ImportRecommendation = "needs_adjustment"
	RecommendationNotRecommended  ImportRecommendation = "not_recommended"
)

type ImportPreviewSource struct {
	OpenAPIURL     string `json:"openapi_url,omitempty"`
	BackendBaseURL string `json:"backend_base_url,omitempty"`
	Fingerprint    string `json:"fingerprint"`
}

type ImportPreviewStats struct {
	Total           int `json:"total"`
	Recommended     int `json:"recommended"`
	NeedsAdjustment int `json:"needs_adjustment"`
	NotRecommended  int `json:"not_recommended"`
	Read            int `json:"read"`
	Write           int `json:"write"`
}

type ImportCandidateCapability struct {
	Name         string          `json:"name"`
	Domain       string          `json:"domain"`
	ResourceType string          `json:"resource_type"`
	Operation    tools.Operation `json:"operation"`
	Risk         tools.RiskLevel `json:"risk"`
}

type ImportCandidate struct {
	ID             string                    `json:"id"`
	Method         string                    `json:"method"`
	Path           string                    `json:"path"`
	OperationID    string                    `json:"operation_id,omitempty"`
	Capability     Capability                `json:"capability"`
	Summary        ImportCandidateCapability `json:"summary"`
	Recommendation ImportRecommendation      `json:"recommendation"`
	Reasons        []string                  `json:"reasons"`
	Warnings       []string                  `json:"warnings"`
}

type ImportPreview struct {
	Source     ImportPreviewSource `json:"source"`
	Stats      ImportPreviewStats  `json:"stats"`
	Candidates []ImportCandidate   `json:"candidates"`
}

type ImportCandidateOverride struct {
	Name         string          `json:"name,omitempty"`
	Domain       string          `json:"domain,omitempty"`
	ResourceType string          `json:"resource_type,omitempty"`
	Operation    tools.Operation `json:"operation,omitempty"`
	Risk         tools.RiskLevel `json:"risk,omitempty"`
}

func OpenAPIFingerprint(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func CandidateID(method, path string) string {
	return strings.ToUpper(strings.TrimSpace(method)) + " " + strings.TrimSpace(path)
}

func ImportOpenAPICandidates(body []byte, existing []ManagedCapability) (ImportPreview, error) {
	operations, err := parseOpenAPIOperations(body)
	if err != nil {
		return ImportPreview{}, err
	}
	existingNames := make(map[string]struct{}, len(existing))
	for _, item := range existing {
		existingNames[item.Name] = struct{}{}
	}
	preview := ImportPreview{
		Source: ImportPreviewSource{Fingerprint: OpenAPIFingerprint(body)},
	}
	for _, operation := range operations {
		candidate := ImportCandidate{
			ID:          CandidateID(operation.Method, operation.Path),
			Method:      operation.Method,
			Path:        operation.Path,
			OperationID: operation.Operation.OperationID,
			Capability:  operation.Capability,
			Summary: ImportCandidateCapability{
				Name:         operation.Capability.Name,
				Domain:       operation.Capability.Domain,
				ResourceType: operation.Capability.ResourceType,
				Operation:    operation.Capability.Operation,
				Risk:         operation.Capability.Risk,
			},
		}
		candidate.Recommendation, candidate.Reasons, candidate.Warnings = recommendCandidate(operation.Capability, existingNames)
		preview.Candidates = append(preview.Candidates, candidate)
	}
	sort.SliceStable(preview.Candidates, func(left, right int) bool {
		return recommendationRank(preview.Candidates[left].Recommendation) < recommendationRank(preview.Candidates[right].Recommendation)
	})
	preview.Stats = importPreviewStats(preview.Candidates)
	return preview, nil
}

func recommendationRank(recommendation ImportRecommendation) int {
	switch recommendation {
	case RecommendationRecommended:
		return 0
	case RecommendationNeedsAdjustment:
		return 1
	case RecommendationNotRecommended:
		return 2
	default:
		return 3
	}
}

func ApplyCandidateOverride(candidate ImportCandidate, override ImportCandidateOverride) Capability {
	capability := candidate.Capability
	if strings.TrimSpace(override.Name) != "" {
		capability.Name = strings.TrimSpace(override.Name)
	}
	if strings.TrimSpace(override.Domain) != "" {
		capability.Domain = strings.TrimSpace(override.Domain)
	}
	if strings.TrimSpace(override.ResourceType) != "" {
		capability.ResourceType = strings.TrimSpace(override.ResourceType)
	}
	if override.Operation != "" {
		capability.Operation = override.Operation
	}
	if override.Risk != "" {
		capability.Risk = override.Risk
	}
	capability.Status = StatusNeedsReview
	return capability
}

func recommendCandidate(capability Capability, existingNames map[string]struct{}) (ImportRecommendation, []string, []string) {
	reasons := []string{}
	warnings := []string{}
	if _, exists := existingNames[capability.Name]; exists {
		return RecommendationNeedsAdjustment, []string{"已有同名能力，需要确认命名"}, []string{"已有同名能力"}
	}
	if capability.Operation != tools.Read || capability.Backend.Method != http.MethodGet {
		return RecommendationNotRecommended, []string{"第一版暂不接入写入能力"}, nil
	}
	if capability.Domain == "unknown" || capability.ResourceType == "resource" {
		warnings = append(warnings, "领域或资源类型需要确认")
		return RecommendationNeedsAdjustment, []string{"需要调整识别结果"}, warnings
	}
	if capability.Output.SummaryTemplate == "" && len(capability.Output.Fields) == 0 {
		warnings = append(warnings, "缺少输出映射")
		return RecommendationNeedsAdjustment, []string{"需要补充输出映射"}, warnings
	}
	reasons = append(reasons, "GET read operation", "known middleware domain")
	return RecommendationRecommended, reasons, warnings
}

func importPreviewStats(candidates []ImportCandidate) ImportPreviewStats {
	stats := ImportPreviewStats{Total: len(candidates)}
	for _, candidate := range candidates {
		switch candidate.Recommendation {
		case RecommendationRecommended:
			stats.Recommended++
		case RecommendationNeedsAdjustment:
			stats.NeedsAdjustment++
		case RecommendationNotRecommended:
			stats.NotRecommended++
		}
		if candidate.Capability.Operation == tools.Read {
			stats.Read++
		} else {
			stats.Write++
		}
	}
	return stats
}
