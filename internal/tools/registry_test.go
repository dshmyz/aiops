package tools

import (
	"encoding/json"
	"math"
	"testing"
)

// registerMiddlewareTools loads the middleware capabilities into the dynamic
// registry, mirroring the production YAML published capabilities (the static
// allowlist no longer contains them). The schemas intentionally use the same
// names and field names ("name", "topic", "retention_hours") as the YAMLs.
func registerMiddlewareTools(t *testing.T) {
	t.Helper()
	ResetDynamicToolsForTest()
	t.Cleanup(ResetDynamicToolsForTest)
	err := RegisterDynamicTools([]DynamicToolDefinition{
		{
			Tool: Tool{Name: GlusterVolumeHealthRead, Operation: Read, Risk: Low, Domain: "glusterfs", ResourceType: "volume"},
			InputSchema: map[string]DynamicInputField{
				"environment": {Type: "string", Required: true},
				"name":        {Type: "string", Required: true},
			},
		},
		{
			Tool: Tool{Name: MinIOBucketHealthRead, Operation: Read, Risk: Low, Domain: "minio", ResourceType: "bucket"},
			InputSchema: map[string]DynamicInputField{
				"environment": {Type: "string", Required: true},
				"name":        {Type: "string", Required: true},
			},
		},
		{
			Tool: Tool{Name: KafkaConsumerLagRead, Operation: Read, Risk: Low, Domain: "kafka", ResourceType: "consumer_group"},
			InputSchema: map[string]DynamicInputField{
				"environment": {Type: "string", Required: true},
				"name":        {Type: "string", Required: true},
			},
		},
		{
			Tool: Tool{Name: TopicRetentionSet, Operation: Write, Risk: Medium, RollbackDescription: "reset_to_previous", Domain: "kafka", ResourceType: "topic", SupportsDryRun: true},
			InputSchema: map[string]DynamicInputField{
				"environment":     {Type: "string", Required: true},
				"topic":           {Type: "string", Required: true},
				"retention_hours": {Type: "integer", Required: true, Min: boundOf(1), Max: boundOf(8760)},
			},
		},
	})
	if err != nil {
		t.Fatalf("register middleware tools: %v", err)
	}
}

func TestLookupReturnsOnlyStaticallyRegisteredTools(t *testing.T) {
	registerMiddlewareTools(t)
	tool, ok := Lookup(TopicRetentionSet)
	if !ok {
		t.Fatalf("registered tool %q was not found", TopicRetentionSet)
	}
	if tool.Operation != Write {
		t.Fatalf("operation = %q, want %q", tool.Operation, Write)
	}
	if tool.Risk != Medium {
		t.Fatalf("risk = %q, want %q", tool.Risk, Medium)
	}
	if tool.RollbackDescription == "" {
		t.Fatal("write tool has no rollback description")
	}

	if _, ok := Lookup("topic.delete"); ok {
		t.Fatal("unregistered destructive tool was accepted")
	}
}

func TestIsStaticIdentifiesBuiltInTools(t *testing.T) {
	ResetDynamicToolsForTest()
	t.Cleanup(ResetDynamicToolsForTest)

	if !IsStatic(ClusterStatusRead) {
		t.Fatalf("IsStatic(%q) = false, want true", ClusterStatusRead)
	}
	if err := RegisterDynamicTools([]DynamicToolDefinition{{
		Tool: Tool{Name: "minio.bucket.capacity.read", Operation: Read, Risk: Low, Domain: "minio", ResourceType: "bucket"},
		InputSchema: map[string]DynamicInputField{
			"environment": {Type: "string", Required: true},
			"bucket":      {Type: "string", Required: true},
		},
	}}); err != nil {
		t.Fatalf("RegisterDynamicTools returned %v", err)
	}
	if IsStatic("minio.bucket.capacity.read") {
		t.Fatal("IsStatic returned true for dynamic tool")
	}
}

