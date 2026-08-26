package policy

import (
	"encoding/json"
	"testing"

	"github.com/gracegaoya/ai-operations-copilot/internal/identity"
	"github.com/gracegaoya/ai-operations-copilot/internal/tools"
)

func TestEvaluateRejectsRoleWithoutToolPermission(t *testing.T) {
	registerMiddlewareTools(t)
	writeTool := registeredTool(t, "topic.retention.set")
	d := Evaluate(user("viewer"), writeTool, validRetentionInput(72))
	if d.Reason != PermissionDenied {
		t.Fatalf("reason = %q, want %q", d.Reason, PermissionDenied)
	}
	if d.Allowed {
		t.Fatal("viewer was allowed to use a write tool")
	}
}

func TestEvaluateViewerDiagnosticReadToolsAllowed(t *testing.T) {
	registerMiddlewareTools(t)

	for _, test := range []struct {
		name      string
		toolName  string
		toolInput map[string]any
	}{
		{name: "glusterfs volume health", toolName: "glusterfs.volume.health.read", toolInput: map[string]any{"name": "data"}},
		{name: "minio bucket health", toolName: "minio.bucket.health.read", toolInput: map[string]any{"name": "backups"}},
		{name: "kafka consumer lag", toolName: "kafka.consumer_lag.read", toolInput: map[string]any{"name": "orders"}},
		{name: "alert query", toolName: tools.AlertQuery, toolInput: map[string]any{}},
		{name: "event query", toolName: tools.EventQuery, toolInput: map[string]any{"query": "上周谁拒绝了 plan"}},
		{name: "task query", toolName: tools.TaskQuery, toolInput: map[string]any{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tool := registeredTool(t, test.toolName)

			allowed := Evaluate(user("viewer"), tool, test.toolInput)
			if !allowed.Allowed || allowed.Reason != Permitted || allowed.RequiresConfirmation {
				t.Fatalf("allowed decision = %+v, want permitted read", allowed)
			}
		})
	}
}

func TestEvaluateRejectsUnsafeParameterBelowFloor(t *testing.T) {
	registerMiddlewareTools(t)
	writeTool := registeredTool(t, "topic.retention.set")
	d := Evaluate(user("admin"), writeTool, validRetentionInput(12))
	if d.Reason != ParameterDenied {
		t.Fatalf("reason = %q, want %q", d.Reason, ParameterDenied)
	}
}

func TestEvaluateRejectsWriteAboveRoleRiskLimit(t *testing.T) {
	registerMiddlewareTools(t)
	writeTool := registeredTool(t, "topic.retention.set")
	d := Evaluate(user("operator"), writeTool, validRetentionInput(72))
	if d.Reason != RiskDenied {
		t.Fatalf("reason = %q, want %q", d.Reason, RiskDenied)
	}
}

func TestEvaluateRequiresConfirmationForAllowedWrite(t *testing.T) {
	registerMiddlewareTools(t)
	writeTool := registeredTool(t, "topic.retention.set")
	d := Evaluate(user("admin"), writeTool, validRetentionInput(72))
	if !d.Allowed {
		t.Fatalf("admin write decision = %+v, want allowed", d)
	}
	if !d.RequiresConfirmation {
		t.Fatal("allowed write did not require human confirmation")
	}
}

func TestEvaluateUsesCanonicalToolMetadata(t *testing.T) {
	registerMiddlewareTools(t)
	forgedRead := tools.Tool{Name: "topic.retention.set", Operation: tools.Read, Risk: tools.Low}
	d := Evaluate(user("admin"), forgedRead, validRetentionInput(72))
	if !d.Allowed || !d.RequiresConfirmation {
		t.Fatalf("forged metadata changed canonical write policy: %+v", d)
	}
}

func TestEvaluateAcceptsValidJSONNumberParameter(t *testing.T) {
	registerMiddlewareTools(t)
	writeTool := registeredTool(t, "topic.retention.set")
	input := validRetentionInput(72)
	input["retention_hours"] = json.Number("72")

	d := Evaluate(user("admin"), writeTool, input)
	if !d.Allowed || !d.RequiresConfirmation {
		t.Fatalf("JSON number input decision = %+v, want an allowed confirmed write", d)
	}
}

func TestEvaluateAllowsDynamicReadForRegisteredRole(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	if err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{{
		Tool: tools.Tool{Name: "minio.bucket.capacity.read", Operation: tools.Read, Risk: tools.Low, Domain: "minio", ResourceType: "bucket"},
		InputSchema: map[string]tools.DynamicInputField{
			"cluster": {Type: "string", Required: true},
			"bucket":  {Type: "string", Required: true},
		},
	}}); err != nil {
		t.Fatalf("register dynamic: %v", err)
	}
	RegisterDynamicRolePermissions(map[string][]string{"minio.bucket.capacity.read": {"viewer"}})
	t.Cleanup(ResetDynamicRolePermissionsForTest)

	d := Evaluate(user("viewer"), registeredTool(t, "minio.bucket.capacity.read"), map[string]any{"cluster": "m1", "bucket": "archive"})
	if !d.Allowed || d.RequiresConfirmation {
		t.Fatalf("dynamic read decision = %+v, want allowed read", d)
	}
}

