package eval

// toolCases covers cases where the planner should resolve a concrete tool
// intent (ToolName + Input). 62 cases total, requiring 100% pass.
//
// Distribution:
//   - cluster.status.read:           12 cases
//   - kafka.consumer_lag.read:       12 cases
//   - minio.bucket.health.read:      12 cases
//   - glusterfs.volume.health.read:  12 cases
//   - fuzzy / boundary:               2 cases
//   - alert.query:                    8 cases
//   - event.query:                    2 cases
//   - task.query:                     2 cases
var toolCases = []Case{
	// === cluster.status.read (12 cases) ===
	{
		Name:        "tool/cluster_status_prod_zh",
		Category:    CategoryTool,
		UserMessage: "查看 prod 集群状态",
		ScriptedLLM: `{"tool_name":"cluster.status.read","input":{"environment":"prod"},"diagnostic":null,"confidence":0.92,"explanation":"read cluster status"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "cluster.status.read",
			Input:    map[string]any{"environment": "prod"},
		},
		Notes: "中文 + prod 环境",
	},
	{
		Name:        "tool/cluster_status_staging_zh",
		Category:    CategoryTool,
		UserMessage: "查一下 staging 集群状态",
		ScriptedLLM: `{"tool_name":"cluster.status.read","input":{"environment":"staging"},"diagnostic":null,"confidence":0.9,"explanation":"read staging cluster status"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "cluster.status.read",
			Input:    map[string]any{"environment": "staging"},
		},
		Notes: "中文 + staging 环境",
	},
	{
		Name:        "tool/cluster_status_dev_en",
		Category:    CategoryTool,
		UserMessage: "check dev cluster status",
		ScriptedLLM: `{"tool_name":"cluster.status.read","input":{"environment":"dev"},"diagnostic":null,"confidence":0.9,"explanation":"read dev cluster status"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "cluster.status.read",
			Input:    map[string]any{"environment": "dev"},
		},
		Notes: "英文 + dev 环境",
	},
	{
		Name:        "tool/cluster_status_prod_en",
		Category:    CategoryTool,
		UserMessage: "show me the status of prod cluster",
		ScriptedLLM: `{"tool_name":"cluster.status.read","input":{"environment":"prod"},"diagnostic":null,"confidence":0.91,"explanation":"read prod cluster status"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "cluster.status.read",
			Input:    map[string]any{"environment": "prod"},
		},
		Notes: "英文长句",
	},
	{
		Name:        "tool/cluster_health_zh",
		Category:    CategoryTool,
		UserMessage: "prod 集群健康吗",
		ScriptedLLM: `{"tool_name":"cluster.status.read","input":{"environment":"prod"},"diagnostic":null,"confidence":0.88,"explanation":"cluster health check"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "cluster.status.read",
			Input:    map[string]any{"environment": "prod"},
		},
		Notes: "健康关键词触发",
	},
	{
		Name:        "tool/cluster_state_en",
		Category:    CategoryTool,
		UserMessage: "what is the state of prod cluster",
		ScriptedLLM: `{"tool_name":"cluster.status.read","input":{"environment":"prod"},"diagnostic":null,"confidence":0.89,"explanation":"cluster state query"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "cluster.status.read",
			Input:    map[string]any{"environment": "prod"},
		},
		Notes: "state 同义",
	},
	{
		Name:        "tool/cluster_status_zh_kan",
		Category:    CategoryTool,
		UserMessage: "看下 prod 集群啥情况",
		ScriptedLLM: `{"tool_name":"cluster.status.read","input":{"environment":"prod"},"diagnostic":null,"confidence":0.85,"explanation":"colloquial status check"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "cluster.status.read",
			Input:    map[string]any{"environment": "prod"},
		},
		Notes: "口语化模糊表达",
	},
	{
		Name:        "tool/cluster_status_staging_en",
		Category:    CategoryTool,
		UserMessage: "staging cluster status please",
		ScriptedLLM: `{"tool_name":"cluster.status.read","input":{"environment":"staging"},"diagnostic":null,"confidence":0.9,"explanation":"staging status"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "cluster.status.read",
			Input:    map[string]any{"environment": "staging"},
		},
		Notes: "极简英文",
	},
	{
		Name:        "tool/cluster_status_dev_zh",
		Category:    CategoryTool,
		UserMessage: "dev 集群现在啥状态",
		ScriptedLLM: `{"tool_name":"cluster.status.read","input":{"environment":"dev"},"diagnostic":null,"confidence":0.87,"explanation":"dev status"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "cluster.status.read",
			Input:    map[string]any{"environment": "dev"},
		},
		Notes: "中文模糊",
	},
	{
		Name:        "tool/cluster_status_prod_caps",
		Category:    CategoryTool,
		UserMessage: "PROD cluster STATUS",
		ScriptedLLM: `{"tool_name":"cluster.status.read","input":{"environment":"prod"},"diagnostic":null,"confidence":0.9,"explanation":"case-insensitive"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "cluster.status.read",
			Input:    map[string]any{"environment": "prod"},
		},
		Notes: "大小写混合",
	},
	{
		Name:        "tool/cluster_status_prod_extra_space",
		Category:    CategoryTool,
		UserMessage: "  查看   prod  集群状态  ",
		ScriptedLLM: `{"tool_name":"cluster.status.read","input":{"environment":"prod"},"diagnostic":null,"confidence":0.89,"explanation":"trim spaces"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "cluster.status.read",
			Input:    map[string]any{"environment": "prod"},
		},
		Notes: "多余空格",
	},
	{
		Name:        "tool/cluster_status_prod_full",
		Category:    CategoryTool,
		UserMessage: "查看 prod 环境集群状态详情",
		ScriptedLLM: `{"tool_name":"cluster.status.read","input":{"environment":"prod"},"diagnostic":null,"confidence":0.93,"explanation":"detailed status query"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "cluster.status.read",
			Input:    map[string]any{"environment": "prod"},
		},
		Notes: "完整中文表达",
	},

	// === kafka.consumer_lag.read (12 cases) ===
	{
		Name:        "tool/kafka_lag_orders_prod_zh",
		Category:    CategoryTool,
		UserMessage: "查 prod kafka orders consumer group 的 lag",
		ScriptedLLM: `{"tool_name":"kafka.consumer_lag.read","input":{"environment":"prod","group":"orders"},"diagnostic":null,"confidence":0.91,"explanation":"read consumer group lag"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "kafka.consumer_lag.read",
			Input:    map[string]any{"environment": "prod", "group": "orders"},
		},
		Notes: "中文 + group 名",
	},
	{
		Name:        "tool/kafka_lag_payments_prod_en",
		Category:    CategoryTool,
		UserMessage: "check kafka consumer group lag for payments in prod",
		ScriptedLLM: `{"tool_name":"kafka.consumer_lag.read","input":{"environment":"prod","group":"payments"},"diagnostic":null,"confidence":0.9,"explanation":"payments lag"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "kafka.consumer_lag.read",
			Input:    map[string]any{"environment": "prod", "group": "payments"},
		},
		Notes: "英文 + 不同 group",
	},
	{
		Name:        "tool/kafka_lag_orders_staging_zh",
		Category:    CategoryTool,
		UserMessage: "staging 环境 kafka orders group lag",
		ScriptedLLM: `{"tool_name":"kafka.consumer_lag.read","input":{"environment":"staging","group":"orders"},"diagnostic":null,"confidence":0.89,"explanation":"staging lag"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "kafka.consumer_lag.read",
			Input:    map[string]any{"environment": "staging", "group": "orders"},
		},
		Notes: "staging 环境",
	},
	{
		Name:        "tool/kafka_lag_analytics_dev",
		Category:    CategoryTool,
		UserMessage: "dev kafka analytics group 的 lag",
		ScriptedLLM: `{"tool_name":"kafka.consumer_lag.read","input":{"environment":"dev","group":"analytics"},"diagnostic":null,"confidence":0.88,"explanation":"analytics lag"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "kafka.consumer_lag.read",
			Input:    map[string]any{"environment": "dev", "group": "analytics"},
		},
		Notes: "dev 环境",
	},
	{
		Name:        "tool/kafka_lag_orders_en_short",
		Category:    CategoryTool,
		UserMessage: "prod kafka orders lag",
		ScriptedLLM: `{"tool_name":"kafka.consumer_lag.read","input":{"environment":"prod","group":"orders"},"diagnostic":null,"confidence":0.87,"explanation":"short lag query"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "kafka.consumer_lag.read",
			Input:    map[string]any{"environment": "prod", "group": "orders"},
		},
		Notes: "极简英文",
	},
	{
		Name:        "tool/kafka_lag_notifications_prod",
		Category:    CategoryTool,
		UserMessage: "查看 prod kafka notifications 消费组延迟",
		ScriptedLLM: `{"tool_name":"kafka.consumer_lag.read","input":{"environment":"prod","group":"notifications"},"diagnostic":null,"confidence":0.9,"explanation":"notifications lag"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "kafka.consumer_lag.read",
			Input:    map[string]any{"environment": "prod", "group": "notifications"},
		},
		Notes: "中文 + 延迟同义",
	},
	{
		Name:        "tool/kafka_lag_ingestion_prod",
		Category:    CategoryTool,
		UserMessage: "prod kafka ingestion group 的 lag",
		ScriptedLLM: `{"tool_name":"kafka.consumer_lag.read","input":{"environment":"prod","group":"ingestion"},"diagnostic":null,"confidence":0.89,"explanation":"ingestion lag"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "kafka.consumer_lag.read",
			Input:    map[string]any{"environment": "prod", "group": "ingestion"},
		},
		Notes: "ingestion group",
	},
	{
		Name:        "tool/kafka_lag_audit_prod",
		Category:    CategoryTool,
		UserMessage: "查 prod kafka audit group 滞后",
		ScriptedLLM: `{"tool_name":"kafka.consumer_lag.read","input":{"environment":"prod","group":"audit"},"diagnostic":null,"confidence":0.88,"explanation":"audit lag"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "kafka.consumer_lag.read",
			Input:    map[string]any{"environment": "prod", "group": "audit"},
		},
		Notes: "滞后同义",
	},
	{
		Name:        "tool/kafka_lag_metrics_staging",
		Category:    CategoryTool,
		UserMessage: "staging kafka metrics group 的 lag",
		ScriptedLLM: `{"tool_name":"kafka.consumer_lag.read","input":{"environment":"staging","group":"metrics"},"diagnostic":null,"confidence":0.87,"explanation":"metrics lag"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "kafka.consumer_lag.read",
			Input:    map[string]any{"environment": "staging", "group": "metrics"},
		},
		Notes: "staging metrics",
	},
	{
		Name:        "tool/kafka_lag_orders_extra_key",
		Category:    CategoryTool,
		UserMessage: "prod kafka orders group lag 详情",
		ScriptedLLM: `{"tool_name":"kafka.consumer_lag.read","input":{"environment":"prod","group":"orders","topic":"orders-topic"},"diagnostic":null,"confidence":0.9,"explanation":"with topic extra"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "kafka.consumer_lag.read",
			Input:    map[string]any{"environment": "prod", "group": "orders"},
		},
		Notes: "实际 input 多一个 topic key（部分匹配应忽略）",
	},
	{
		Name:        "tool/kafka_lag_orders_capitalized",
		Category:    CategoryTool,
		UserMessage: "PROD Kafka ORDERS group lag",
		ScriptedLLM: `{"tool_name":"kafka.consumer_lag.read","input":{"environment":"prod","group":"orders"},"diagnostic":null,"confidence":0.88,"explanation":"case-insensitive group"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "kafka.consumer_lag.read",
			Input:    map[string]any{"environment": "prod", "group": "orders"},
		},
		Notes: "大小写混合",
	},
	{
		Name:        "tool/kafka_lag_orders_zh_colloquial",
		Category:    CategoryTool,
		UserMessage: "看下 prod kafka orders 这组落后多少",
		ScriptedLLM: `{"tool_name":"kafka.consumer_lag.read","input":{"environment":"prod","group":"orders"},"diagnostic":null,"confidence":0.85,"explanation":"colloquial lag"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "kafka.consumer_lag.read",
			Input:    map[string]any{"environment": "prod", "group": "orders"},
		},
		Notes: "口语化",
	},

	// === minio.bucket.health.read (12 cases) ===
	{
		Name:        "tool/minio_health_orders_prod_zh",
		Category:    CategoryTool,
		UserMessage: "查 prod minio orders bucket 健康",
		ScriptedLLM: `{"tool_name":"minio.bucket.health.read","input":{"environment":"prod","bucket":"orders"},"diagnostic":null,"confidence":0.91,"explanation":"bucket health"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "minio.bucket.health.read",
			Input:    map[string]any{"environment": "prod", "bucket": "orders"},
		},
		Notes: "中文 + bucket 名",
	},
	{
		Name:        "tool/minio_health_images_prod_en",
		Category:    CategoryTool,
		UserMessage: "check minio bucket images health in prod",
		ScriptedLLM: `{"tool_name":"minio.bucket.health.read","input":{"environment":"prod","bucket":"images"},"diagnostic":null,"confidence":0.9,"explanation":"images bucket health"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "minio.bucket.health.read",
			Input:    map[string]any{"environment": "prod", "bucket": "images"},
		},
		Notes: "英文 + 不同 bucket",
	},
	{
		Name:        "tool/minio_health_logs_staging_zh",
		Category:    CategoryTool,
		UserMessage: "staging minio logs bucket 健康状态",
		ScriptedLLM: `{"tool_name":"minio.bucket.health.read","input":{"environment":"staging","bucket":"logs"},"diagnostic":null,"confidence":0.89,"explanation":"logs bucket"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "minio.bucket.health.read",
			Input:    map[string]any{"environment": "staging", "bucket": "logs"},
		},
		Notes: "staging + logs bucket",
	},
	{
		Name:        "tool/minio_health_backups_dev",
		Category:    CategoryTool,
		UserMessage: "dev minio backups bucket 健康",
		ScriptedLLM: `{"tool_name":"minio.bucket.health.read","input":{"environment":"dev","bucket":"backups"},"diagnostic":null,"confidence":0.88,"explanation":"backups bucket"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "minio.bucket.health.read",
			Input:    map[string]any{"environment": "dev", "bucket": "backups"},
		},
		Notes: "dev + backups bucket",
	},
	{
		Name:        "tool/minio_health_thumbnails_prod",
		Category:    CategoryTool,
		UserMessage: "prod minio thumbnails bucket 健康",
		ScriptedLLM: `{"tool_name":"minio.bucket.health.read","input":{"environment":"prod","bucket":"thumbnails"},"diagnostic":null,"confidence":0.87,"explanation":"thumbnails bucket"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "minio.bucket.health.read",
			Input:    map[string]any{"environment": "prod", "bucket": "thumbnails"},
		},
		Notes: "thumbnails bucket",
	},
	{
		Name:        "tool/minio_health_artifacts_prod",
		Category:    CategoryTool,
		UserMessage: "查 prod minio artifacts bucket 是否健康",
		ScriptedLLM: `{"tool_name":"minio.bucket.health.read","input":{"environment":"prod","bucket":"artifacts"},"diagnostic":null,"confidence":0.9,"explanation":"artifacts bucket"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "minio.bucket.health.read",
			Input:    map[string]any{"environment": "prod", "bucket": "artifacts"},
		},
		Notes: "artifacts bucket",
	},
	{
		Name:        "tool/minio_health_orders_en_short",
		Category:    CategoryTool,
		UserMessage: "prod minio orders bucket health",
		ScriptedLLM: `{"tool_name":"minio.bucket.health.read","input":{"environment":"prod","bucket":"orders"},"diagnostic":null,"confidence":0.89,"explanation":"short health"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "minio.bucket.health.read",
			Input:    map[string]any{"environment": "prod", "bucket": "orders"},
		},
		Notes: "极简英文",
	},
	{
		Name:        "tool/minio_health_cache_staging",
		Category:    CategoryTool,
		UserMessage: "staging minio cache bucket 状态",
		ScriptedLLM: `{"tool_name":"minio.bucket.health.read","input":{"environment":"staging","bucket":"cache"},"diagnostic":null,"confidence":0.88,"explanation":"cache bucket"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "minio.bucket.health.read",
			Input:    map[string]any{"environment": "staging", "bucket": "cache"},
		},
		Notes: "cache bucket",
	},
	{
		Name:        "tool/minio_health_temp_dev",
		Category:    CategoryTool,
		UserMessage: "dev minio temp bucket 健康吗",
		ScriptedLLM: `{"tool_name":"minio.bucket.health.read","input":{"environment":"dev","bucket":"temp"},"diagnostic":null,"confidence":0.87,"explanation":"temp bucket"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "minio.bucket.health.read",
			Input:    map[string]any{"environment": "dev", "bucket": "temp"},
		},
		Notes: "temp bucket",
	},
	{
		Name:        "tool/minio_health_media_prod",
		Category:    CategoryTool,
		UserMessage: "查 prod minio media bucket 情况",
		ScriptedLLM: `{"tool_name":"minio.bucket.health.read","input":{"environment":"prod","bucket":"media"},"diagnostic":null,"confidence":0.86,"explanation":"media bucket"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "minio.bucket.health.read",
			Input:    map[string]any{"environment": "prod", "bucket": "media"},
		},
		Notes: "media bucket",
	},
	{
		Name:        "tool/minio_health_orders_extra_key",
		Category:    CategoryTool,
		UserMessage: "prod minio orders bucket 详情健康",
		ScriptedLLM: `{"tool_name":"minio.bucket.health.read","input":{"environment":"prod","bucket":"orders","size":12345},"diagnostic":null,"confidence":0.9,"explanation":"with size extra"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "minio.bucket.health.read",
			Input:    map[string]any{"environment": "prod", "bucket": "orders"},
		},
		Notes: "实际 input 多一个 size key（部分匹配应忽略）",
	},
	{
		Name:        "tool/minio_health_orders_caps",
		Category:    CategoryTool,
		UserMessage: "PROD Minio ORDERS Bucket HEALTH",
		ScriptedLLM: `{"tool_name":"minio.bucket.health.read","input":{"environment":"prod","bucket":"orders"},"diagnostic":null,"confidence":0.88,"explanation":"case-insensitive"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "minio.bucket.health.read",
			Input:    map[string]any{"environment": "prod", "bucket": "orders"},
		},
		Notes: "大小写混合",
	},

	// === glusterfs.volume.health.read (12 cases) ===
	{
		Name:        "tool/gluster_health_data_prod_zh",
		Category:    CategoryTool,
		UserMessage: "查 prod glusterfs data volume 健康",
		ScriptedLLM: `{"tool_name":"glusterfs.volume.health.read","input":{"environment":"prod","volume":"data"},"diagnostic":null,"confidence":0.91,"explanation":"volume health"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "glusterfs.volume.health.read",
			Input:    map[string]any{"environment": "prod", "volume": "data"},
		},
		Notes: "中文 + volume 名",
	},
	{
		Name:        "tool/gluster_health_logs_prod_en",
		Category:    CategoryTool,
		UserMessage: "check glusterfs logs volume health in prod",
		ScriptedLLM: `{"tool_name":"glusterfs.volume.health.read","input":{"environment":"prod","volume":"logs"},"diagnostic":null,"confidence":0.9,"explanation":"logs volume"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "glusterfs.volume.health.read",
			Input:    map[string]any{"environment": "prod", "volume": "logs"},
		},
		Notes: "英文 + logs volume",
	},
	{
		Name:        "tool/gluster_health_backup_staging",
		Category:    CategoryTool,
		UserMessage: "staging glusterfs backup volume 健康",
		ScriptedLLM: `{"tool_name":"glusterfs.volume.health.read","input":{"environment":"staging","volume":"backup"},"diagnostic":null,"confidence":0.89,"explanation":"backup volume"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "glusterfs.volume.health.read",
			Input:    map[string]any{"environment": "staging", "volume": "backup"},
		},
		Notes: "staging + backup volume",
	},
	{
		Name:        "tool/gluster_health_media_dev",
		Category:    CategoryTool,
		UserMessage: "dev glusterfs media volume 状态",
		ScriptedLLM: `{"tool_name":"glusterfs.volume.health.read","input":{"environment":"dev","volume":"media"},"diagnostic":null,"confidence":0.88,"explanation":"media volume"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "glusterfs.volume.health.read",
			Input:    map[string]any{"environment": "dev", "volume": "media"},
		},
		Notes: "dev + media volume",
	},
	{
		Name:        "tool/gluster_health_archive_prod",
		Category:    CategoryTool,
		UserMessage: "查 prod glusterfs archive volume 健康状态",
		ScriptedLLM: `{"tool_name":"glusterfs.volume.health.read","input":{"environment":"prod","volume":"archive"},"diagnostic":null,"confidence":0.9,"explanation":"archive volume"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "glusterfs.volume.health.read",
			Input:    map[string]any{"environment": "prod", "volume": "archive"},
		},
		Notes: "archive volume",
	},
	{
		Name:        "tool/gluster_health_data_en_short",
		Category:    CategoryTool,
		UserMessage: "prod glusterfs data volume health",
		ScriptedLLM: `{"tool_name":"glusterfs.volume.health.read","input":{"environment":"prod","volume":"data"},"diagnostic":null,"confidence":0.89,"explanation":"short health"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "glusterfs.volume.health.read",
			Input:    map[string]any{"environment": "prod", "volume": "data"},
		},
		Notes: "极简英文",
	},
	{
		Name:        "tool/gluster_health_cache_prod",
		Category:    CategoryTool,
		UserMessage: "prod glusterfs cache volume 健康吗",
		ScriptedLLM: `{"tool_name":"glusterfs.volume.health.read","input":{"environment":"prod","volume":"cache"},"diagnostic":null,"confidence":0.87,"explanation":"cache volume"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "glusterfs.volume.health.read",
			Input:    map[string]any{"environment": "prod", "volume": "cache"},
		},
		Notes: "cache volume",
	},
	{
		Name:        "tool/gluster_health_temp_staging",
		Category:    CategoryTool,
		UserMessage: "staging glusterfs temp volume 情况",
		ScriptedLLM: `{"tool_name":"glusterfs.volume.health.read","input":{"environment":"staging","volume":"temp"},"diagnostic":null,"confidence":0.86,"explanation":"temp volume"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "glusterfs.volume.health.read",
			Input:    map[string]any{"environment": "staging", "volume": "temp"},
		},
		Notes: "temp volume",
	},
	{
		Name:        "tool/gluster_health_data_extra_key",
		Category:    CategoryTool,
		UserMessage: "prod glusterfs data volume 健康详情",
		ScriptedLLM: `{"tool_name":"glusterfs.volume.health.read","input":{"environment":"prod","volume":"data","replica_count":3},"diagnostic":null,"confidence":0.9,"explanation":"with replica extra"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "glusterfs.volume.health.read",
			Input:    map[string]any{"environment": "prod", "volume": "data"},
		},
		Notes: "实际 input 多一个 replica_count key（部分匹配应忽略）",
	},
	{
		Name:        "tool/gluster_health_data_caps",
		Category:    CategoryTool,
		UserMessage: "PROD GlusterFS DATA Volume HEALTH",
		ScriptedLLM: `{"tool_name":"glusterfs.volume.health.read","input":{"environment":"prod","volume":"data"},"diagnostic":null,"confidence":0.88,"explanation":"case-insensitive"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "glusterfs.volume.health.read",
			Input:    map[string]any{"environment": "prod", "volume": "data"},
		},
		Notes: "大小写混合",
	},
	{
		Name:        "tool/gluster_health_data_colloquial",
		Category:    CategoryTool,
		UserMessage: "看下 prod glusterfs data 卷啥情况",
		ScriptedLLM: `{"tool_name":"glusterfs.volume.health.read","input":{"environment":"prod","volume":"data"},"diagnostic":null,"confidence":0.85,"explanation":"colloquial"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "glusterfs.volume.health.read",
			Input:    map[string]any{"environment": "prod", "volume": "data"},
		},
		Notes: "卷同义 volume",
	},
	{
		Name:        "tool/gluster_health_shares_prod",
		Category:    CategoryTool,
		UserMessage: "查 prod glusterfs shares volume 健康",
		ScriptedLLM: `{"tool_name":"glusterfs.volume.health.read","input":{"environment":"prod","volume":"shares"},"diagnostic":null,"confidence":0.89,"explanation":"shares volume"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "glusterfs.volume.health.read",
			Input:    map[string]any{"environment": "prod", "volume": "shares"},
		},
		Notes: "shares volume",
	},

	// === fuzzy / boundary (2 cases) ===
	{
		Name:        "tool/fuzzy_zh_status",
		Category:    CategoryTool,
		UserMessage: "看下 prod 啥情况",
		ScriptedLLM: `{"tool_name":"cluster.status.read","input":{"environment":"prod"},"diagnostic":null,"confidence":0.7,"explanation":"fuzzy status"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "cluster.status.read",
			Input:    map[string]any{"environment": "prod"},
		},
		Notes: "极模糊中文表达",
	},
	{
		Name:        "tool/fuzzy_en_status",
		Category:    CategoryTool,
		UserMessage: "how is prod doing",
		ScriptedLLM: `{"tool_name":"cluster.status.read","input":{"environment":"prod"},"diagnostic":null,"confidence":0.75,"explanation":"fuzzy english"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "cluster.status.read",
			Input:    map[string]any{"environment": "prod"},
		},
		Notes: "极模糊英文表达，LLM 仍能解析为 cluster.status.read（confidence 0.75 属较明确区间，通过 0.7 阈值）",
	},

	// === alert.query (8 cases) ===
	{
		Name:        "tool/alert_query_active_prod_zh",
		Category:    CategoryTool,
		UserMessage: "当前有哪些告警",
		ScriptedLLM: `{"tool_name":"alert.query","input":{"environment":"prod"},"diagnostic":null,"confidence":0.9,"explanation":"list active alerts"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "alert.query",
			Input:    map[string]any{"environment": "prod"},
		},
		Notes: "中文 + 默认 prod",
	},
	{
		Name:        "tool/alert_query_active_staging_zh",
		Category:    CategoryTool,
		UserMessage: "staging 有哪些告警",
		ScriptedLLM: `{"tool_name":"alert.query","input":{"environment":"staging"},"diagnostic":null,"confidence":0.9,"explanation":"list staging alerts"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "alert.query",
			Input:    map[string]any{"environment": "staging"},
		},
		Notes: "中文 + 显式 staging 环境",
	},
	{
		Name:        "tool/alert_query_active_en",
		Category:    CategoryTool,
		UserMessage: "show me active alerts in prod",
		ScriptedLLM: `{"tool_name":"alert.query","input":{"environment":"prod"},"diagnostic":null,"confidence":0.88,"explanation":"show active alerts"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "alert.query",
			Input:    map[string]any{"environment": "prod"},
		},
		Notes: "英文表达",
	},
	{
		Name:        "tool/alert_query_critical_only",
		Category:    CategoryTool,
		UserMessage: "查看 prod 的 critical 告警",
		ScriptedLLM: `{"tool_name":"alert.query","input":{"environment":"prod","severity":"critical"},"diagnostic":null,"confidence":0.9,"explanation":"list critical alerts"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "alert.query",
			Input:    map[string]any{"environment": "prod", "severity": "critical"},
		},
		Notes: "按严重级别过滤",
	},
	{
		Name:        "tool/alert_query_resolved",
		Category:    CategoryTool,
		UserMessage: "prod 已恢复的告警",
		ScriptedLLM: `{"tool_name":"alert.query","input":{"environment":"prod","status":"resolved"},"diagnostic":null,"confidence":0.85,"explanation":"list resolved alerts"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "alert.query",
			Input:    map[string]any{"environment": "prod", "status": "resolved"},
		},
		Notes: "按状态过滤（resolved）",
	},
	{
		Name:        "tool/alert_query_colloquial",
		Category:    CategoryTool,
		UserMessage: "现在有啥告警",
		ScriptedLLM: `{"tool_name":"alert.query","input":{"environment":"prod"},"diagnostic":null,"confidence":0.85,"explanation":"colloquial alert query"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "alert.query",
			Input:    map[string]any{"environment": "prod"},
		},
		Notes: "口语化表达",
	},
	{
		Name:        "tool/alert_query_domain_filter",
		Category:    CategoryTool,
		UserMessage: "prod 环境 kafka 域的告警",
		ScriptedLLM: `{"tool_name":"alert.query","input":{"environment":"prod","domain":"kafka"},"diagnostic":null,"confidence":0.88,"explanation":"filter by domain"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "alert.query",
			Input:    map[string]any{"environment": "prod", "domain": "kafka"},
		},
		Notes: "按域过滤",
	},
	{
		Name:        "tool/alert_query_extra_keys_ignored",
		Category:    CategoryTool,
		UserMessage: "当前有哪些告警，顺便看下集群",
		ScriptedLLM: `{"tool_name":"alert.query","input":{"environment":"prod","cluster":"prod-cluster-01"},"diagnostic":null,"confidence":0.87,"explanation":"alert query with extra cluster key"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "alert.query",
			Input:    map[string]any{"environment": "prod"},
		},
		Notes: "LLM 返回多余键 cluster，部分匹配应忽略",
	},

	// === event.query (2 cases) ===
	{
		Name:        "tool/event_query_zh",
		Category:    CategoryTool,
		UserMessage: "查看 prod 上周的审计记录",
		ScriptedLLM: `{"tool_name":"event.query","input":{"environment":"prod","query":"查看 prod 上周的审计记录"},"diagnostic":null,"confidence":0.88,"explanation":"query audit events"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "event.query",
			Input:    map[string]any{"environment": "prod", "query": "查看 prod 上周的审计记录"},
		},
		Notes: "中文事件中心查询",
	},
	{
		Name:        "tool/event_query_en",
		Category:    CategoryTool,
		UserMessage: "show audit events in prod last week",
		ScriptedLLM: `{"tool_name":"event.query","input":{"environment":"prod","query":"show audit events in prod last week"},"diagnostic":null,"confidence":0.9,"explanation":"query audit events english"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "event.query",
			Input:    map[string]any{"environment": "prod", "query": "show audit events in prod last week"},
		},
		Notes: "英文事件中心查询",
	},

	// === task.query (2 cases) ===
	{
		Name:        "tool/task_query_zh",
		Category:    CategoryTool,
		UserMessage: "查看 prod 有哪些定时任务",
		ScriptedLLM: `{"tool_name":"task.query","input":{"environment":"prod"},"diagnostic":null,"confidence":0.88,"explanation":"query scheduled tasks"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "task.query",
			Input:    map[string]any{"environment": "prod"},
		},
		Notes: "中文任务中心查询",
	},
	{
		Name:        "tool/task_query_en",
		Category:    CategoryTool,
		UserMessage: "list scheduled tasks in prod",
		ScriptedLLM: `{"tool_name":"task.query","input":{"environment":"prod"},"diagnostic":null,"confidence":0.9,"explanation":"list scheduled tasks english"}`,
		ExpectedIntent: ExpectedIntent{
			ToolName: "task.query",
			Input:    map[string]any{"environment": "prod"},
		},
		Notes: "英文任务中心查询",
	},
}