func TestDynamicInputSchemaReturnsCopy(t *testing.T) {
	ResetDynamicToolsForTest()
	t.Cleanup(ResetDynamicToolsForTest)
	err := RegisterDynamicTools([]DynamicToolDefinition{{
		Tool: Tool{Name: "minio.bucket.capacity.read", Operation: Read, Risk: Low, Domain: "minio", ResourceType: "bucket"},
		InputSchema: map[string]DynamicInputField{
			"environment": {Type: "string", Required: true},
			"cluster":     {Type: "string", Required: true},
			"bucket":      {Type: "string", Required: true},
		},
	}})
	if err != nil {
		t.Fatalf("register dynamic tool: %v", err)
	}

	schema, ok := DynamicInputSchema("minio.bucket.capacity.read")
	if !ok {
		t.Fatal("DynamicInputSchema returned ok=false")
	}
	schema["bucket"] = DynamicInputField{Type: "integer", Required: false}

	fresh, ok := DynamicInputSchema("minio.bucket.capacity.read")
	if !ok || fresh["bucket"].Type != "string" || !fresh["bucket"].Required {
		t.Fatalf("fresh schema = %+v, want unchanged copy", fresh)
	}
	if !IsDynamic("minio.bucket.capacity.read") {
		t.Fatal("IsDynamic returned false for registered dynamic tool")
	}
	if IsDynamic(ClusterStatusRead) {
		t.Fatal("IsDynamic returned true for static tool")
	}
}

func TestValidateInputRejectsUnknownWriteParameters(t *testing.T) {
	registerMiddlewareTools(t)
	tool, ok := Lookup(TopicRetentionSet)
	if !ok {
		t.Fatalf("registered tool %q was not found", TopicRetentionSet)
	}

	err := ValidateInput(tool, map[string]any{
		"environment":     "prod",
		"topic":           "orders",
		"retention_hours": 72,
		"delete_all":      true,
	})
	if err == nil {
		t.Fatal("ValidateInput accepted an unknown write parameter")
	}
}

func TestValidateInputUsesCanonicalRegisteredTool(t *testing.T) {
	registerMiddlewareTools(t)
	tool, ok := Lookup(TopicRetentionSet)
	if !ok {
		t.Fatalf("registered tool %q was not found", TopicRetentionSet)
	}
	forged := Tool{Name: TopicRetentionSet, Operation: Read, Risk: Low}
	err := ValidateInput(forged, map[string]any{
		"environment": "prod",
		"topic":       "orders",
	})
	if err == nil {
		t.Fatal("ValidateInput accepted input valid only for a forged tool definition")
	}
	// ensure the canonical (registered) tool still accepts valid input
	if err := ValidateInput(tool, map[string]any{"environment": "prod", "topic": "orders", "retention_hours": 72}); err != nil {
		t.Fatalf("ValidateInput canonical = %v, want nil", err)
	}
}

func TestDomainReadToolsAreRegisteredReadOnlyTools(t *testing.T) {
	registerMiddlewareTools(t)
	for _, name := range []string{GlusterVolumeHealthRead, MinIOBucketHealthRead, KafkaConsumerLagRead} {
		tool, ok := Lookup(name)
		if !ok {
			t.Fatalf("tool %q was not registered", name)
		}
		if tool.Operation != Read {
			t.Fatalf("tool %q operation = %q, want read", name, tool.Operation)
		}
		if tool.Domain == "" || tool.ResourceType == "" {
			t.Fatalf("tool %q missing domain metadata: %+v", name, tool)
		}
		if err := ValidateInput(tool, map[string]any{"environment": "prod", "name": "orders"}); err != nil {
			t.Fatalf("ValidateInput(%q) returned %v", name, err)
		}
	}
}

func TestDomainReadToolsRejectUnknownParameters(t *testing.T) {
	registerMiddlewareTools(t)
	tool, ok := Lookup(GlusterVolumeHealthRead)
	if !ok {
		t.Fatalf("tool %q was not registered", GlusterVolumeHealthRead)
	}
	err := ValidateInput(tool, map[string]any{"environment": "prod", "name": "data", "shell": "rm -rf /"})
	if err == nil {
		t.Fatal("ValidateInput accepted unknown diagnostic parameter")
	}
}

func TestAlertQueryToolRegistration(t *testing.T) {
	tool, ok := Lookup(AlertQuery)
	if !ok {
		t.Fatalf("tool %q was not registered", AlertQuery)
	}
	if tool.Operation != Read {
		t.Fatalf("operation = %q, want read", tool.Operation)
	}
	if tool.Risk != Low {
		t.Fatalf("risk = %q, want low", tool.Risk)
	}
	if tool.Domain != "alert" || tool.ResourceType != "alert" {
		t.Fatalf("domain metadata = %+v, want alert/alert", tool)
	}
}

