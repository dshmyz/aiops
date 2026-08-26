package diagnostics

import (
	"fmt"
	"strings"

	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

// genericDiagnostic 为未接入任何诊断能力的域生成"通用诊断框架包"。
//
// 这是优雅降级路径：域没发布能力时，用户仍能得到结构化的检查框架
// （按 runbook 给出 SOP 检查维度、参考阈值、接入引导），而不是 400 报错。
// 包内 Observation.Data["framework"]=true 标记这不是实测数据，消费方
// （assistant/前端）据此区分"真实指标诊断"与"通用框架"。
func (s *Service) genericDiagnostic(v validatedRequest, request Request) (Package, error) {
	now := s.clock.Now().UTC()
	domain := v.domain
	resourceType := v.resourceType
	if strings.TrimSpace(resourceType) == "" {
		resourceType = "resource"
	}
	name := v.resourceName
	if strings.TrimSpace(name) == "" {
		name = defaultResourceName(domain, resourceType)
	}
	runbook := strings.TrimSpace(request.Runbook)
	if runbook == "" {
		runbook = "health"
	}
	resourceID := domain + ":" + resourceType + ":" + name
	checkpoints := genericCheckpoints(runbook, domain)

	observationID := newID("obs")
	observation := Observation{
		ID:          observationID,
		ResourceID:  resourceID,
		Kind:        "generic." + runbook,
		Severity:    SeverityInfo,
		Summary:     fmt.Sprintf("%s 域未接入精确诊断能力，返回通用检查框架（%d 个检查点，非实测数据）", domain, len(checkpoints)),
		Data: map[string]any{
			"framework":  true,
			"domain":     domain,
			"runbook":    runbook,
			"checkpoints": checkpoints,
			"guidance":   "发布该域能力（QuickPublish）后可获得真实指标诊断",
		},
		CollectedAt: now,
	}
	finding := Finding{
		ID:          newID("finding"),
		Severity:    SeverityInfo,
		Summary:     fmt.Sprintf("%s 域未接入诊断能力，以下结论为通用检查框架，非实测结果", domain),
		EvidenceIDs: []string{observationID},
		Confidence:  ConfidenceLow,
	}
	recommendation := Recommendation{
		ID:        newID("rec"),
		Summary:   fmt.Sprintf("发布 %s 能力以启用精确诊断（QuickPublish）", domain),
		Rationale: "该域没有注册的只读诊断工具，无法返回真实指标；发布能力后注册表自动注入读工具",
		Risk:      tools.Low,
	}
	pkg := Package{
		ID:              newID("diag"),
		Domains:         []string{domain},
		Resources:       []ResourceRef{{Domain: domain, Type: resourceType, ID: resourceID, Name: name}},
		Observations:    []Observation{observation},
		Findings:        []Finding{finding},
		Recommendations: []Recommendation{recommendation},
		CreatedAt:       now,
	}
	if err := ValidatePackage(pkg); err != nil {
		return Package{}, err
	}
	return pkg, nil
}

// genericCheckpoints 返回某 runbook 的通用 SOP 检查维度清单（域无关）。
// 维度由 runbook 注册表维护，缺失的 runbook 兜底为 health。
// 域接入能力后，这些维度由真实工具覆盖；未接入时先按框架人工排查。
func genericCheckpoints(runbook, domain string) []string {
	t, ok := lookupRunbook(runbook)
	if !ok {
		t, _ = lookupRunbook("health")
	}
	return t.Checkpoints
}
