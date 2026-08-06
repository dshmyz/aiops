# 代码评审记录 — 前端时间格式/样式统一改动

- **日期**：2026-08-06
- **评审范围**：工作区未提交改动（时间格式统一 + 巡检报告样式/卡片间距修复）
- **涉及文件**：
  - `apps/capability-console/src/conversationFormat.ts`（新增 `formatCompactDateTime`）
  - `apps/capability-console/src/components/AuditLogView.vue`
  - `apps/capability-console/src/components/AuditEventDetail.vue`
  - `apps/capability-console/src/views/ExecutionsView.vue`
  - `apps/capability-console/src/views/InspectionReportsView.vue`
  - `apps/capability-console/src/styles.css`
  - `apps/capability-console/src/conversationFormat.test.ts`
- **评审方式**：多角度人工/子代理交叉扫描（逐行 diff、跨文件数据流追踪、Go 序列化语义核对、语言陷阱、复用/简化/海拔审查）

---

## 结论摘要

未发现会导致崩溃或数据丢失的高危问题；测试套件（`conversationFormat`、`AuditLogView`、`AuditEventDetail`、`ExecutionsView`）均未断言时间渲染文本，故本改动不破坏既有测试。存在 **1 个确定性逻辑缺陷**（`latestReport` 字符串比较误序）、**1 个健壮性缺口**（`formatAbsoluteTime` 无非法输入兜底），以及若干复用/注释类改进项。

**处置记录（2026-08-06 当日修复）**：
- ✅ #1 已修复（取已排序数组首元素）
- ✅ #2 已修复（`formatAbsoluteTime` 加 NaN 兜底 + 测试）
- ✅ #3 已修复（`fmtTs` 复用 `formatCompactDateTime`）
- ⚪ #4 保留（复核确认覆盖为结构性必要，见下）
- ✅ #5 已修复（`fmtWindow` 注释改为与实际行为一致）

详见各条与文末「修复明细」。

---

## 评审发现（按严重度排序）

### 1. [高] `latestReport` 用字典序字符串比较 `generated_at`，RF C3339Nano 时间戳会误序

- **文件**：`apps/capability-console/src/views/InspectionReportsView.vue:57`
- **问题**：`if (!latest || r.generated_at > latest.generated_at) latest = r;` 对时间戳做逐字符字典序比较，但这只在所有时间戳长度/格式完全一致时才正确。
- **触发场景**：Go 的 `time.Time` JSON 序列化走 RFC3339Nano——小数秒为零时**整个省略**，否则**去掉尾零**。同一秒内生成两份报告时：
  - `r_a.generated_at = "2026-08-01T00:30:00Z"`（小数秒为零，被省略）
  - `r_b.generated_at = "2026-08-01T00:30:00.5Z"`（真实时间更晚）
  
  逐字符比较：`'Z'`(0x5A) > `'.'`(0x2E)，于是 `"…00Z" > "…00.5Z"` 为真，循环会把**更早的** `r_a` 当作 `latestReport`。顶部总览健康标签、健康条、以及「最新…N/M 成功」都描述了错误的报告。
- **额外说明**：后端接口已按 `generated_at DESC` 排序返回（`reporter.go` 统一用 `r.now()`，格式一致），前端此处的"手动取最新"既脆弱又冗余——直接用 `reports.value[0]` 即可正确且更简单。
- **建议**：改用 `Date.parse(r.generated_at) > Date.parse(latest.generated_at)` 比较时间戳数值，或直接用已排序数组首元素。

### 2. [中] `formatAbsoluteTime` 无非法输入兜底，详情面板可能渲染 `NaN年NaN月…`

- **文件**：`apps/capability-console/src/components/AuditEventDetail.vue:70`；`apps/capability-console/src/conversationFormat.ts:51`
- **问题**：本次新增的 `formatCompactDateTime` 有 `Number.isNaN(d.getTime())` 兜底（坏输入原样返回），但同文件里被新接入的 `formatAbsoluteTime` **没有**兜底——对 `Invalid Date` 调用 `getFullYear()/getMonth()/getDay()` 会全返回 NaN，若传入空/非法字符串会显示 `NaN年NaN月NaN日 … NaN:NaN:NaN`，且无回退，取代了之前直接显示原始字符串的行为。
- **触发场景**：当前后端审计记录 `CreatedAt` 有默认值，执行记录 `started_at/completed_at` 为可选字段（`ExecutionsView.vue:193-194` 已用三元 `? : '-'` 防护，因此只有 `AuditEventDetail.vue:70` 这一处为未防护调用点）。属「当前不可触发」的潜在回归，但作为同类改动应保持一致的健壮性。
- **建议**：给 `formatAbsoluteTime` 加与 `formatCompactDateTime` 相同的 `Number.isNaN` 兜底（返回原值或 `-`），或在调用点防护。

### 3. [低] `fmtTs` 重复实现共享时间格式逻辑

- **文件**：`apps/capability-console/src/views/InspectionReportsView.vue:94-100`
- **问题**：同一改动里 `conversationFormat.ts` 集中了"本地时间 + `padStart` + 非法兜底"模式（`formatCompactDateTime`），但视图内 `fmtTs` 又重新实现了一份（`new Date` + `Number.isNaN` + `pad` + `getMonth/getDate/getHours/getMinutes`），且格式略不同（无年份/无秒）。同一数据源出现两套发散的时间格式实现。
- **建议**：`fmtTs` 改复用共享助手（如 `formatCompactDateTime(ts, false)` 再裁掉 `MM-DD` 前的年份段），保持格式一致、单一实现。