func TestAlertQueryValidateInput(t *testing.T) {
	tool, ok := Lookup(AlertQuery)
	if !ok {
		t.Fatalf("tool %q was not registered", AlertQuery)
	}
	// 合法：仅 environment
	if err := ValidateInput(tool, map[string]any{"environment": "prod"}); err != nil {
		t.Fatalf("ValidateInput(prod) = %v, want nil", err)
	}
	// 合法：全部可选过滤
	if err := ValidateInput(tool, map[string]any{"environment": "prod", "severity": "critical", "status": "firing", "domain": "kafka"}); err != nil {
		t.Fatalf("ValidateInput(all filters) = %v, want nil", err)
	}
	// 缺 environment
	if err := ValidateInput(tool, map[string]any{"severity": "critical"}); err == nil {
		t.Fatal("ValidateInput without environment accepted")
	}
	// 未知键
	if err := ValidateInput(tool, map[string]any{"environment": "prod", "shell": "rm -rf /"}); err == nil {
		t.Fatal("ValidateInput accepted unknown key")
	}
	// 非法 severity
	if err := ValidateInput(tool, map[string]any{"environment": "prod", "severity": "severe"}); err == nil {
		t.Fatal("ValidateInput accepted invalid severity")
	}
	// 非法 status
	if err := ValidateInput(tool, map[string]any{"environment": "prod", "status": "acknowledged"}); err == nil {
		t.Fatal("ValidateInput accepted invalid status")
	}
}

func TestEventQueryToolRegistration(t *testing.T) {
	tool, ok := Lookup(EventQuery)
	if !ok {
		t.Fatalf("tool %q was not registered", EventQuery)
	}
	if tool.Operation != Read || tool.Risk != Low {
		t.Fatalf("operation/risk = %v/%v, want read/low", tool.Operation, tool.Risk)
	}
	if tool.Domain != "event" || tool.ResourceType != "event" {
		t.Fatalf("domain metadata = %+v, want event/event", tool)
	}
}

func TestEventQueryValidateInput(t *testing.T) {
	tool, ok := Lookup(EventQuery)
	if !ok {
		t.Fatalf("tool %q was not registered", EventQuery)
	}
	if err := ValidateInput(tool, map[string]any{"environment": "prod", "query": "上周谁拒绝了 plan"}); err != nil {
		t.Fatalf("ValidateInput(valid) = %v, want nil", err)
	}
	if err := ValidateInput(tool, map[string]any{"environment": "prod"}); err == nil {
		t.Fatal("ValidateInput without query accepted")
	}
	if err := ValidateInput(tool, map[string]any{"query": "上周"}); err == nil {
		t.Fatal("ValidateInput without environment accepted")
	}
	if err := ValidateInput(tool, map[string]any{"environment": "prod", "query": "x", "shell": "rm"}); err == nil {
		t.Fatal("ValidateInput accepted unknown key")
	}
}

func TestTaskQueryToolRegistration(t *testing.T) {
	tool, ok := Lookup(TaskQuery)
	if !ok {
		t.Fatalf("tool %q was not registered", TaskQuery)
	}
	if tool.Operation != Read || tool.Risk != Low {
		t.Fatalf("operation/risk = %v/%v, want read/low", tool.Operation, tool.Risk)
	}
	if tool.Domain != "task" || tool.ResourceType != "task" {
		t.Fatalf("domain metadata = %+v, want task/task", tool)
	}
}

func TestTaskQueryValidateInput(t *testing.T) {
	tool, ok := Lookup(TaskQuery)
	if !ok {
		t.Fatalf("tool %q was not registered", TaskQuery)
	}
	if err := ValidateInput(tool, map[string]any{"environment": "prod"}); err != nil {
		t.Fatalf("ValidateInput(env only) = %v, want nil", err)
	}
	if err := ValidateInput(tool, map[string]any{"environment": "prod", "status": "enabled", "limit": 10}); err != nil {
		t.Fatalf("ValidateInput(all) = %v, want nil", err)
	}
	if err := ValidateInput(tool, map[string]any{"environment": "prod", "status": "paused"}); err == nil {
		t.Fatal("ValidateInput accepted invalid status")
	}
	if err := ValidateInput(tool, map[string]any{"environment": "prod", "limit": -5}); err == nil {
		t.Fatal("ValidateInput accepted negative limit")
	}
	if err := ValidateInput(tool, map[string]any{}); err == nil {
		t.Fatal("ValidateInput without environment accepted")
	}
}

