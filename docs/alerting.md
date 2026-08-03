# Alert Ingestion

告警接入层：外部监控系统推送告警 → 归一化 → 落库（`copilot_alerts`）→ 可查询（`alert.query` 工具 / `/v1/alerts` 查询）。

两个 webhook 端点都走**同一条**已建好的管线（HMAC 签名校验 → 归一化 → 按 `source + external_id` 去重 upsert → 审计），只是接收的载荷格式不同：

| 端点 | 接收格式 | 用途 |
|------|---------|------|
| `POST /v1/alerts/webhook` | 自定义 `WebhookPayload`：`external_id` / `source` / `title` / `severity` / `status` | 通用告警源直接推归一化后的告警 |
| `POST /v1/alerts/alertmanager` | Prometheus **Alertmanager 原生 webhook v2** payload | 一条推送含多条 `alerts[]`，逐条转换后进入管线 |

两者都由 `X-Webhook-Signature: HMAC-SHA256(body, secret)` 十六进制摘要门控（`COPILOT_ALERT_WEBHOOK_SECRET`，常量时间比较）。

## Alertmanager 原生 webhook

Alertmanager 的 webhook 载荷结构与我们自定义 schema 不同（`validate` 需要
`external_id/source/title/severity` 和 `status ∈ {firing,resolved}`），所以原生载荷需要先映射：

```json
{
  "version": "4",
  "groupKey": "{}/{namespace=prod}:{alertname=HighCPU}",
  "receiver": "webhook",
  "status": "firing",
  "alerts": [
    {
      "status": "firing",
      "labels": {"alertname":"HighCPU","namespace":"prod","severity":"critical","environment":"prod"},
      "annotations": {"summary":"CPU 超过 90%", "description":"节点 ns1 CPU 持续过高"},
      "startsAt": "2026-08-02T10:00:00Z",
      "endsAt": "",
      "fingerprint": "fp-abc-1"
    }
  ],
  "externalURL": "http://alertmanager:9093"
}
```

请求时带 `X-Webhook-Signature: <HMAC-SHA256 hex>`。

**映射规则**（`internal/alert.MapAlertmanager`），每条 `alerts[]` → 一条 `WebhookPayload`：

| 原生字段 | → 归一化字段 |
|---------|-------------|
| `fingerprint` | `external_id`（前缀 `fp:`，跨状态去重的稳定身份；缺失时退回按 labels 排序拼接的复合串） |
| `labels.alertname` | `title`（优先取 `annotations.summary`） |
| `annotations.description` / `message` | `description` |
| `labels.severity` | `severity`（未知值在归一化时降级为 `warning`） |
| `status` | `status`（`firing` / `resolved`；空值默认 firing） |
| `labels.environment` | `environment`（空则默认 `prod`） |
| `labels.domain` / `namespace` | `domain` |
| `startsAt` / `endsAt` | `fired_at` / `resolved_at`（RFC3339，解析失败忽略） |
| — | `source = "alertmanager"`，`labels` 与 `annotations` 合并（annotations 键加 `am_` 前缀） |

**响应**：单条推送可能带多条告警；每条成功落库计入 `acknowledged`，失败的单条不阻断整批：

```json
{
  "acknowledged": 2,
  "alerts": [
    {"id": "uuid-1", "status": "firing", "created": true},
    {"id": "uuid-2", "status": "firing", "created": true}
  ]
}
```

## 配置

```env
COPILOT_ALERT_WEBHOOK_SECRET=0123456789abcdef0123456789abcdef
```

`internal/alert` 的归一化模型与 `internal/httpapi` 的签名校验在
`internal/alert/alertmanager.go`、`internal/httpapi/alerts.go`。

---

**维护者**: AIOps Platform Team
**更新时间**: 2026-08-03
