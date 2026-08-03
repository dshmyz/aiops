package eval

import (
	"github.com/gracegaoya/ai-operations-copilot/internal/assistant"
	"github.com/gracegaoya/ai-operations-copilot/internal/diagnostics"
)

// historyCases covers multi-turn cases where the planner should resolve
// references to previous turns by reading the structured [Last Intent] block
// appended to assistant turns. 10 cases total, requiring ≥90% pass.
//
// Each case provides a History of prior turns whose assistant entries carry
// a populated Intent. The user message references the prior turn via
// phrases like "同 environment", "同一个 group", "再查一个".
var historyCases = []Case{
	// === same environment reference ===
	{
		Name:        "history/same_env_kafka_lag",
		Category:    CategoryHistory,
		UserMessage: "同 environment 再查一个 payments group",
		History: []assistant.Turn{
			{Role: "user", Content: "查 prod kafka orders group 的 lag"},
			{
				Role:    "assistant",
				Content: "lag = 1234",
				Intent: &assistant.Intent{
					ToolName: "kafka.consumer_lag.read",
					Input:    map[string]any{"environment": "prod", "group": "orders"},
				},
			},
		},
		ScriptedLLM: `{"tool_name":"kafka.consumer_lag.read","input":{"environment":"prod","group":"payments"},"diagnostic":null,"confidence":0.9,"explanation":"same env, new group"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "kafka.consumer_lag.read",
			Input:    map[string]any{"environment": "prod", "group": "payments"},
		},
		Notes: "同 environment → 从上一轮 Intent.environment=prod 继承",
	},
	{
		Name:        "history/same_env_minio_health",
		Category:    CategoryHistory,
		UserMessage: "同 environment 查下 images bucket 健康",
		History: []assistant.Turn{
			{Role: "user", Content: "查 prod minio orders bucket 健康"},
			{
				Role:    "assistant",
				Content: "orders bucket 健康",
				Intent: &assistant.Intent{
					ToolName: "minio.bucket.health.read",
					Input:    map[string]any{"environment": "prod", "bucket": "orders"},
				},
			},
		},
		ScriptedLLM: `{"tool_name":"minio.bucket.health.read","input":{"environment":"prod","bucket":"images"},"diagnostic":null,"confidence":0.9,"explanation":"same env minio"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "minio.bucket.health.read",
			Input:    map[string]any{"environment": "prod", "bucket": "images"},
		},
		Notes: "同 environment → 从 minio Intent 继承",
	},
	{
		Name:        "history/same_env_gluster_health",
		Category:    CategoryHistory,
		UserMessage: "同 environment 查下 logs volume 健康",
		History: []assistant.Turn{
			{Role: "user", Content: "查 prod glusterfs data volume 健康"},
			{
				Role:    "assistant",
				Content: "data volume 健康",
				Intent: &assistant.Intent{
					ToolName: "glusterfs.volume.health.read",
					Input:    map[string]any{"environment": "prod", "volume": "data"},
				},
			},
		},
		ScriptedLLM: `{"tool_name":"glusterfs.volume.health.read","input":{"environment":"prod","volume":"logs"},"diagnostic":null,"confidence":0.9,"explanation":"same env gluster"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "glusterfs.volume.health.read",
			Input:    map[string]any{"environment": "prod", "volume": "logs"},
		},
		Notes: "同 environment → 从 gluster Intent 继承",
	},

	// === same group/bucket/volume, different environment ===
	{
		Name:        "history/same_group_diff_env",
		Category:    CategoryHistory,
		UserMessage: "查 staging 同一个 group 的 lag",
		History: []assistant.Turn{
			{Role: "user", Content: "查 prod kafka orders group 的 lag"},
			{
				Role:    "assistant",
				Content: "lag = 1234",
				Intent: &assistant.Intent{
					ToolName: "kafka.consumer_lag.read",
					Input:    map[string]any{"environment": "prod", "group": "orders"},
				},
			},
		},
		ScriptedLLM: `{"tool_name":"kafka.consumer_lag.read","input":{"environment":"staging","group":"orders"},"diagnostic":null,"confidence":0.9,"explanation":"same group, new env"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "kafka.consumer_lag.read",
			Input:    map[string]any{"environment": "staging", "group": "orders"},
		},
		Notes: "同一个 group → 从上一轮继承 group=orders",
	},
	{
		Name:        "history/same_bucket_diff_env",
		Category:    CategoryHistory,
		UserMessage: "查 staging 同一个 bucket",
		History: []assistant.Turn{
			{Role: "user", Content: "查 prod minio orders bucket 健康"},
			{
				Role:    "assistant",
				Content: "orders bucket 健康",
				Intent: &assistant.Intent{
					ToolName: "minio.bucket.health.read",
					Input:    map[string]any{"environment": "prod", "bucket": "orders"},
				},
			},
		},
		ScriptedLLM: `{"tool_name":"minio.bucket.health.read","input":{"environment":"staging","bucket":"orders"},"diagnostic":null,"confidence":0.9,"explanation":"same bucket, new env"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "minio.bucket.health.read",
			Input:    map[string]any{"environment": "staging", "bucket": "orders"},
		},
		Notes: "同一个 bucket → 继承 bucket=orders",
	},

	// === diagnostic context reference ===
	{
		Name:        "history/refer_diagnostic_domain",
		Category:    CategoryHistory,
		UserMessage: "再查这个域的 orders consumer group lag",
		History: []assistant.Turn{
			{Role: "user", Content: "检查 prod kafka 健康状态"},
			{
				Role:    "assistant",
				Content: "kafka 健康",
				Intent: &assistant.Intent{
					Diagnostic: diagReq("kafka", "prod", "consumer_group", ""),
				},
			},
		},
		ScriptedLLM: `{"tool_name":"kafka.consumer_lag.read","input":{"environment":"prod","group":"orders"},"diagnostic":null,"confidence":0.88,"explanation":"diagnostic referenced domain"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "kafka.consumer_lag.read",
			Input:    map[string]any{"environment": "prod", "group": "orders"},
		},
		Notes: "这个域 → 从 diagnostic.Domain=kafka 继承",
	},
	{
		Name:        "history/refer_diagnostic_env",
		Category:    CategoryHistory,
		UserMessage: "同环境查 minio backups bucket 健康",
		History: []assistant.Turn{
			{Role: "user", Content: "检查 prod glusterfs 健康状态"},
			{
				Role:    "assistant",
				Content: "glusterfs 健康",
				Intent: &assistant.Intent{
					Diagnostic: diagReq("glusterfs", "prod", "volume", ""),
				},
			},
		},
		ScriptedLLM: `{"tool_name":"minio.bucket.health.read","input":{"environment":"prod","bucket":"backups"},"diagnostic":null,"confidence":0.87,"explanation":"diag env referenced"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "minio.bucket.health.read",
			Input:    map[string]any{"environment": "prod", "bucket": "backups"},
		},
		Notes: "同环境 → 从 diagnostic.Environment=prod 继承",
	},

	// === 3-turn history ===
	{
		Name:        "history/3_turn_chained",
		Category:    CategoryHistory,
		UserMessage: "同上 environment 再查 analytics group",
		History: []assistant.Turn{
			{Role: "user", Content: "查 prod kafka orders group 的 lag"},
			{
				Role:    "assistant",
				Content: "lag = 1234",
				Intent: &assistant.Intent{
					ToolName: "kafka.consumer_lag.read",
					Input:    map[string]any{"environment": "prod", "group": "orders"},
				},
			},
			{Role: "user", Content: "再查 payments group"},
			{
				Role:    "assistant",
				Content: "lag = 5678",
				Intent: &assistant.Intent{
					ToolName: "kafka.consumer_lag.read",
					Input:    map[string]any{"environment": "prod", "group": "payments"},
				},
			},
		},
		ScriptedLLM: `{"tool_name":"kafka.consumer_lag.read","input":{"environment":"prod","group":"analytics"},"diagnostic":null,"confidence":0.9,"explanation":"chained reference"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "kafka.consumer_lag.read",
			Input:    map[string]any{"environment": "prod", "group": "analytics"},
		},
		Notes: "3 轮历史，同上 environment → 从最近 Intent 继承",
	},

	// === clarification after history ===
	{
		Name:        "history/clarification_after_intent",
		Category:    CategoryHistory,
		UserMessage: "再查一个",
		History: []assistant.Turn{
			{Role: "user", Content: "查 prod kafka orders group 的 lag"},
			{
				Role:    "assistant",
				Content: "lag = 1234",
				Intent: &assistant.Intent{
					ToolName: "kafka.consumer_lag.read",
					Input:    map[string]any{"environment": "prod", "group": "orders"},
				},
			},
		},
		ScriptedLLM: `{"tool_name":"","input":{},"diagnostic":null,"confidence":0.3,"explanation":"group missing"}`,
		ExpectedIntent: ExpectedIntent{
			Clarification: true,
		},
		Notes: "「再查一个」缺 group 名 → clarification",
	},

	// === cross-tool same env ===
	{
		Name:        "history/cross_tool_same_env",
		Category:    CategoryHistory,
		UserMessage: "同 environment 查 minio orders bucket",
		History: []assistant.Turn{
			{Role: "user", Content: "查 prod kafka orders group 的 lag"},
			{
				Role:    "assistant",
				Content: "lag = 1234",
				Intent: &assistant.Intent{
					ToolName: "kafka.consumer_lag.read",
					Input:    map[string]any{"environment": "prod", "group": "orders"},
				},
			},
		},
		ScriptedLLM: `{"tool_name":"minio.bucket.health.read","input":{"environment":"prod","bucket":"orders"},"diagnostic":null,"confidence":0.88,"explanation":"cross tool same env"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "minio.bucket.health.read",
			Input:    map[string]any{"environment": "prod", "bucket": "orders"},
		},
		Notes: "跨工具但同 environment → 从 kafka Intent 继承 env，切到 minio",
	},
}

// diagReq is a constructor helper that keeps case data terse. Runbook is left
// empty because eval does not check it.
func diagReq(domain, env, resourceType, resourceName string) *diagnostics.Request {
	return &diagnostics.Request{
		Domain:       domain,
		Environment:  env,
		ResourceType: resourceType,
		ResourceName: resourceName,
	}
}