func TestRegisterDynamicToolsAddsCanonicalLookupAndValidation(t *testing.T) {
	ResetDynamicToolsForTest()
	t.Cleanup(ResetDynamicToolsForTest)

	err := RegisterDynamicTools([]DynamicToolDefinition{{
		Tool: Tool{Name: "minio.bucket.capacity.read", Operation: Read, Risk: Low, Domain: "minio", ResourceType: "bucket"},
		InputSchema: map[string]DynamicInputField{
			"environment": {Type: "string", Required: true},
			"cluster":     {Type: "string", Required: true},
			"bucket":      {Type: "string", Required: true},
		},
	}})
	if err != nil {
		t.Fatalf("RegisterDynamicTools returned %v", err)
	}
	tool, ok := Lookup("minio.bucket.capacity.read")
	if !ok || tool.Domain != "minio" {
		t.Fatalf("Lookup dynamic tool = %+v, %v", tool, ok)
	}
	found := false
	for _, registered := range All() {
		if registered.Name == tool.Name {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("All() did not include dynamic tool %q", tool.Name)
	}
	if err := ValidateInput(tool, map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive"}); err != nil {
		t.Fatalf("ValidateInput dynamic returned %v", err)
	}
	if err := ValidateInput(tool, map[string]any{"environment": "prod", "cluster": "m1", "bucket": "archive", "extra": true}); err == nil {
		t.Fatal("ValidateInput accepted unknown dynamic input")
	}
}

func TestRegisterDynamicToolsRejectsStaticAndDuplicateNames(t *testing.T) {
	ResetDynamicToolsForTest()
	t.Cleanup(ResetDynamicToolsForTest)

	definition := DynamicToolDefinition{
		Tool:        Tool{Name: "runtime.read", Operation: Read, Risk: Low, Domain: "runtime", ResourceType: "item"},
		InputSchema: map[string]DynamicInputField{"environment": {Type: "string", Required: true}},
	}
	if err := RegisterDynamicTools([]DynamicToolDefinition{{
		Tool:        Tool{Name: ClusterStatusRead, Operation: Read, Risk: Low},
		InputSchema: map[string]DynamicInputField{"environment": {Type: "string", Required: true}},
	}}); err == nil {
		t.Fatal("RegisterDynamicTools accepted a static tool conflict")
	}
	if err := RegisterDynamicTools([]DynamicToolDefinition{definition}); err != nil {
		t.Fatalf("RegisterDynamicTools returned %v", err)
	}
	if err := RegisterDynamicTools([]DynamicToolDefinition{definition}); err == nil {
		t.Fatal("RegisterDynamicTools accepted a duplicate dynamic tool")
	}
}

func TestRegisterDynamicToolsIsAtomicForInvalidBatch(t *testing.T) {
	ResetDynamicToolsForTest()
	t.Cleanup(ResetDynamicToolsForTest)

	definition := DynamicToolDefinition{
		Tool:        Tool{Name: "runtime.atomic.read", Operation: Read, Risk: Low, Domain: "runtime", ResourceType: "item"},
		InputSchema: map[string]DynamicInputField{"environment": {Type: "string", Required: true}},
	}
	err := RegisterDynamicTools([]DynamicToolDefinition{
		definition,
		definition,
	})
	if err == nil {
		t.Fatal("RegisterDynamicTools accepted duplicate names within one batch")
	}
	if _, ok := Lookup(definition.Tool.Name); ok {
		t.Fatalf("invalid batch partially registered dynamic tool %q", definition.Tool.Name)
	}
}

func TestRegisterDynamicToolsRequiresEnvironmentString(t *testing.T) {
	ResetDynamicToolsForTest()
	t.Cleanup(ResetDynamicToolsForTest)

	for name, environment := range map[string]DynamicInputField{
		"missing":    {},
		"optional":   {Type: "string"},
		"wrong type": {Type: "boolean", Required: true},
	} {
		t.Run(name, func(t *testing.T) {
			err := RegisterDynamicTools([]DynamicToolDefinition{{
				Tool:        Tool{Name: "runtime.environment." + name, Operation: Read, Risk: Low, Domain: "runtime", ResourceType: "item"},
				InputSchema: map[string]DynamicInputField{"environment": environment},
			}})
			if err == nil {
				t.Fatal("RegisterDynamicTools accepted an invalid environment schema")
			}
		})
	}
}

func TestValidateInputDynamicSupportsSchemaTypesAndRequiredFields(t *testing.T) {
	ResetDynamicToolsForTest()
	t.Cleanup(ResetDynamicToolsForTest)

	if err := RegisterDynamicTools([]DynamicToolDefinition{{
		Tool: Tool{Name: "runtime.types.read", Operation: Read, Risk: Low, Domain: "runtime", ResourceType: "item"},
		InputSchema: map[string]DynamicInputField{
			"environment": {Type: "string", Required: true},
			"count":       {Type: "integer", Required: true},
			"ratio":       {Type: "number", Required: true},
			"enabled":     {Type: "boolean", Required: true},
			"note":        {Type: "string"},
		},
	}}); err != nil {
		t.Fatalf("RegisterDynamicTools returned %v", err)
	}
	tool, _ := Lookup("runtime.types.read")
	valid := map[string]any{"environment": "prod", "count": 3, "ratio": 1.5, "enabled": true}
	if err := ValidateInput(tool, valid); err != nil {
		t.Fatalf("ValidateInput accepted schema-valid input: %v", err)
	}
	for name, value := range map[string]any{"count": "3", "ratio": true, "enabled": "yes"} {
		input := map[string]any{"environment": "prod", "count": 3, "ratio": 1.5, "enabled": true}
		input[name] = value
		if err := ValidateInput(tool, input); err == nil {
			t.Fatalf("ValidateInput accepted invalid %s value", name)
		}
	}
	missing := map[string]any{"environment": "prod", "count": 3, "ratio": 1.5}
	if err := ValidateInput(tool, missing); err == nil {
		t.Fatal("ValidateInput accepted input missing a required field")
	}
}

func TestValidateInputDynamicRejectsInvalidNumbers(t *testing.T) {
	ResetDynamicToolsForTest()
	t.Cleanup(ResetDynamicToolsForTest)

	if err := RegisterDynamicTools([]DynamicToolDefinition{{
		Tool: Tool{Name: "runtime.number.read", Operation: Read, Risk: Low, Domain: "runtime", ResourceType: "item"},
		InputSchema: map[string]DynamicInputField{
			"environment": {Type: "string", Required: true},
			"value":       {Type: "number", Required: true},
		},
	}}); err != nil {
		t.Fatalf("RegisterDynamicTools returned %v", err)
	}
	tool, _ := Lookup("runtime.number.read")
	for name, value := range map[string]any{
		"invalid json number": json.Number("not-a-number"),
		"nan":                 math.NaN(),
		"positive infinity":   math.Inf(1),
		"negative infinity":   math.Inf(-1),
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateInput(tool, map[string]any{"environment": "prod", "value": value}); err == nil {
				t.Fatal("ValidateInput accepted a non-finite or invalid number")
			}
		})
	}
}

