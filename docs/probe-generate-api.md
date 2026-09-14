# probe-generate 接入契约

把一个后端接口描述提交上去，服务端真实探测（读）或离线推断（写），返回一段三段真值对比和一份可直接保存为草稿的能力。本页是该终点的请求 / 响应 / 错误契约。

接口：`POST /v1/capabilities/probe-generate`

## 终点语义

这个终点把"一段接口描述"转成"一段三段真值 + 一份能力草稿"，全程不落库——是否需要保存由调用方决定。

它复用了 `ProbeAndInfer` 与 `HTTPAdapter.Execute`，读接口用一次真实的只读请求拿到响应样本，写接口不会发出真实写请求，只用你提供的 `sample_body` 离线推断输出映射。能力命名、领域、风险等级由 `AutoInferQuickPublish` 推断，返回的草稿统一落在 `discovered` 状态。

> 边界：`probe-generate` 是纯读取式的"探测 + 推断"，不写持久化存储。真正保存到草稿库走另一条路径（`POST /v1/capabilities/…` 保存族），本页只描述探测这一站。

## 请求体

请求体为一个 `ProbeGenerateRequest` 对象，`backend` 为唯一必填段，其余字段按需补充。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `backend.adapter` | string | 选填 | 适配器名，无视（当前仅 `http`）。 |
| `backend.method` | string | 选填 | HTTP 方法，空值默认 `GET`。合法值：`GET / POST / PUT / PATCH / DELETE`，仅在 `GET` 时真实探测。 |
| `backend.path` | string | 必填 | 接口路径，可含 `{var}` 路径变量（自动进入 `input_schema`）。 |
| `backend.base_url` | string | 必填 | 后端基地址，必须是绝对 `http(s)` URL。 |
| `name` | string | 选填 | 能力名。缺省由 `domain.resource_type.operation` 推断，含路径变量时追加 `_by_var1_and_var2`。 |
| `description` | string | 选填 | 接口说明，参与 domain / resource_type 推断。 |
| `input` | object | 选填 | 读接口的探测实参（键为路径变量名）。 |
| `sample_body` | string | 选填 | 写接口离线推断映射的示例响应体（JSON 字符串）。`GET` 时忽略。 |

### 校验规则

服务端在推断前做最小校验，任何一条不满足都返回 `400`：方法必须是上述五种之一（或省略）、`path` 非空、`base_url` 是绝对 `http(s)` URL。

### 读接口示例（GET，真实探测）

```bash
curl -X POST http://127.0.0.1:8080/v1/capabilities/probe-generate \
  -H 'Content-Type: application/json' \
  -d '{
    "backend": {
      "method": "GET",
      "path": "/api/minio/m1/buckets/archive/capacity",
      "base_url": "http://127.0.0.1:19090"
    },
    "description": "查看 MinIO 桶容量"
  }'
```

### 写接口示例（POST，离线推断）

```bash
curl -X POST http://127.0.0.1:8080/v1/capabilities/probe-generate \
  -H 'Content-Type: application/json' \
  -d '{
    "backend": {
      "method": "POST",
      "path": "/api/kafka/m1/topics",
      "base_url": "http://127.0.0.1:19090"
    },
    "sample_body": "{\"name\":\"orders\",\"status\":\"ok\"}",
    "description": "创建 Kafka topic"
  }'
```

> 请求体经 `MaxBytesReader` 限流，上限 10 KiB；超限返回 `400 invalid JSON input`。

## 响应体

成功返回 `200`，响应体为 `{ capability, probe }` 两个对象。这正好对应"三段真值对比"的三块内容。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `capability` | Capability | 推断改写后的能力草稿（三段中的「LLM 视角映射」产物），含推断的 name / domain / operation / risk / input_schema / output。 |
| `probe.probe` | NormalizedResult（读） | 真实探测的归一化结果（`data`、`summary`、`severity`）。仅读接口有；初始化失败或缺 `sample_body` 时为 `null`。对应三段「归一化 data」。 |
| `probe.raw_body` | string | 脱敏截断后的原始响应体快照，对应三段「后端原始响应」。读接口从真实响应来；写接口离线推断时可能为空。 |
| `probe.inferred` | OutputSpec | 输出映射（`kind`、`severity_path`、`status_mapping`、`summary_template`、`fields`）。 |
| `probe.inferred_by` | string | 映射来源：`llm_sample`（真实样本 + LLM）或 `rules`（规则回退，可能不准）。 |
| `probe.warnings` | string[] | 警告列表：后端不可达、LLM 推断失败回退规则、`sample_body` 缺失或非法等。 |

### 读接口响应（GET）

