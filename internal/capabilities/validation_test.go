package capabilities_test

import (
	"math"
	"net/http"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/capabilities"
	"github.com/gracegaoya/ai-operations-copilot/internal/policy"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestValidateAcceptsPublishedReadCapability(t *testing.T) {
	t.Parallel()
	capability := validReadCapability()

	if err := capabilities.Validate(capability); err != nil {
		t.Fatalf("Validate returned %v", err)
	}
	tool, err := capabilities.ToTool(capability)
	if err != nil {
		t.Fatalf("ToTool returned %v", err)
	}
	if tool.Name != "minio.bucket.capacity.read" || tool.Operation != tools.Read || tool.Risk != tools.Low || tool.Domain != "minio" || tool.ResourceType != "bucket" {
		t.Fatalf("tool = %+v, want canonical read tool metadata", tool)
	}
}

func TestValidateRejectsWriteWithoutGovernance(t *testing.T) {
	t.Parallel()
	capability := validReadCapability()
	capability.Name = "kafka.topic.retention.update"
	capability.Operation = tools.Write
	capability.Risk = tools.Medium
	capability.Output = capabilities.OutputSpec{}

	err := capabilities.Validate(capability)
	if err == nil {
		t.Fatal("Validate accepted write capability without action-plan governance")
	}
}

func TestValidateRejectsPublishedWriteWithHeadMethod(t *testing.T) {
	t.Parallel()

	capability := validReadCapability()
	capability.Name = "kafka.topic.retention.update"
	capability.Operation = tools.Write
	capability.Risk = tools.Medium
	capability.Backend.Method = "HEAD"
	capability.Governance = capabilities.GovernanceSpec{
		RequiresActionPlan: true,
		RequiresApproval:   true,
		PrecheckTools:      []string{"minio.bucket.capacity.read"},
		Rollback:           capabilities.RollbackSpec{Strategy: "restore_previous"},
	}

	if err := capabilities.Validate(capability); err == nil {
		t.Fatal("Validate accepted published write capability with HEAD backend method")
	}
}

func TestValidateRejectsPathVariableMissingInputSchema(t *testing.T) {
	t.Parallel()
	capability := validReadCapability()
	capability.Backend.Path = "/api/minio/{cluster}/{bucket}/{missing}"

	err := capabilities.Validate(capability)
	if err == nil {
		t.Fatal("Validate accepted path variable missing from input schema")
	}
}

func TestValidateRejectsReadWithMutatingBackendMethod(t *testing.T) {
	t.Parallel()

	for _, status := range []string{capabilities.StatusPublished, capabilities.StatusNeedsReview} {
		for _, method := range []string{"HEAD", "POST", "PUT", "PATCH", "DELETE"} {
			capability := validReadCapability()
			capability.Status = status
			capability.Backend.Method = method

			if err := capabilities.Validate(capability); err == nil {
				t.Errorf("Validate accepted %s read capability with %s backend method", status, method)
			}
		}
	}
}

func TestValidateRequiresPublishedBaseURL(t *testing.T) {
	t.Parallel()

	for _, baseURL := range []string{"", "/relative", "http://", "https://[::1", "ftp://backend.example.com"} {
		capability := validReadCapability()
		capability.Backend.BaseURL = baseURL

		if err := capabilities.Validate(capability); err == nil {
			t.Errorf("Validate accepted published capability with base_url %q", baseURL)
		}
	}
}

func TestValidateAllowsDraftWithoutBaseURL(t *testing.T) {
	t.Parallel()
	capability := validReadCapability()
	capability.Status = capabilities.StatusNeedsReview
	capability.Backend.BaseURL = ""

	if err := capabilities.Validate(capability); err != nil {
		t.Fatalf("Validate returned %v", err)
	}
}

// TestValidateAcceptsAndPropagatesNumericBounds checks the bounds survive the
// InputField → tools.DynamicInputField conversion in RegisterPublishedCapability.
// A bound that validates but never reaches the runtime schema is no guardrail.
func TestValidateAcceptsAndPropagatesNumericBounds(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	policy.ResetDynamicRolePermissionsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	t.Cleanup(policy.ResetDynamicRolePermissionsForTest)

	capability := validWriteCapability()
	capability.InputSchema["quota"] = capabilities.InputField{Type: "integer", Required: true, Min: floatPtr(1), Max: floatPtr(1000)}

	if err := capabilities.Validate(capability); err != nil {
		t.Fatalf("Validate returned %v", err)
	}
	if err := capabilities.RegisterPublishedCapability(capability); err != nil {
		t.Fatalf("RegisterPublishedCapability returned %v", err)
	}

	schema, ok := tools.DynamicInputSchema(capability.Name)
	if !ok {
		t.Fatal("registered capability has no dynamic input schema")
	}
	quota := schema["quota"]
	if quota.Min == nil || *quota.Min != 1 || quota.Max == nil || *quota.Max != 1000 {
		t.Fatalf("quota bounds = %+v, want min 1 max 1000", quota)
	}

	tool, _ := tools.Lookup(capability.Name)
	valid := map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive", "quota": 500}
	if err := tools.ValidateInput(tool, valid); err != nil {
		t.Fatalf("ValidateInput in-range = %v, want accepted", err)
	}
	for _, quota := range []int{0, 1001} {
		input := map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive", "quota": quota}
		if err := tools.ValidateInput(tool, input); err == nil {
			t.Fatalf("ValidateInput accepted out-of-range quota %d", quota)
		}
	}
}

// TestValidateRejectsUnsatisfiableBounds keeps a capability that could never
// accept a valid value out of the registry at publish time.
func TestValidateRejectsUnsatisfiableBounds(t *testing.T) {
	t.Parallel()

	for name, field := range map[string]capabilities.InputField{
		"min above max":      {Type: "integer", Required: true, Min: floatPtr(100), Max: floatPtr(10)},
		"bounds on a string": {Type: "string", Required: true, Max: floatPtr(10)},
		"non-finite min":     {Type: "integer", Required: true, Min: floatPtr(math.Inf(-1))},
		"nan max":            {Type: "integer", Required: true, Max: floatPtr(math.NaN())},
	} {
		t.Run(name, func(t *testing.T) {
			capability := validReadCapability()
			capability.InputSchema["limit"] = field

			if err := capabilities.Validate(capability); err == nil {
				t.Fatal("Validate accepted an unsatisfiable bound")
			}
		})
	}
}

func floatPtr(value float64) *float64 {
	return &value
}

func validReadCapability() capabilities.Capability {
	return capabilities.Capability{
		SchemaVersion: 1,
		Name:          "minio.bucket.capacity.read",
		Status:        capabilities.StatusPublished,
		Domain:        "minio",
		ResourceType:  "bucket",
		Operation:     tools.Read,
		Risk:          tools.Low,
		Backend: capabilities.BackendSpec{
			Adapter:   "http",
			Method:    "GET",
			Path:      "/api/minio/clusters/{cluster}/buckets/{bucket}/capacity",
			TimeoutMS: 3000,
			BaseURL:   "https://backend.example.com",
		},
		InputSchema: map[string]capabilities.InputField{
			"environment": {Type: "string", Required: true},
			"cluster":     {Type: "string", Required: true},
			"bucket":      {Type: "string", Required: true},
		},
		Output: capabilities.OutputSpec{
			Kind:            "observation",
			SummaryTemplate: "Bucket {bucket} usage is {usage_pct}%",
			Fields: map[string]string{
				"usage_pct": "$.data.usage_pct",
			},
		},
		Auth: capabilities.AuthSpec{
			Roles:             []string{"viewer", "operator", "admin"},
			EnvironmentScoped: true,
		},
	}
}

func validWriteCapability() capabilities.Capability {
	return capabilities.Capability{
		SchemaVersion: 1,
		Name:          "minio.bucket.quota.set",
		Status:        capabilities.StatusPublished,
		Domain:        "minio",
		ResourceType:  "bucket",
		Operation:     tools.Write,
		Risk:          tools.Medium,
		Backend: capabilities.BackendSpec{
			Adapter:   "http",
			Method:    http.MethodPost,
			Path:      "/api/minio/clusters/{cluster}/buckets/{bucket}/quota",
			TimeoutMS: 3000,
			BaseURL:   "https://backend.example.com",
		},
		InputSchema: map[string]capabilities.InputField{
			"environment": {Type: "string", Required: true},
			"cluster":     {Type: "string", Required: true},
			"bucket":      {Type: "string", Required: true},
			"quota":       {Type: "integer", Required: true},
		},
		Output: capabilities.OutputSpec{
			Kind:            "mutation",
			SummaryTemplate: "Bucket {bucket} quota set to {quota}",
		},
		Governance: capabilities.GovernanceSpec{
			RequiresActionPlan: true,
			RequiresApproval:   true,
			PrecheckTools:      []string{"minio.bucket.capacity.read"},
			Rollback:           capabilities.RollbackSpec{Strategy: "restore_previous"},
		},
		Auth: capabilities.AuthSpec{
			Roles:             []string{"operator", "admin"},
			EnvironmentScoped: true,
		},
	}
}