// --- 数值护栏：DynamicInputField Min/Max 测试 ---

// TestValidateInputDynamicEnforcesDeclaredBounds is the schema half of the
// guardrail a re-routed write would otherwise lose: the static
// topic.retention.set caps hours in hand-written Go, so an equivalent
// capability has to express the same limits in its schema.
func TestValidateInputDynamicEnforcesDeclaredBounds(t *testing.T) {
	ResetDynamicToolsForTest()
	t.Cleanup(ResetDynamicToolsForTest)

	if err := RegisterDynamicTools([]DynamicToolDefinition{{
		Tool: Tool{
			Name:                "kafka.topic.retention.write",
			Operation:           Write,
			Risk:                Medium,
			RollbackDescription: "reset_to_previous",
			Domain:              "kafka",
			ResourceType:        "topic",
		},
		InputSchema: map[string]DynamicInputField{
			"environment":     {Type: "string", Required: true},
			"topic":           {Type: "string", Required: true},
			"retention_hours": {Type: "integer", Required: true, Min: boundOf(1), Max: boundOf(8760)},
		},
	}}); err != nil {
		t.Fatalf("RegisterDynamicTools returned %v", err)
	}
	tool, _ := Lookup("kafka.topic.retention.write")

	for name, test := range map[string]struct {
		hours    any
		accepted bool
	}{
		"below minimum":       {hours: 0, accepted: false},
		"above maximum":       {hours: 999999, accepted: false},
		"json number too big": {hours: json.Number("8761"), accepted: false},
		"at minimum":          {hours: 1, accepted: true},
		"at maximum":          {hours: 8760, accepted: true},
		"inside range":        {hours: 72, accepted: true},
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidateInput(tool, map[string]any{"environment": "prod", "topic": "orders", "retention_hours": test.hours})
			if test.accepted && err != nil {
				t.Fatalf("ValidateInput(%v) = %v, want accepted", test.hours, err)
			}
			if !test.accepted && err == nil {
				t.Fatalf("ValidateInput accepted out-of-range %v", test.hours)
			}
		})
	}
}

