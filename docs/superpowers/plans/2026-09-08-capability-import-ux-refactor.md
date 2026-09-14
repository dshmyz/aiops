# 能力接入 UX 重构 —— 详细改动方案

- 状态：**待评审**
- 日期：2026-09-08
- 范围：能力控制台"接入一个后端能力 → 可试调"的体验重构（前后端）
- 原则：复用已有积木，收敛成一条主路径，不新建平行概念；**设计红线——任何新概念都必须是可省略的：默认走单一主路径，其余一律收进"展开/高级"，不占主视**。

---

## 1. 背景与目标

根据已有的痛点调研（YAML/JSONPath 繁琐、流程太长、概念门槛高、AI 推断/试调不稳），本次重构的目标是把当前**碎片化、多入口**的接入体验，收敛成一条**"零门槛主路径"**：

```
①连源 → ②自动生成 → ③试调验收 → ④发布
```

其中 `②自动生成` 是本方案的核心新能力：**给一个可访问的后端 URL，自动探测真实返回结构 → 自动推断 output 映射（JSONPath/severity/summary）→ 生成可试调的能力草稿**，让用户不再手写 YAML / JSONPath。

## 2. 现状盘点（复用基线）

### 2.1 后端已有积木

| 能力 | 位置 | 说明 |
|---|---|---|
| Swagger/OpenAPI 导入 | `internal/capabilities/importer.go` `ImportOpenAPI`(363) | 仅接受 OpenAPI/Swagger 文档，从 schema 推断 input/output |
| 字段命名推断 | `importer.go` `inferCapability`(591) 等 | domain/resource_type/operation/risk/input_schema/name 等 |
| 能力试调+推断 | `internal/capabilities/manage.go` `ProbeAndInfer`(339) | 对**已构建的能力**做真实调用 + LLM 推断输出映射，返回 `ProbeInferResponse`(含 `Probe`/`RawBody`/`Inferred`/`InferredBy`) |
| 规则级输出提取 | `internal/capabilities/smart_extract.go` | `smartExtractFields`(30) / `inferSeverityFromData`(234) / `inferSummaryFromData`(302) |
| LLM 输出推断 | `internal/capabilities/llm_enricher.go` `InferOutputFromSample`(299) | 从真实样例推断 `OutputSpec`（fields/severity_path/status_mapping/summary_template） |
| 响应归一化 | `internal/capabilities/http_adapter.go` `Execute`(163) | JSON/数组/文本归一化成 `data`，含非 JSON→`data.content`、顶层数组→`data.items`、递归脱敏、severity/summary 推断 |
| 现有 API | `internal/httpapi/router.go` `serveCapabilities`(620-885) | encode 已有 15+ 端点：import/openapi-url/*、quick-publish、test、probe-infer、enrich-*、drafts CRUD、publish/unpublish |

### 2.2 前端已有积木

| 积木 | 位置 | 说明 |
|---|---|---|
| 四阶段编排 | `src/views/ManagementView.vue`(68-71) | `managementPhase` 切换 source/candidates/review/ai |
| 导航 | `src/components/management/WorkflowRail.vue` | 现文案：接入API→选择能力→评审发布→AI试问 |
| Swagger 源输入 | `src/components/management/SwaggerSourceStrip.vue` | URL + base_url + preview 事件，成熟可照抄 |
| 快速发布 | `src/components/management/QuickPublishForm.vue` + `useQuickPublishInfer.ts` | 无 Swagger 时手动填 base_url/path/description |
| 手动创建 | `src/components/manual/ManualCreatePanel.vue` (`ManualFormBuilder`/`ManualJsonPaster`) | 表单 / JSON 粘贴 |
| 试调面板 | `src/components/management/ProbePanel.vue` | 已展示 `raw_body` + `inferred`(summary/severity_path/fields) + `inferred_by` |
| 测试预览 | `src/components/management/TestPreviewPanel.vue` | requestPreview/responsePreview/preview(normalized) 但**非三视图对比** |
| 编排 composable | `src/composables/useCapabilities.ts` | 汇聚 importWizard + editor + publish |
| 导入向导 | `src/composables/useCapabilityImport.ts` / `importWizard.ts` / `importBatch.ts` | Swagger 预览、候选选择、批量试调、提交 |

### 2.3 关键缺口（本次要补的）

- **后端**：没有"裸 REST URL → 真实响应 → 自动生成能力"的完整入口。`probe-infer` 需要预构建 Capability；`importer.go` 只认 OpenAPI/Swagger，无法处理裸 JSON/XML 接口。
- **后端细节**：`Execute`/`Validate` 要求能力为 `published`，未发布能力无法直接走真值试调（需绕过）。
- **后端细节**：`severity_path`/`status_mapping`/`fields`（针对真实返回体）在导入期不自动推断，只靠运行期 `smart_extract`。
- **前端**：没有"裸 URL 自动出能力"入口；没有"后端原始响应 ‖ 归一化 data ‖ LLM 视角"三段对比视图；快速/高级没有显式模式概念；`ReviewStage` 面板扁平堆积。

---

## 3. 后端改动

### 3.1 新增：`Manager.ProbeAndGenerate`（核心方法）

位置：`internal/capabilities/manage.go` 新增方法，或在 `importer.go` 新增独立函数（建议放 `manage.go`，因为它重试用 `ProbeAndInfer` 骨架与 HTTPAdapter）。

职责：输入一个"裸"后端接入描述 → 内部执行真实请求 → 归一化 → 生成能力草稿 + 三段真值结果。

```go
// ProbeGenerateRequest —— 本次唯一的"新结构"（大道至简，见 3.5）。
// 后端请求体复用已有 Capability.Backend；返回完全复用现有 ProbeInferResponse：
//   RawBody=原始响应、Probe.Data=归一化 data、Probe.Severity=severity、Inferred=LLM 视角。
type ProbeGenerateRequest struct {
    Name       string         `json:"name,omitempty"`         // 可选，自动推断
    Backend    Backend        `json:"backend"`                // 复用 Capability.Backend
    Input      map[string]any `json:"input,omitempty"`        // 仅 GET/只读真实探测用
    SampleBody string         `json:"sample_body,omitempty"`  // 写能力/后端离线时离线推断映射
}

// 方法签名：ProbeAndGenerate(ctx, req) (Capability, ProbeInferResponse, error)
// capability = 生成的草稿（未保存）；probe = 三段真值素材。
```

实现要点（复用链）：
1. 复用 `HTTPAdapter.Execute` → 真实调用 + 归一化 `data`。**不新增执行方法**：在 manage 层用 `req` 构造一个**临时的只读 published 能力副本（仅内存、不入库）**传入现有 `Execute`，借道其归一化与脱敏；写/离线场景不 invoke 执行，走第 2-3 步推断。复用 `smart_extract.go` 的 `inferSeverityFromData`/`inferSummaryFromData` 作为**规则兜底**（无 LLM 也能出结果）。
2. `smartExtractFields(data)` → 规则级推断 `output.fields` + 顺带可能的 `severity_path`。
3. 若已配 LLM：`InferOutputFromSample(raw, ...)` → 增强 `severity_path`/`status_mapping`/`summary_template`，`InferredBy="llm_sample"`；否则 `"rules"`。**这是把现在 `probe-infer` 的 LLM 推断能力复用到"生成"路径。**
4. `importer.go` 的 `inferDomain/inferResourceType/inferOperation/inferRisk/inferDescription` → 填能力命名与治理字段（安全默认值：运行期仍按 fail-closed，草稿期可视 `operation=read` 给 `risk=low`）。
5. 结果只用于预览/生成草稿，**不直接发布**（发布仍走现有 `publish` 端点 + 审批/清单）。

### 3.2 新增路由

位置：`internal/httpapi/router.go` `serveCapabilities`。

| Method | Path | Handler | 作用 |
|---|---|---|---|
| POST | `/v1/capabilities/probe-generate` | 新增 | 裸接入点 → 三段真值 + 生成能力草稿（不保存） |

请求体 = `ProbeGenerateRequest`；响应 = `{ capability: <草稿>, probe: <现有 ProbeInferResponse> }`。鉴权：与 `test`/`probe-infer` 同级（需要能力访问权限，建议 viewer 及以上；这是只读探测，不产生写）。

### 3.3 数据返回约定如何被旧系统复用

`output.OutputSpec` 的既有约定直接承载 `③试调验收` 的三段对比：
- 后端原始响应 → `RawBody`（脱敏截断）
- 归一化结果 → 即复用结构里的 `Probe.Data`（含 `data.content`/`data.items`，`status_mapping` 驱动的 severity），**无需单独字段**
- LLM 视角 → `Inferred` 的 `summary_template`/`severity_path`/`fields`

不做任何新归一化逻辑，三视图取的是同一份 `Execute` 的产物，保证"映射对不对"在三视图上一致可见。

### 3.4 已知限制（如实标注）

| 限制 | 说明 | 缓解 |
|---|---|---|
| `severity_path`/`status_mapping` 规则级可能推不准 | 仅 openAPI schema 能推；裸响应靠 `smart_extract` 启发式 + LLM 增强 | 提供三段对比 + 手动微调；`InferredBy` 标注来源，诚实降级 |
| 写能力探测有风险 | 不能对写能力裸发真实写请求 | `probe-generate` 只对 `GET`/只读路径做真实调用；写能力由用户提供 `sample_body` 离线推断映射，生成草稿后走审批 |
| 大响应体 | 归一化/脱敏后截断 | 沿用 `maxNonJSONContentBytes` 截断 + `truncated` 标志 |
| 依赖后端在线 | 后端不在线探测失败 | 前端允许粘贴"示例响应体"离线预览（见 4.3），后端 `raw_body` 可作为输入 |

### 3.5 大道至简取舍（复用优先，新建最少）

| 初稿曾计划 | 至简后 | 理由 |
|---|---|---|
| `ProbeRequest` + `ProbeBackend` 两个新类型 | 仅新增 `ProbeGenerateRequest`，后端复用已有 `Capability.Backend` | 请求端只该表达"目标后端"，它就是 Backend |
| `ProbeGenerateResult` 新返回结构 | 整个删掉，返回复用现有 `ProbeInferResponse` + `capability` 草稿 | 三段真值素材（RawBody/Probe.Data/Inferred/Probe.Severity）现有结构已齐 |
| 新方法 `ExecuteProbe` | 不新增方法：构造临时只读 published 能力副本借道现有 `Execute` | 只借归一化/脱敏，不重写执行 |
| 前端 `RestUrlEntry` + "来源①②③" 三选一 | 收敛为单一"后端接口地址"入口（`UrlEntry`） | 少概念，见 4.1 |

**至简红线**：不因新功能发明平行结构；能借道已有模型/方法的，一律复用。新增面收敛为——后端 1 个请求结构 + 1 个方法 + 1 个路由；前端 2 个组件 + 1 个 composable 方法。

---

## 4. 前端改动

### 4.1 接入入口 —— 一个"后端接口地址"，自动识别（少概念）

不设 "①Swagger ②裸URL ③高级" 三种并列选择，收敛为**单一主输入**：

```
后端接口地址：[______________________]  [接入]
```

地址自动识别：
- 指向 Swagger/OpenAPI 文档 → 复用现有 `SwaggerSourceStrip` 批量导入。
- 指向单个接口 → 走 `probe-generate` 单接口接入（`UrlEntry`：method 默认 GET + 可选示例 body）。
- 手动表单/JSON 粘贴（现有 `ManualCreatePanel`）收敛为可折叠"高级 JSON"，不占主视。

单一心智：**填一个地址 → 看它能不能变成能力**。

### 4.2 新增：裸 URL 接入交互

- 交互：输入 `URL + method(默认GET) + 可选示例入参` → 点"探测" → 调 `/v1/capabilities/probe-generate` → 进入"生成预览" → 展示三段对比 + 可改的 output 映射 → 点"生成能力草稿" → 写回 `useCapabilities.editor` → 切到 `review`。
- composable：`useCapabilityImport.ts` 新增 `probeGenerate()`；`useCapabilities.ts` 暴露 `probeGenerateResult`。

### 4.3 新增组件：`ThreeWayCompare.vue`（三段真值对比）

`src/components/management/ThreeWayCompare.vue`，三列并排：**后端原始响应 ‖ 归一化 data ‖ LLM 视角**（`summary_template` + `severity_path` + `fields` 命中高亮）。`InferredBy` 标签（rules/llm_sample）、`severity`、`warnings` 随头部展示。

可复用地：作为 `UrlEntry` 接入结果区；也可**嵌入/替代 `ProbePanel` 的 raw+inferred 区**（两者消费的都是现有 `ProbeInferResponse`，`Probe.Data`=归一化，前端不必重造）。新增"归一化 data"展示列（现有 `ProbePanel` 没展示 `Probe.Data`）。**默认折叠**，映射出错时才展开排查。

### 4.4 流程收敛（标题 + 折叠）

- `WorkflowRail.vue`：节点文案改为 `①连源 ②自动生成 ③试调验收 ④发布`，状态机/`managementPhase` 不变（仍是 source/candidates/review/ai）。
- `SourceStage`/`CandidatesStage`/`ReviewStage` 标题对齐四步语义（主要是文案）。
- `ReviewStage.vue`：把 `PublishChecklistPanel` 与 `ProbePanel` 从扁平排布收进 `<details>` 折叠面板（保留组件与逻辑不变），主视区留给 `CapabilityEditorPanel` + `TestPreviewPanel`。

### 4.5 各类可选项 —— 默认安全值，需要时才展开

- 接入即给安全默认（`risk=low`/只读/viewer+、`requires_approval` 随操作定），用户无需先懂治理概念。
- 能力编辑（`CapabilityEditorPanel`）、发布检查清单（`PublishChecklistPanel`）、手动 JSON（`ManualCreatePanel`）统一收进可折叠"高级"区，不进入主路径。
- 三段真值对比作为"接入结果详情"的可展开视图，默认折叠。

---

## 5. 分阶段落地

| 阶段 | 内容 | 交付 | 复用/新增 |
|---|---|---|---|
| **P0（核心）** | 后端 `ProbeAndGenerate` + `/probe-generate`；读操作真实探测、写操作 `sample_body` 离线推断 | 一条"URL→三段真值→草稿"闭环 | 复用 `Execute`/`ProbeInferResponse`/`smart_extract`/`InferOutputFromSample`；仅新增 1 请求结构 |
| **P1（前端主路径）** | `UrlEntry` 接入入口 + `ThreeWayCompare` + 生成草稿写回 review | ①连源②自动生成③试调验收④发布 全走通 | 复用 `SwaggerSourceStrip` 结构/`useCapabilityImport`；新增 2 组件 |
| **P2（收敛与打磨）** | `WorkflowRail` 文案、`ReviewStage` 折叠、高级区收拢、离线示例响应预览 | 视觉收敛、概念最少 | 纯前端，多改文案/布局 |

## 6. 验收标准

- 后端有 `/v1/capabilities/probe-generate` 的单元测试：**给的裸 JSON 接口能自动生成含 `output.fields` 的能力草稿**；无 LLM 时 `InferredBy="rules"`；写能力不裸发写请求。
- 前端无登录障碍下，**输入一个可达读 URL → 三步内看到三段真值并生成可试调草稿**，且不手写任何 YAML。
- 现有 Swagger 导入、快速发布、手动创建、发布审批链路**行为不变**（回归）。
- `make all-checks` 全绿。

## 7. 待评审确认

1. ✅ 已确认：`ProbeAndGenerate` 放 `manage.go`（与 `ProbeAndInfer` 同族共享复用；`importer.go` 保持只管静态文档推断）。
2. ✅ 已确认：探测只对读操作发真实请求；写操作不裸发，由用户提供 `sample_body` 离线推断映射，生成草稿后走审批（"读取真实 + 写离线性"）。
3. ✅ 全上：P0+P1+P2 一次交付，不中途停留。
4. ✅ 已确认：入口收敛为单一"后端接口地址"自动识别，不设三源并列；新建组件命名 `UrlEntry`；三段对比作为展开详情。全链路以"少概念、默认安全值"为红线。