```json
{
  "capability": {
    "name": "minio.bucket.capacity.read",
    "status": "discovered",
    "domain": "minio",
    "operation": "read",
    "risk": "low",
    "backend": { "adapter": "http", "method": "GET",
      "path": "/api/minio/m1/buckets/archive/capacity",
      "timeout_ms": 3000, "base_url": "http://127.0.0.1:19090" },
    "input_schema": {},
    "output": {
      "kind": "observation",
      "severity_path": "",
      "summary_template": "查询bucket完成",
      "fields": { "data.usage_pct": "$.data.usage_pct" }
    }
  },
  "probe": {
    "probe": {
      "kind": "observation",
      "resource": { "domain": "minio", "type": "bucket", "name": "" },
      "severity": "info",
      "summary": "usage 88%",
      "data": { "data.usage_pct": "88" }
    },
    "raw_body": "{\"data\":{\"usage_pct\":88}}",
    "inferred": {
      "kind": "observation",
      "severity_path": "$.data.severity",
      "status_mapping": {},
      "summary_template": "{{data.usage_pct}}% 已用",
      "fields": { "usage_pct": "$.data.usage_pct" }
    },
    "inferred_by": "rules",
    "warnings": []
  }
}
```

> 字段展平：归一化把嵌套 JSON 展平成 `data.usage_pct` 这样的取值路径键，值是字符串化的标量。判断映射时以 `probe.data` 的展平键为准，而不是原始嵌套结构。

## 读接口与写接口的差异

同一份契约，行为按 HTTP 方法分叉。两种路径都返回三段结构，但「真实探测」与「离线推断」的来源不同、保证程度不同。

### 读接口（GET）

走 `ProbeAndInfer`：用 `input` 实调一次后端，拿真实响应。

- `probe.probe` 有真实归一化结果
- `raw_body` 来自真实响应
- 映射按真实样本推断并校验命中
- 后端不可达时 `probe.probe` 为 `null`，仅告警不失败

### 写接口（POST / PUT / PATCH / DELETE）

绝不发送真实写请求，只用 `sample_body` 离线推断映射。

- `sample_body` 缺失 → 告警，映射回退为能力默认 output
- `sample_body` 非合法 JSON → 告警，映射回退
- 合法时用规则/LLM 推断字段与 severity，填成伪 `NormalizedResult` 供三段对比
- 风险默认 `medium`，`DELETE` 为 `high`

> 写安全红线：`probe-generate` 对任何写方法都不执行真实请求。真实写入只发生在能力被保存、批准之后由执行链路负责。离线推断的映射不一定准，`inferred_by: "rules"` 时应人工核对 `fields` 路径。

## 能力字段如何推断

带 `name` 时原样保留，其余字段由 `AutoInferQuickPublish` 从 `base_url + path + description` 联合推断。

| 能力字段 | 推断方式 |
| --- | --- |
| `name` | 缺省 = `domain.resource_type.operation`；path 含变量时加 `_by_{a}_and_{b}`。 |
| `operation` | `GET` → `read`；`POST/PUT/PATCH/DELETE` → `create / update / patch / delete`（按方法映射）。 |
| `risk` | 读 `low`；写默认 `medium`；`DELETE` 为 `high`。 |
| `input_schema` | 每个路径变量生成一个 `{type: string, required: true}` 字段。 |
| `output.kind` | 读 `observation`；写 `action_result`。 |
| `summary_template` | 缺省读「查询{resource_type}完成」、写「{operation}{resource_type}完成」。`inferred` 会用真实样本覆盖更具体模板。 |

## 错误响应

错误统一为 `{"error": "…"}` 形状，无错误码字段，靠 HTTP 状态码区分。探测本身的失败多为「告警而非报错」——业务不可达一般不打断主流程。

| HTTP | 场景 | error 示例 |
| --- | --- | --- |
| `400` | 请求体非合法 JSON | `invalid JSON input` |
| `400` | 方法不受支持 / path 为空 / base_url 非绝对 http(s) | `probe-generate requires a supported method, an absolute base_url, and a non-empty path` |
| `400` | 其它一般性错误 | 具体信息（endpoint 后端不可达等） |
| `503` | capability 根目录未配置 | `capability root is not configured` |

```json
{ "error": "capability root is not configured" }
```

> 阈值：响应整体受 `maxCapabilityResponseBytes`（1 MiB）上限保护，超限返回 `502 response exceeded size limit`。字段推理时 `probe.data` 会跳过 `_count / _sample / _overview` 之类聚合键，最多取 10 个，避免草稿响应过大。

## 契约出处

契约核对：`internal/capabilities/manage.go`（ProbeAndGenerate / AutoInferQuickPublish / inferWriteOutputFromSample）、`internal/httpapi/router.go`（handler 与 writeError）。字段与示例一一对应，可直接用于对接与测试。