// TestValidateInputDynamicSkipsBoundsForUnboundedFields guards the common
// case: a field without min/max must keep validating on type alone.
func TestValidateInputDynamicSkipsBoundsForUnboundedFields(t *testing.T) {
	ResetDynamicToolsForTest()
	t.Cleanup(ResetDynamicToolsForTest)

	if err := RegisterDynamicTools([]DynamicToolDefinition{{
		Tool: Tool{Name: "runtime.unbounded.read", Operation: Read, Risk: Low, Domain: "runtime", ResourceType: "item"},
		InputSchema: map[string]DynamicInputField{
			"environment": {Type: "string", Required: true},
			"count":       {Type: "integer", Required: true},
		},
	}}); err != nil {
		t.Fatalf("RegisterDynamicTools returned %v", err)
	}
	tool, _ := Lookup("runtime.unbounded.read")
	if err := ValidateInput(tool, map[string]any{"environment": "prod", "count": -999999}); err != nil {
		t.Fatalf("ValidateInput on unbounded field = %v, want accepted", err)
	}
}

// TestRegisterDynamicToolsRejectsUnsatisfiableBounds fails a bad capability at
// publish time rather than on the operator's first request.
func TestRegisterDynamicToolsRejectsUnsatisfiableBounds(t *testing.T) {
	ResetDynamicToolsForTest()
	t.Cleanup(ResetDynamicToolsForTest)

	for name, field := range map[string]DynamicInputField{
		"min above max":      {Type: "integer", Required: true, Min: boundOf(100), Max: boundOf(10)},
		"bounds on a string": {Type: "string", Required: true, Min: boundOf(1)},
		"non-finite min":     {Type: "number", Required: true, Min: boundOf(math.Inf(-1))},
		"nan max":            {Type: "number", Required: true, Max: boundOf(math.NaN())},
	} {
		t.Run(name, func(t *testing.T) {
			err := RegisterDynamicTools([]DynamicToolDefinition{{
				Tool: Tool{Name: "runtime.bounds.read", Operation: Read, Risk: Low, Domain: "runtime", ResourceType: "item"},
				InputSchema: map[string]DynamicInputField{
					"environment": {Type: "string", Required: true},
					"value":       field,
				},
			}})
			if err == nil {
				t.Fatal("RegisterDynamicTools accepted an unsatisfiable bound")
			}
			if _, ok := Lookup("runtime.bounds.read"); ok {
				t.Fatal("rejected definition was still registered")
			}
		})
	}
}

// TestDynamicInputSchemaCopiesBounds catches the shallow-copy bug: cloning the
// map but sharing the Min/Max pointers would let a caller rewrite the
// registry's guardrail through the copy it was handed.
func TestDynamicInputSchemaCopiesBounds(t *testing.T) {
	ResetDynamicToolsForTest()
	t.Cleanup(ResetDynamicToolsForTest)

	if err := RegisterDynamicTools([]DynamicToolDefinition{{
		Tool: Tool{Name: "runtime.clone.read", Operation: Read, Risk: Low, Domain: "runtime", ResourceType: "item"},
		InputSchema: map[string]DynamicInputField{
			"environment": {Type: "string", Required: true},
			"value":       {Type: "integer", Required: true, Min: boundOf(1), Max: boundOf(10)},
		},
	}}); err != nil {
		t.Fatalf("RegisterDynamicTools returned %v", err)
	}

	schema, ok := DynamicInputSchema("runtime.clone.read")
	if !ok {
		t.Fatal("DynamicInputSchema returned ok=false")
	}
	*schema["value"].Max = 999999

	tool, _ := Lookup("runtime.clone.read")
	if err := ValidateInput(tool, map[string]any{"environment": "prod", "value": 500}); err == nil {
		t.Fatal("mutating the returned copy widened the registry's bound")
	}
}