// TestEvaluateAppliesParameterFloorToDynamicWriteCapability covers the route
// the planner actually takes today: a published capability, not the static
// tool. The floor matches on the parameter, so re-routing the same operation
// to a capability must still enforce it.
func TestEvaluateAppliesParameterFloorToDynamicWriteCapability(t *testing.T) {
	tools.ResetDynamicToolsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	if err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{{
		Tool: tools.Tool{
			Name:                "kafka.topic.retention.write",
			Operation:           tools.Write,
			Risk:                tools.Medium,
			RollbackDescription: "reset_to_previous",
			Domain:              "kafka",
			ResourceType:        "topic",
		},
		InputSchema: map[string]tools.DynamicInputField{
			"cluster":         {Type: "string", Required: true},
			"topic":           {Type: "string", Required: true},
			"retention_hours": {Type: "integer", Required: true, Min: bound(1), Max: bound(8760)},
		},
	}}); err != nil {
		t.Fatalf("register dynamic: %v", err)
	}
	RegisterDynamicRolePermissions(map[string][]string{"kafka.topic.retention.write": {"admin"}})
	t.Cleanup(ResetDynamicRolePermissionsForTest)

	tool := registeredTool(t, "kafka.topic.retention.write")
	input := func(hours any) map[string]any {
		return map[string]any{"cluster": "c1", "topic": "orders", "retention_hours": hours}
	}

	for name, test := range map[string]struct {
		hours any
		want  Reason
	}{
		"below floor":            {hours: 1, want: ParameterDenied},
		"above schema maximum":   {hours: 999999, want: InvalidInput},
		"json number below floor": {hours: json.Number("12"), want: ParameterDenied},
		"at floor":               {hours: 24, want: Permitted},
	} {
		t.Run(name, func(t *testing.T) {
			d := Evaluate(user("admin"), tool, input(test.hours))
			if d.Reason != test.want {
				t.Fatalf("reason = %q, want %q", d.Reason, test.want)
			}
		})
	}
}

// registerMiddlewareTools loads the middleware capabilities into the dynamic
// registry and injects their role permissions, mirroring production: reads are
// visible to all roles; the retention write requires operator/admin (its
// risk is handled by the risk check, not the role table).
func registerMiddlewareTools(t *testing.T) {
	t.Helper()
	tools.ResetDynamicToolsForTest()
	t.Cleanup(tools.ResetDynamicToolsForTest)
	ResetDynamicRolePermissionsForTest()
	t.Cleanup(ResetDynamicRolePermissionsForTest)
	if err := tools.RegisterDynamicTools([]tools.DynamicToolDefinition{
		{
			Tool: tools.Tool{Name: "glusterfs.volume.health.read", Operation: tools.Read, Risk: tools.Low, Domain: "glusterfs", ResourceType: "volume"},
			InputSchema: map[string]tools.DynamicInputField{
				"name": {Type: "string", Required: true},
			},
		},
		{
			Tool: tools.Tool{Name: "minio.bucket.health.read", Operation: tools.Read, Risk: tools.Low, Domain: "minio", ResourceType: "bucket"},
			InputSchema: map[string]tools.DynamicInputField{
				"name": {Type: "string", Required: true},
			},
		},
		{
			Tool: tools.Tool{Name: "kafka.consumer_lag.read", Operation: tools.Read, Risk: tools.Low, Domain: "kafka", ResourceType: "consumer_group"},
			InputSchema: map[string]tools.DynamicInputField{
				"name": {Type: "string", Required: true},
			},
		},
		{
			Tool: tools.Tool{
				Name:                "topic.retention.set",
				Operation:           tools.Write,
				Risk:                tools.Medium,
				RollbackDescription: "reset_to_previous",
				Domain:              "kafka",
				ResourceType:        "topic",
				SupportsDryRun:      true,
			},
			InputSchema: map[string]tools.DynamicInputField{
				"topic":           {Type: "string", Required: true},
				"retention_hours": {Type: "integer", Required: true, Min: bound(1), Max: bound(8760)},
			},
		},
	}); err != nil {
		t.Fatalf("register middleware tools: %v", err)
	}
	RegisterDynamicRolePermissions(map[string][]string{
		"glusterfs.volume.health.read": {"viewer", "operator", "admin"},
		"minio.bucket.health.read":     {"viewer", "operator", "admin"},
		"kafka.consumer_lag.read":      {"viewer", "operator", "admin"},
		"topic.retention.set":          {"operator", "admin"},
	})
}

func bound(value float64) *float64 {
	return &value
}

func registeredTool(t *testing.T, name string) tools.Tool {
	t.Helper()
	tool, ok := tools.Lookup(name)
	if !ok {
		t.Fatalf("registered tool %q not found", name)
	}
	return tool
}

func user(role string) identity.CurrentUser {
	return identity.CurrentUser{
		Subject:   "user-123",
		Roles:     []string{role},
		RequestID: "req-456",
	}
}

func validRetentionInput(hours int) map[string]any {
	return map[string]any{
		"topic":           "orders",
		"retention_hours": hours,
	}
}