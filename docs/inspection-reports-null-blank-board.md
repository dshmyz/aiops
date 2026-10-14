# 巡检报告白板：根因与修复

## 现象

打开「巡检报告」页面 → 左栏卡在加载骨架、右侧白板、控制台报
`TypeError: Cannot read properties of null (reading 'length')`（`InspectionReportsView.vue`）。

## 根因（已端到端复现）

`SQLInspectionReportStore.ListReports`（`internal/store/inspection_reports.go:199`）用
`var reports []InspectionReport` 收集行，**表里没有报告时它是 nil slice**；
`handleListInspectionReports` 直接 `writeCapabilityJSON(writer, reports)`，
Go 的 `json.Marshal` 把 nil slice 编成 **`null`**：

```bash
$ curl -s http://127.0.0.1:18080/v1/inspection-reports   # 空库
null
```

前端 `listInspectionReports()` 把 `null` 原样塞进 `reports`，模板里
`reports.length === 0` 直接抛 TypeError → Vue render 失败 → 视图渲染不出来 = 白板。

**触发条件就是「库里还没有日报」**：日报由 scheduler 每天生成一次，新库/换 DSN/多节点
没跑到日报时就是 0 行 → 必现。（仓库默认 `copilot-local.db` 有 3 份，所以平时看不出来。）

## 为什么不止巡检报告

同一个 nil-slice 模式是系统性的。实测（空/部分空数据）：

| 端点 | 空时返回 | 前端是否有 `?? []` |
|---|---|---|
| `GET /v1/inspection-reports` | `null` | ❌ **崩溃（本次）** |
| `GET /v1/mcp/servers` | `null`（裸数组） | ❌ `mcpServers.value = await …` |
| `GET /v1/admin/knowledge/documents` | `{"documents":null}` | ✅ |
| `GET /v1/action-plans` | `{"plans":null}` | ✅ |
| `GET /v1/executions` | `{"executions":null}` | ✅ |
| `GET /v1/scheduled-tasks` / `.../runs` | `{"tasks":null}` / `{"runs":null}` | ✅ |
| `GET /v1/runbooks`（`var low []store.Runbook` 过滤后） | `{"runbooks":null}` | ✅（`Array.isArray` 挡住，只是下拉空） |
| `GET /v1/marketplace/capabilities` | `{"capabilities":null}` | ✅ |
| `GET /v1/admin/notification-channels` | `{"channels":null}`（ChannelManager.List 返回 nil map 的 values） | ✅ |
| `GET /v1/admin/alert-actions/:name/runs` | `{"recent":null}`（`Runs()` 在 `runStore==nil` 时返回零值） | ❌ 直接存 overview |
| `/v1/capabilities` `/v1/skills` `/v1/audit-events` `/v1/incidents` `/v1/runbook-drafts` `/v1/marketplace/.../versions|ratings` `/v1/assistant/feedback` | 已是 `[]` | ✅ |

后端已有先例：`store.FeedbackPage.MarshalJSON`、`incidents.go`、`runbook_draft.go`、
`mcp`/`executions` handler 里的 `make([]T, 0, n)`——**只是没统一**。

## 修复（两层，都做）

1. **后端（根治）**：nil slice 永不落 JSON。
   - `writeJSONWithLimit` 拦不住（对象里的 nil slice 编成 `{"key":null}`），
     所以改在**取数处**把 `var x []T` 改成 `x := []T{}`（已有数据时编译器无差别），
     裸数组端点 + 有风险的包装字段各改一遍。
2. **前端（兜底）**：`api.ts` 的列表封装统一 `?? []`，对象型响应用
   `body.key ?? []`。这样后端即使再退化，视图也只会显示「暂无数据」而不是白板。

## 验证

- 空库：`curl /v1/inspection-reports` → `[]`（改前 `null`）
- 浏览器：巡检报告页列出「暂无巡检报告」空态，控制台无 TypeError
- `go build ./...`、`go test ./internal/httpapi ./internal/store`、
  `npm run build`（vue-tsc）、`npm test`