func boundOf(value float64) *float64 {
	return &value
}

// --- 热配置：UnregisterDynamicTools 测试 ---

func TestUnregisterDynamicToolsRemovesRegistered(t *testing.T) {
	ResetDynamicToolsForTest()
	t.Cleanup(ResetDynamicToolsForTest)

	if err := RegisterDynamicTools([]DynamicToolDefinition{{
		Tool: Tool{Name: "grafana.query.read", Operation: Read, Risk: Low, Domain: "grafana", ResourceType: "mcp"},
		InputSchema: map[string]DynamicInputField{
			"environment": {Type: "string", Required: true},
		},
	}}); err != nil {
		t.Fatalf("RegisterDynamicTools: %v", err)
	}

	if err := UnregisterDynamicTools([]string{"grafana.query.read"}); err != nil {
		t.Fatalf("UnregisterDynamicTools: %v", err)
	}

	if _, ok := Lookup("grafana.query.read"); ok {
		t.Fatal("Lookup should return false after unregister")
	}
	if IsDynamic("grafana.query.read") {
		t.Fatal("IsDynamic should return false after unregister")
	}
}

func TestUnregisterDynamicToolsRemovesInputSchema(t *testing.T) {
	ResetDynamicToolsForTest()
	t.Cleanup(ResetDynamicToolsForTest)

	if err := RegisterDynamicTools([]DynamicToolDefinition{{
		Tool: Tool{Name: "loki.search.read", Operation: Read, Risk: Low, Domain: "loki", ResourceType: "mcp"},
		InputSchema: map[string]DynamicInputField{
			"environment": {Type: "string", Required: true},
			"query":       {Type: "string", Required: true},
		},
	}}); err != nil {
		t.Fatalf("RegisterDynamicTools: %v", err)
	}

	if err := UnregisterDynamicTools([]string{"loki.search.read"}); err != nil {
		t.Fatalf("UnregisterDynamicTools: %v", err)
	}

	if _, ok := DynamicInputSchema("loki.search.read"); ok {
		t.Fatal("DynamicInputSchema should return false after unregister")
	}
}

func TestUnregisterDynamicToolsRejectsStaticTools(t *testing.T) {
	ResetDynamicToolsForTest()
	t.Cleanup(ResetDynamicToolsForTest)

	err := UnregisterDynamicTools([]string{ClusterStatusRead})
	if err == nil {
		t.Fatal("UnregisterDynamicTools should reject static tools")
	}
}

func TestUnregisterDynamicToolsIgnoresUnknownNames(t *testing.T) {
	ResetDynamicToolsForTest()
	t.Cleanup(ResetDynamicToolsForTest)

	// 未注册的名字不应报错（幂等，便于 Reload 增量注销）
	if err := UnregisterDynamicTools([]string{"nonexistent.tool"}); err != nil {
		t.Fatalf("UnregisterDynamicTools unknown name: %v", err)
	}
}

func TestUnregisterDynamicToolsAllListUpdated(t *testing.T) {
	ResetDynamicToolsForTest()
	t.Cleanup(ResetDynamicToolsForTest)

	if err := RegisterDynamicTools([]DynamicToolDefinition{{
		Tool: Tool{Name: "svc.tool1.read", Operation: Read, Risk: Low, Domain: "svc", ResourceType: "mcp"},
		InputSchema: map[string]DynamicInputField{
			"environment": {Type: "string", Required: true},
		},
	}}); err != nil {
		t.Fatalf("RegisterDynamicTools: %v", err)
	}

	beforeCount := len(All())
	if err := UnregisterDynamicTools([]string{"svc.tool1.read"}); err != nil {
		t.Fatalf("UnregisterDynamicTools: %v", err)
	}
	afterCount := len(All())
	if afterCount != beforeCount-1 {
		t.Fatalf("All() count after unregister = %d, want %d", afterCount, beforeCount-1)
	}
}
