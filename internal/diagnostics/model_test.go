package diagnostics_test

import (
	"testing"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestValidatePackageAcceptsStructuredDiagnostic(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.July, 22, 9, 0, 0, 0, time.UTC)
	pkg := diagnostics.Package{
		ID:      "diag-1",
		Domains: []string{"glusterfs"},
		Resources: []diagnostics.ResourceRef{{
			Domain: "glusterfs", Type: "volume", ID: "vol-prod-data", Name: "prod-data",
		}},
		Observations: []diagnostics.Observation{{
			ID: "obs-1", ResourceID: "vol-prod-data", Kind: "glusterfs.volume.health", Severity: diagnostics.SeverityWarning, Summary: "heal backlog is present", Data: map[string]any{"heal_pending": 12}, CollectedAt: now,
		}},
		Findings: []diagnostics.Finding{{
			ID: "finding-1", Severity: diagnostics.SeverityWarning, Summary: "volume needs heal review", EvidenceIDs: []string{"obs-1"}, Confidence: diagnostics.ConfidenceMedium,
		}},
		Recommendations: []diagnostics.Recommendation{{
			ID: "rec-1", Summary: "review heal status", Rationale: "pending entries are above zero", Risk: tools.Low, Actionable: false,
		}},
		CreatedAt: now,
	}

	if err := diagnostics.ValidatePackage(pkg); err != nil {
		t.Fatalf("ValidatePackage returned %v", err)
	}
}

func TestValidatePackageRejectsUnknownEvidenceReference(t *testing.T) {
	t.Parallel()
	pkg := diagnostics.Package{
		ID:        "diag-1",
		Domains:   []string{"glusterfs"},
		Resources: []diagnostics.ResourceRef{{Domain: "glusterfs", Type: "volume", ID: "vol-prod-data", Name: "prod-data"}},
		Observations: []diagnostics.Observation{{
			ID: "obs-1", ResourceID: "vol-prod-data", Kind: "glusterfs.volume.health", Severity: diagnostics.SeverityOK, Summary: "healthy", CollectedAt: time.Now().UTC(),
		}},
		Findings:  []diagnostics.Finding{{ID: "finding-1", Severity: diagnostics.SeverityWarning, Summary: "bad reference", EvidenceIDs: []string{"missing-observation"}, Confidence: diagnostics.ConfidenceLow}},
		CreatedAt: time.Now().UTC(),
	}

	if err := diagnostics.ValidatePackage(pkg); err == nil {
		t.Fatal("ValidatePackage accepted a finding with missing evidence")
	}
}
