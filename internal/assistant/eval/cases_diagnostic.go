package eval

// diagnosticCases covers cases where the planner should route to the
// diagnostic package instead of a concrete tool. 20 cases total, requiring
// ≥90% pass.
//
// EinoPlanner signals diagnostic by returning a non-null diagnostic object
// in the LLM JSON. The runner checks intent.Diagnostic != nil.
//
// Distribution:
//   - glusterfs health:     3 cases
//   - minio health:         3 cases
//   - kafka health:         3 cases
//   - diagnostic variant:   1 case
//   - P2 cost.analyze:      2 cases
//   - P2 sla.analyze:       2 cases
//   - P2 incident.review:   2 cases
//   - P2 health.check:      2 cases
//   - P2 performance:       2 cases
var diagnosticCases = []Case{
	// === glusterfs health (3 cases) ===
	{
		Name:        "diagnostic/glusterfs_data_prod",
		Category:    CategoryDiagnostic,
		UserMessage: "检查 prod glusterfs data volume 健康",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":{"domain":"glusterfs","environment":"prod","resource_type":"volume","resource_name":"data","runbook":"health"},"confidence":0.88,"explanation":"check volume health"}`,
		ExpectedIntent: ExpectedIntent{
			Diagnostic: true,
		},
		Notes: "glusterfs 健康诊断 prod",
	},
	{
		Name:        "diagnostic/glusterfs_logs_staging",
		Category:    CategoryDiagnostic,
		UserMessage: "检查 staging glusterfs logs volume 健康",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":{"domain":"glusterfs","environment":"staging","resource_type":"volume","resource_name":"logs","runbook":"health"},"confidence":0.87,"explanation":"check logs volume health"}`,
		ExpectedIntent: ExpectedIntent{
			Diagnostic: true,
		},
		Notes: "glusterfs 健康诊断 staging",
	},
	{
		Name:        "diagnostic/glusterfs_backup_dev",
		Category:    CategoryDiagnostic,
		UserMessage: "检查 dev glusterfs backup volume 健康",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":{"domain":"glusterfs","environment":"dev","resource_type":"volume","resource_name":"backup","runbook":"health"},"confidence":0.86,"explanation":"check backup volume health"}`,
		ExpectedIntent: ExpectedIntent{
			Diagnostic: true,
		},
		Notes: "glusterfs 健康诊断 dev",
	},

	// === minio health (3 cases) ===
	{
		Name:        "diagnostic/minio_orders_prod",
		Category:    CategoryDiagnostic,
		UserMessage: "检查 prod minio orders bucket 健康",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":{"domain":"minio","environment":"prod","resource_type":"bucket","resource_name":"orders","runbook":"health"},"confidence":0.88,"explanation":"check bucket health"}`,
		ExpectedIntent: ExpectedIntent{
			Diagnostic: true,
		},
		Notes: "minio 健康诊断 prod",
	},
	{
		Name:        "diagnostic/minio_images_staging",
		Category:    CategoryDiagnostic,
		UserMessage: "检查 staging minio images bucket 健康",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":{"domain":"minio","environment":"staging","resource_type":"bucket","resource_name":"images","runbook":"health"},"confidence":0.87,"explanation":"check images bucket health"}`,
		ExpectedIntent: ExpectedIntent{
			Diagnostic: true,
		},
		Notes: "minio 健康诊断 staging",
	},
	{
		Name:        "diagnostic/minio_logs_dev",
		Category:    CategoryDiagnostic,
		UserMessage: "检查 dev minio logs bucket 健康",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":{"domain":"minio","environment":"dev","resource_type":"bucket","resource_name":"logs","runbook":"health"},"confidence":0.86,"explanation":"check logs bucket health"}`,
		ExpectedIntent: ExpectedIntent{
			Diagnostic: true,
		},
		Notes: "minio 健康诊断 dev",
	},

	// === kafka health (3 cases) ===
	{
		Name:        "diagnostic/kafka_orders_prod",
		Category:    CategoryDiagnostic,
		UserMessage: "检查 prod kafka orders consumer group 健康",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":{"domain":"kafka","environment":"prod","resource_type":"consumer_group","resource_name":"orders","runbook":"health"},"confidence":0.88,"explanation":"check consumer group health"}`,
		ExpectedIntent: ExpectedIntent{
			Diagnostic: true,
		},
		Notes: "kafka 健康诊断 prod",
	},
	{
		Name:        "diagnostic/kafka_payments_staging",
		Category:    CategoryDiagnostic,
		UserMessage: "检查 staging kafka payments consumer group 健康",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":{"domain":"kafka","environment":"staging","resource_type":"consumer_group","resource_name":"payments","runbook":"health"},"confidence":0.87,"explanation":"check payments group health"}`,
		ExpectedIntent: ExpectedIntent{
			Diagnostic: true,
		},
		Notes: "kafka 健康诊断 staging",
	},
	{
		Name:        "diagnostic/kafka_analytics_dev",
		Category:    CategoryDiagnostic,
		UserMessage: "检查 dev kafka analytics consumer group 健康",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":{"domain":"kafka","environment":"dev","resource_type":"consumer_group","resource_name":"analytics","runbook":"health"},"confidence":0.86,"explanation":"check analytics group health"}`,
		ExpectedIntent: ExpectedIntent{
			Diagnostic: true,
		},
		Notes: "kafka 健康诊断 dev",
	},

	// === diagnostic variant (1 case) ===
	{
		Name:        "diagnostic/variant_diagnose_keyword",
		Category:    CategoryDiagnostic,
		UserMessage: "诊断 prod kafka orders consumer group",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":{"domain":"kafka","environment":"prod","resource_type":"consumer_group","resource_name":"orders","runbook":"health"},"confidence":0.85,"explanation":"diagnose keyword variant"}`,
		ExpectedIntent: ExpectedIntent{
			Diagnostic: true,
		},
		Notes: "用「诊断」关键词替代「检查...健康」",
	},

	// === P2 cost.analyze (2 cases) ===
	{
		Name:        "diagnostic/cost_analyze_prod",
		Category:    CategoryDiagnostic,
		UserMessage: "分析一下 prod 成本，有哪些闲置资源",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":{"domain":"cost","environment":"prod","resource_type":"resource","resource_name":"","runbook":"cost_analyze"},"confidence":0.86,"explanation":"cost analysis for prod"}`,
		ExpectedIntent: ExpectedIntent{
			Diagnostic: true,
		},
		Notes: "P2 cost.analyze 关键词命中，走 diagnostic 路径产出成本分析",
	},
	{
		Name:        "diagnostic/cost_optimize_idle",
		Category:    CategoryDiagnostic,
		UserMessage: "有哪些闲置资源可以优化成本",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":{"domain":"cost","environment":"","resource_type":"resource","resource_name":"","runbook":"cost_optimize"},"confidence":0.85,"explanation":"identify idle resources for cost optimization"}`,
		ExpectedIntent: ExpectedIntent{
			Diagnostic: true,
		},
		Notes: "P2 cost.analyze 闲置资源优化场景，无指定 environment",
	},

	// === P2 sla.analyze (2 cases) ===
	{
		Name:        "diagnostic/sla_analyze_order_prod",
		Category:    CategoryDiagnostic,
		UserMessage: "看一下 order 服务 SLA 达成率",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":{"domain":"sla","environment":"prod","resource_type":"service","resource_name":"order","runbook":"sla_analyze"},"confidence":0.87,"explanation":"sla achievement for order service"}`,
		ExpectedIntent: ExpectedIntent{
			Diagnostic: true,
		},
		Notes: "P2 sla.analyze 关键词命中，diagnostic 路径产出 SLA 分析",
	},
	{
		Name:        "diagnostic/sla_violation_risk",
		Category:    CategoryDiagnostic,
		UserMessage: "SLA 有违反风险吗",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":{"domain":"sla","environment":"","resource_type":"service","resource_name":"","runbook":"sla_violation"},"confidence":0.84,"explanation":"sla violation risk assessment"}`,
		ExpectedIntent: ExpectedIntent{
			Diagnostic: true,
		},
		Notes: "P2 sla.analyze 违反风险评估场景",
	},

	// === P2 incident.review (2 cases) ===
	{
		Name:        "diagnostic/incident_review_recent",
		Category:    CategoryDiagnostic,
		UserMessage: "上次故障做个复盘",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":{"domain":"incident","environment":"","resource_type":"incident","resource_name":"recent","runbook":"incident_review"},"confidence":0.86,"explanation":"recent incident review"}`,
		ExpectedIntent: ExpectedIntent{
			Diagnostic: true,
		},
		Notes: "P2 incident.review 关键词命中，diagnostic 路径产出复盘",
	},
	{
		Name:        "diagnostic/incident_postmortem",
		Category:    CategoryDiagnostic,
		UserMessage: "生成事故 postmortem",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":{"domain":"incident","environment":"","resource_type":"incident","resource_name":"postmortem","runbook":"incident_review"},"confidence":0.85,"explanation":"generate postmortem report"}`,
		ExpectedIntent: ExpectedIntent{
			Diagnostic: true,
		},
		Notes: "P2 incident.review postmortem 场景，英文关键词",
	},

	// === P2 health.check (2 cases) ===
	{
		Name:        "diagnostic/health_check_prod",
		Category:    CategoryDiagnostic,
		UserMessage: "给 prod 环境做个体检",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":{"domain":"health","environment":"prod","resource_type":"cluster","resource_name":"","runbook":"health_check"},"confidence":0.87,"explanation":"prod health check"}`,
		ExpectedIntent: ExpectedIntent{
			Diagnostic: true,
		},
		Notes: "P2 health.check 关键词命中，diagnostic 路径产出体检报告",
	},
	{
		Name:        "diagnostic/health_inspection",
		Category:    CategoryDiagnostic,
		UserMessage: "集群健康巡检",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":{"domain":"health","environment":"","resource_type":"cluster","resource_name":"","runbook":"health_inspection"},"confidence":0.86,"explanation":"cluster health inspection"}`,
		ExpectedIntent: ExpectedIntent{
			Diagnostic: true,
		},
		Notes: "P2 health.check 巡检场景",
	},

	// === P2 performance.bottleneck (2 cases) ===
	{
		Name:        "diagnostic/perf_bottleneck_slow",
		Category:    CategoryDiagnostic,
		UserMessage: "order 服务响应变慢了排查一下",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":{"domain":"performance","environment":"prod","resource_type":"service","resource_name":"order","runbook":"bottleneck"},"confidence":0.86,"explanation":"order service slow response bottleneck"}`,
		ExpectedIntent: ExpectedIntent{
			Diagnostic: true,
		},
		Notes: "P2 performance.bottleneck 关键词命中，diagnostic 路径定位瓶颈",
	},
	{
		Name:        "diagnostic/perf_bottleneck_locate",
		Category:    CategoryDiagnostic,
		UserMessage: "定位一下性能瓶颈",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":{"domain":"performance","environment":"","resource_type":"service","resource_name":"","runbook":"bottleneck_locate"},"confidence":0.85,"explanation":"locate performance bottleneck"}`,
		ExpectedIntent: ExpectedIntent{
			Diagnostic: true,
		},
		Notes: "P2 performance.bottleneck 通用定位场景",
	},
}
