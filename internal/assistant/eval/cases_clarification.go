package eval

// clarificationCases covers cases where the planner should signal that the
// user message is missing required information. 20 cases total, requiring
// ≥90% pass.
//
// EinoPlanner signals clarification by returning tool_name == "" in the LLM
// JSON, which causes EinoPlanner.Plan to return ErrClarificationNeeded.
//
// Distribution:
//   - missing environment:     5 cases
//   - missing key parameter:   5 cases
//   - ambiguous domain:        5 cases
//   - multiple missing fields: 5 cases
var clarificationCases = []Case{
	// === missing environment (5 cases) ===
	{
		Name:        "clarification/no_env_cluster",
		Category:    CategoryClarification,
		UserMessage: "查看集群状态",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":null,"confidence":0.3,"explanation":"environment missing"}`,
		ExpectedIntent: ExpectedIntent{
			Clarification: true,
		},
		Notes: "缺 environment",
	},
	{
		Name:        "clarification/no_env_kafka_lag",
		Category:    CategoryClarification,
		UserMessage: "查 kafka orders group 的 lag",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":null,"confidence":0.3,"explanation":"environment missing"}`,
		ExpectedIntent: ExpectedIntent{
			Clarification: true,
		},
		Notes: "缺 environment（kafka）",
	},
	{
		Name:        "clarification/no_env_minio_health",
		Category:    CategoryClarification,
		UserMessage: "查 minio orders bucket 健康",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":null,"confidence":0.3,"explanation":"environment missing"}`,
		ExpectedIntent: ExpectedIntent{
			Clarification: true,
		},
		Notes: "缺 environment（minio）",
	},
	{
		Name:        "clarification/no_env_gluster_health",
		Category:    CategoryClarification,
		UserMessage: "查 glusterfs data volume 健康",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":null,"confidence":0.3,"explanation":"environment missing"}`,
		ExpectedIntent: ExpectedIntent{
			Clarification: true,
		},
		Notes: "缺 environment（glusterfs）",
	},
	{
		Name:        "clarification/no_env_ambiguous",
		Category:    CategoryClarification,
		UserMessage: "查下集群啥情况",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":null,"confidence":0.2,"explanation":"environment missing and ambiguous"}`,
		ExpectedIntent: ExpectedIntent{
			Clarification: true,
		},
		Notes: "缺 environment + 模糊",
	},

	// === missing key parameter (5 cases) ===
	{
		Name:        "clarification/no_group_kafka_lag",
		Category:    CategoryClarification,
		UserMessage: "查 prod kafka 的 lag",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":null,"confidence":0.3,"explanation":"group missing"}`,
		ExpectedIntent: ExpectedIntent{
			Clarification: true,
		},
		Notes: "缺 group 名",
	},
	{
		Name:        "clarification/no_bucket_minio",
		Category:    CategoryClarification,
		UserMessage: "查 prod minio bucket 健康",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":null,"confidence":0.3,"explanation":"bucket missing"}`,
		ExpectedIntent: ExpectedIntent{
			Clarification: true,
		},
		Notes: "缺 bucket 名",
	},
	{
		Name:        "clarification/no_volume_gluster",
		Category:    CategoryClarification,
		UserMessage: "查 prod glusterfs volume 健康",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":null,"confidence":0.3,"explanation":"volume missing"}`,
		ExpectedIntent: ExpectedIntent{
			Clarification: true,
		},
		Notes: "缺 volume 名",
	},
	{
		Name:        "clarification/no_topic_kafka",
		Category:    CategoryClarification,
		UserMessage: "查 prod kafka topic 的 retention",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":null,"confidence":0.3,"explanation":"topic missing"}`,
		ExpectedIntent: ExpectedIntent{
			Clarification: true,
		},
		Notes: "缺 topic 名（topic.retention.set 路径）",
	},
	{
		Name:        "clarification/no_hours_retention",
		Category:    CategoryClarification,
		UserMessage: "查 prod kafka orders topic 的 retention",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":null,"confidence":0.3,"explanation":"retention hours missing"}`,
		ExpectedIntent: ExpectedIntent{
			Clarification: true,
		},
		Notes: "缺 retention hours",
	},

	// === ambiguous domain (5 cases) ===
	{
		Name:        "clarification/ambiguous_lag",
		Category:    CategoryClarification,
		UserMessage: "查 prod lag",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":null,"confidence":0.2,"explanation":"kafka vs minio ambiguous"}`,
		ExpectedIntent: ExpectedIntent{
			Clarification: true,
		},
		Notes: "kafka/minio 歧义",
	},
	{
		Name:        "clarification/ambiguous_health",
		Category:    CategoryClarification,
		UserMessage: "查 prod 健康",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":null,"confidence":0.2,"explanation":"domain ambiguous for health"}`,
		ExpectedIntent: ExpectedIntent{
			Clarification: true,
		},
		Notes: "域不明的健康查询",
	},
	{
		Name:        "clarification/ambiguous_status",
		Category:    CategoryClarification,
		UserMessage: "查 prod 状态",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":null,"confidence":0.2,"explanation":"status ambiguous"}`,
		ExpectedIntent: ExpectedIntent{
			Clarification: true,
		},
		Notes: "域不明的状态查询",
	},
	{
		Name:        "clarification/ambiguous_orders",
		Category:    CategoryClarification,
		UserMessage: "查 prod orders",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":null,"confidence":0.2,"explanation":"orders could be kafka group or minio bucket"}`,
		ExpectedIntent: ExpectedIntent{
			Clarification: true,
		},
		Notes: "orders 可能是 kafka group 或 minio bucket",
	},
	{
		Name:        "clarification/ambiguous_data",
		Category:    CategoryClarification,
		UserMessage: "查 prod data",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":null,"confidence":0.2,"explanation":"data could be minio bucket or glusterfs volume"}`,
		ExpectedIntent: ExpectedIntent{
			Clarification: true,
		},
		Notes: "data 可能是 minio bucket 或 glusterfs volume",
	},

	// === multiple missing fields (5 cases) ===
	{
		Name:        "clarification/missing_env_and_group",
		Category:    CategoryClarification,
		UserMessage: "查 kafka lag",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":null,"confidence":0.1,"explanation":"env and group missing"}`,
		ExpectedIntent: ExpectedIntent{
			Clarification: true,
		},
		Notes: "缺 environment + group",
	},
	{
		Name:        "clarification/missing_env_and_bucket",
		Category:    CategoryClarification,
		UserMessage: "查 minio bucket 健康",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":null,"confidence":0.1,"explanation":"env and bucket missing"}`,
		ExpectedIntent: ExpectedIntent{
			Clarification: true,
		},
		Notes: "缺 environment + bucket",
	},
	{
		Name:        "clarification/missing_env_and_volume",
		Category:    CategoryClarification,
		UserMessage: "查 glusterfs volume 健康",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":null,"confidence":0.1,"explanation":"env and volume missing"}`,
		ExpectedIntent: ExpectedIntent{
			Clarification: true,
		},
		Notes: "缺 environment + volume",
	},
	{
		Name:        "clarification/missing_all",
		Category:    CategoryClarification,
		UserMessage: "查下情况",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":null,"confidence":0.05,"explanation":"everything missing"}`,
		ExpectedIntent: ExpectedIntent{
			Clarification: true,
		},
		Notes: "几乎全部缺失",
	},
	{
		Name:        "clarification/missing_garbled",
		Category:    CategoryClarification,
		UserMessage: "asdf qwer",
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":null,"confidence":0.0,"explanation":"unintelligible"}`,
		ExpectedIntent: ExpectedIntent{
			Clarification: true,
		},
		Notes: "无意义输入",
	},
}