### 4. [低] `.topbar` 内边距适配补丁在四个视图里重复复制（保留，已确认必要）

- **文件**：`apps/capability-console/src/styles.css:440`（全局 `.topbar`）及各视图 scoped 覆盖
- **问题**：同一段 `X-entry .topbar { padding: var(--space-5) 0 var(--space-4) }` 覆盖被复制进 `ExecutionsView`、`IncidentView`、`MarketplaceView`、`InspectionReportsView` 四个文件。
- **评审结论（更新）**：经复核，这四处覆盖是**结构性必要**的，并非海拔/层级错误。这四类视图的入口容器均为 flex-column 且自带 `padding: 0 var(--space-6) …`（如 `ExecutionsView .executions-entry`），因此内部 `.topbar` 必须清零自身的左右内边距，否则水平间距会翻倍成 `--space-12`。而其余视图（`PlansView`、`AuditView`、`ManagementView`、`ScheduledTasksView`、`McpServersView`）的入口不提供水平内边距，依赖全局 `.topbar` 的 `var(--space-6)` 水平间距。**若按初稿建议"收敛到全局 `.topbar` 并删除四处覆盖"，会让其余五个去掉入口包裹的视图顶栏左对齐塌陷**，故不采纳该修法。
- **保留原因**：四处覆盖保持现状（正确）。潜在的进一步去重（给出四个入口共享一个类、仅定义一次）需改动四处模板，收益有限且超出本次评审修复范围，暂不处理。

### 5. [低] `fmtWindow` 文档注释与实现不符

- **文件**：`apps/capability-console/src/views/InspectionReportsView.vue:102-105`
- **问题**：注释声称"同月只保留一次月份，去掉重复信息"，但实现就是 `fmtTs(start) → fmtTs(end)` 直接拼接，**没有**任何去重逻辑（跨月/跨日窗口会渲染 `08-05 08:00 → 08-06 07:59`，月份都会出现）。注释描述的行为未实现，会误导后续维护者。
- **建议**：要么实现注释所述的月份去重，要么把注释改成与实际拼接行为一致。

---

## 已检查并放过的候选项

- **`successRate`**：`total_tasks <= 0` 时返回 0，无除零。✅
- **`filterMode` 模板绑定**：ref 自动解包正确。✅
- **`selectedReport` 模板使用点**：均由 `v-if="!selectedReport"` / `v-else` 链防护，无空引用。✅
- **`fmtTs`/`fmtWindow` 空值 & 非法值**：`fmtTs` 已对空/非法输入兜底。✅
- 未发现会对本改动输出产生影响的 `CLAUDE.md` 约定约束。

---

## 附：测试稳定性备注

`App.test.ts` 与 `ReviewStage.test.ts` 在并发/整批运行下偶发 5s 超时失败，属已记录的既有 flake（见 `docs/OPERATIONS.md` §7），单独运行/加长超时即通过，与本次时间格式改动无关。

---

## 修复明细

下列修复均于 2026-08-06 完成，未新增对时间渲染文本的依赖，既有测试不受影响。

### 修复 1 — `latestReport` 时间戳误序
- **改动**：`apps/capability-console/src/views/InspectionReportsView.vue`
  `latestReport` 由「对 `generated_at` 做字典序比较取最新」改为 `reports.value[0] ?? null`。
- **依据**：后端 `copilot_inspection_reports` 已按 `generated_at DESC` 排序返回（`internal/store/inspection_reports.go:147`），故数组首元素即最新，且避免了对 RFC3339Nano 时间戳做字符串比较的误序风险。已核实 `GeneratedAt` 为 `time.Time`（`inspection_reports.go:28`），JSON 序列化会去小数尾零，证实原比较的脆弱性。

### 修复 2 — `formatAbsoluteTime` 加 NaN 兜底
- **改动**：`apps/capability-console/src/conversationFormat.ts`
  `formatAbsoluteTime` 开头增加 `if (Number.isNaN(d.getTime())) return iso;`，与 `formatCompactDateTime` 一致：非法/空输入原样返回，而非渲染 `NaN年NaN月…`。同步补充与现有函数风格一致的文档注释。
- **测试**：`apps/capability-console/src/conversationFormat.test.ts` 新增 `describe('formatAbsoluteTime')`，覆盖有效输入格式化与非法/空输入原样返回。

### 修复 3 — `fmtTs` 复用共享时间格式助手
- **改动**：`apps/capability-console/src/views/InspectionReportsView.vue`
  `fmtTs` 移除内联的 `new Date` + `Number.isNaN` + `padStart` 实现，改为 `formatCompactDateTime(ts, false).slice(5)` 复用共享助手（含非法兜底）并裁掉 `YYYY-` 前缀，保持 `MM-DD HH:mm` 输出不变。

### 修复 5 — `fmtWindow` 注释与实现一致
- **改动**：`apps/capability-console/src/views/InspectionReportsView.vue`
  `fmtWindow` 注释由错误的「同月只保留一次月份」改为描述实际拼接行为（`08-05 08:00 → 08-06 07:59`）。

### 保持现状（#4）— `.topbar` 四处覆盖
- 复核确认四处 `.X-entry .topbar` 覆盖为**结构性必要**：这四个视图的入口容器自带水平 `padding: 0 var(--space-6)`，需清零 `.topbar` 左右内边距；其余五个无入口 padding 的视图依赖全局 `.topbar` 水平内边距。若删覆盖收敛到全局会令那五个视图的顶栏左对齐塌陷，故不采纳。潜在的去重方案（给四入口加共享类）收益有限、超出本次范围，暂缓。
