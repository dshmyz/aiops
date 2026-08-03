# Planner 评估框架设计

## 背景与动机

当前 AI 运维助手的 EinoPlanner 测试存在三个问题：

1. **无评估框架**：`eino_planner_test.go` 中的 `fakeEinoChatModel` 是局部 stub，硬编码 LLM 响应，无数据驱动用例集、无准确率统计、无回归基线。任何 prompt 改动、模型升级、JSON 解析逻辑调整都没有量化护栏。

2. **无 token 预算控制**：当前 EinoPlanner 把最近 10 个 turn 的 Content 字符串全量拼接进上下文，长 turn 会撑爆模型上下文窗口，且丢失上一轮的 Intent/Selection/Trace 结构化信息。

3. **演化路径受阻**：未来要做的上下文工程改造（结构化 history、滚动摘要、token 预算）缺乏验证手段——改完之后效果是变好还是变坏无法量化。

本设计是后续上下文工程改造的前置依赖，也是任何 planner 改动的护栏。

## 设计目标

借鉴 harness9 的 ScriptedProvider + 黄金数据集 + 准确率回归思路，**保留当前项目的治理闭环**（评估只到 Intent 层，不执行工具、不创建 Action Plan、不写审计）。

## 范围

### 包含

- `internal/assistant/eval` 子包：ScriptedProvider、Case、Runner、Reporter
- 80 条手写评估用例，按 tool/clarification/diagnostic 三类划分
- Markdown 报告生成（分域统计 + 失败明细 + 阈值标记）
- 框架自身单元测试（普通 `go test ./...` 跑）
- 评估套件（build tag `eval` 隔离，本地手动跑）

### 不包含

- 真实 LLM 输出录制（本期 ScriptedLLM 是手写理想 JSON）
- 多轮 history 用例（Case 结构保留 `History` 字段，本期留空）
- CI 集成（用户选择本地手动跑）
- DeterministicPlanner 和 CapabilityAwarePlanner 的评估（本期只评估 EinoPlanner）
- 上下文工程改造（独立子项目，依赖本框架先落地）

## 架构

### 模块依赖

```
internal/assistant/eval   （新）
    ↓ imports
internal/assistant        （现有，提供 EinoPlanner、Intent、Turn 类型）
internal/tools            （现有，提供 ToolName 常量）
internal/identity          （现有，提供 CurrentUser）
```

### 模块边界

- `internal/assistant/eval` 对外只暴露一个入口：`Run(ctx, cases) -> Report`
- 内部分三层：ScriptedProvider（LLM 替身）+ Runner（执行评估）+ Reporter（生成报告）
- 三层通过 Go 接口解耦，每层可独立测试

### 关键约束（保留治理闭环）

- 评估过程**不创建 Action Plan、不写审计、不调用 ReadOnlyService**
- ScriptedProvider 返回的 LLM JSON 不会被持久化、不会被传给真实工具
- EinoPlanner 现有的 plan→policy→execution 治理链路完全不动
- 评估边界是"EinoPlanner.Plan() 返回的 Intent 是否正确"，到此为止

## 核心数据结构

### Case — 评估用例

```go
type Case struct {
    Name            string
    Category        Category
    UserMessage     string
    History         []Turn
    ScriptedLLM     string
    ExpectedIntent  ExpectedIntent
    Notes           string
}

type Category string

const (
    CategoryTool          Category = "tool"
    CategoryClarification Category = "clarification"
    CategoryDiagnostic    Category = "diagnostic"
)

type ExpectedIntent struct {
    ToolName      string
    Input         map[string]any
    Selection     *ExpectedSelection
    Clarification bool
    Diagnostic    bool
}

type ExpectedSelection struct {
    Environment string
    Cluster     string
    Domain      string
}
```

**设计要点**：
- `Input` 是"部分匹配"：用例只列出关心的 key，其他 key 不检查
- `History` 字段本期留空，为后续上下文工程改造铺路
- `Category` 驱动分域统计和分域阈值

### ScriptedProvider — LLM 替身

```go
type ScriptedProvider struct {
    responses map[string]string
    calls     []string
}

func (p *ScriptedProvider) Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error)
func (p *ScriptedProvider) Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error)
```

**caseName 传递机制**：Runner 用 `context.WithValue(ctx, caseNameKey, c.Name)` 注入，ScriptedProvider 从 ctx 取出来查 responses。未找到时返回 error（防止静默走错路径）。

### Report — 评估报告

```go
type Report struct {
    Total      int
    Passed     int
    Failed     int
    ByCategory map[Category]CategoryStat
    Failures   []Failure
    Duration   time.Duration
}

type CategoryStat struct {
    Total  int
    Passed int
    Failed int
    Rate   float64
}

type Failure struct {
    CaseName string
    Category Category
    Reason   string
    Expected ExpectedIntent
    Actual   assistant.Intent
}
```

## 执行流程

### Runner 主循环

```go
func Run(ctx context.Context, cases []Case) (Report, error) {
    provider := NewScriptedProvider(cases)
    planner := buildEinoPlannerWithProvider(provider)

    report := newReport()
    for _, c := range cases {
        caseCtx := context.WithValue(ctx, caseNameKey, c.Name)
        intent, err := planner.Plan(caseCtx, evalUser(), c.UserMessage, c.History)
        report.record(c, intent, err)
    }
    return report.finalize(), nil
}
```

### 判定规则（record 内部）

按以下顺序短路判定（一个失败就停止该 case 后续检查）：

1. `err != nil` 且期望不是错误 → 失败（reason: "planner error: ..."）
2. 期望 `Diagnostic=true` → 检查 `intent.Selection.Diagnostic == true`
3. 期望 `Clarification=true` → 检查 `intent.Selection.Clarification == true`
4. 否则检查 `intent.ToolName == ExpectedIntent.ToolName`
5. ToolName 通过后检查 Input 部分匹配
6. Input 通过后检查 Selection（如果 ExpectedSelection 非 nil）

### 部分匹配语义

用例列出 `Input["environment"] = "prod"`，实际 `intent.Input` 中必须有 `environment` 键且值为 `prod`。其他键（例如 LLM 返回多了一个不相关的 `cluster`）不视为失败——因为 LLM 输出可能确实包含额外字段，只要核心字段对就算通过。

### 退出码策略

- 核心 category（`tool`）失败 → exit 1
- 边缘 category（`clarification`、`diagnostic`）未达 ≥90% 阈值 → exit 0（因为是手动跑而非 CI 门）

### Reporter markdown 输出

```markdown
# EinoPlanner Evaluation Report

Generated: 2026-07-27 16:30
Duration: 1.2s
Total: 80 | Pass: 76 | Fail: 4

## By Category

| Category       | Total | Pass | Fail | Rate   | Threshold |
|----------------|-------|------|------|--------|-----------|
| tool           | 50    | 50   | 0    | 100.0% | 100%      |
| clarification  | 20    | 18   | 2    | 90.0%  | ≥90%      |
| diagnostic     | 10    | 8    | 2    | 80.0%  | ≥90% ❌   |

## Failures

### clarification/case_42_ambiguous_cluster
**Reason**: expected clarification=true, got ToolName=kafka.topic.retention.read
**Expected**: clarification needed (cluster not specified)
**Actual**:    ToolName=kafka.topic.retention.read, Input={environment:prod, topic:orders}
```

报告位置：`internal/assistant/eval/report.md`（每次跑覆盖，加入 `.gitignore`）

## 文件结构

```
internal/assistant/eval/
├── eval.go                  # 公开类型：Case、Category、ExpectedIntent、ExpectedSelection
├── provider.go              # ScriptedProvider 实现
├── runner.go                # Run() 主循环 + record/finalize 逻辑
├── reporter.go              # Report 类型 + markdown 生成
├── eval_test.go             # 框架自身单元测试（普通 go test 跑）
├── provider_test.go         # ScriptedProvider 测试
├── cases_tool.go            # tool 类 50 条用例（纯数据）
├── cases_clarification.go   # clarification 类 20 条
├── cases_diagnostic.go      # diagnostic 类 10 条
├── cases.go                 # 汇总 Cases []Case 切片
├── eval_suite_test.go       # build tag=eval，跑全量评估 + 生成 report.md
├── .gitignore               # 忽略 report.md
└── report.md                # 生成产物
```

**设计要点**：

- **数据与逻辑分离**：`cases_*.go` 三个文件只放纯数据，无函数；`cases.go` 汇总导出 `var Cases`。增加用例只需编辑数据文件，框架逻辑不感知。
- **eval.go 与 provider.go 分离**：`eval.go` 是类型定义（被 cases 引用），`provider.go` 是 ScriptedProvider 实现。cases 包只 import 类型，不耦合实现细节。
- **eval_suite_test.go 带 build tag** `//go:build eval`。它不测框架逻辑，只跑全量评估并断言阈值。
- **现有测试不动**：`internal/assistant/*_test.go` 全部保持原样。`fakeEinoChatModel` 保留，不替换为 ScriptedProvider——它服务的是单元测试，与评估框架职责不同。
- **build tag 隔离不污染主套件**：普通 `go test ./...` 编译框架代码（eval.go/provider.go/runner.go/reporter.go/cases_*.go/eval_test.go/provider_test.go），但跳过 eval_suite_test.go。框架自身 bug 能被普通 CI 发现，"评估是否通过"是本地手动行为。

## 入口命令

```bash
go test -tags=eval ./internal/assistant/eval/...
```

跑完会在包目录下生成 `report.md`。

## 测试策略

### 框架本身的自测（单元测试，普通 `go test ./...` 跑）

**Runner 单元测试**：
- `TestRunnerPassesWhenIntentMatches` — happy path
- `TestRunnerFailsWhenToolNameMismatch`
- `TestRunnerPartialInputMatchIgnoresExtraKeys` — 实际多一个未声明 key，通过
- `TestRunnerPartialInputMatchFailsOnMissingKey` — 实际缺声明 key，失败
- `TestRunnerClarificationPath` — 期望 clarification=true，实际有 ToolName，失败
- `TestRunnerDiagnosticPath` — 期望 diagnostic=true，实际走 tool 路径，失败
- `TestRunnerContextPropagation` — ScriptedProvider 能从 ctx 取 caseName
- `TestReporterMarkdownStructure` — 报告包含分域统计表、Failures 章节、阈值标记

**ScriptedProvider 单元测试**：
- `TestScriptedProviderReturnsCaseResponse`
- `TestScriptedProviderFailsOnUnknownCase`
- `TestScriptedProviderStreamWrapsGenerate`

### 评估套件测试（build tag `eval`）

- `TestEvalSuiteCoreTool100Percent` — 跑 tool 类用例，断言 Passed == Total
- `TestEvalSuiteClarificationAtLeast90Percent` — 跑 clarification 类用例，断言 Rate >= 0.9
- `TestEvalSuiteDiagnosticAtLeast90Percent` — 跑 diagnostic 类用例，断言 Rate >= 0.9

## 初始数据集规划

80 条用例，分三个 category：

### tool 类（50 条）— 核心，要求 100%

| 域 | 工具 | 用例数 | 覆盖维度 |
|----|------|--------|---------|
| MinIO | `minio.bucket.capacity.read` | 8 | 中英文、环境（prod/staging/dev）、bucket 名 |
| Kafka | `kafka.topic.retention.read` | 8 | 中英文、topic 名、cluster |
| Kafka | `kafka.consumer_group.lag.read` | 8 | 中英文、group 名、cluster |
| GlusterFS | `glusterfs.volume.status.read` | 8 | 中英文、volume 名 |
| 模糊表达 | 同上工具 | 10 | "看下"/"查一下"/"健康吗"/"啥情况"等同义表达 |
| 边界 | 同上工具 | 8 | 大小写、空格、参数缺失（应该路由到 clarification） |

### clarification 类（20 条）— 边缘，要求 ≥90%

| 子类 | 用例数 | 场景 |
|------|--------|------|
| 缺环境 | 5 | "查 kafka lag" 不带 environment |
| 缺关键参数 | 5 | "查 kafka lag" 不带 group 名 |
| 歧义域 | 5 | "查 prod lag" 不指明 kafka/minio |
| 多重缺失 | 5 | 缺多个字段 |

### diagnostic 类（10 条）— 边缘，要求 ≥90%

按 README 已声明的三个诊断关键词：
- "检查 ... 健康"（GlusterFS/MinIO/Kafka 各 3 条）
- "诊断 ..."（1 条变体）

### 用例 schema 一致性

每条用例必须包含：
- `Name`：唯一，命名规范 `<category>/<域>_<场景>`，例如 `tool/minio_capacity_prod`
- `ScriptedLLM`：预设的 LLM 响应 JSON，格式遵循 EinoPlanner 当前解析的 JSON schema
- `ExpectedIntent`：至少一个非空字段（ToolName / Clarification / Diagnostic）

### ScriptedLLM 内容来源

本期采用**手写理想 JSON**策略，明确评估边界是"planner 解析 + Intent 构造"。

理由：
- 用户选择"仅 EinoPlanner + ScriptedProvider"，LLM 调用有成本
- 评估的边界是"planner 解析逻辑 + Intent 构造"，不是"prompt 引导 LLM 输出的能力"
- 真实 LLM 输出录制在上下文工程改造后才有意义（改 prompt 后需要重新录制），那时再升级

## 阈值规则

- 核心 category（`tool`）要求 100% 通过
- 边缘 category（`clarification`、`diagnostic`）要求 ≥ 90%
- 阈值通过常量定义在 `internal/assistant/eval`，可后续调整

## 与现有代码的关系

- **不修改** `internal/assistant` 的任何现有文件（EinoPlanner 已接受 `model.BaseChatModel`，ScriptedProvider 就是实现这个接口）
- **不引入** 新的运行时依赖（不解析 YAML、不调用真实 LLM）
- **不污染** 主测试套件：build tag `eval` 隔离
- **不动** 现有 `fakeEinoChatModel`、`fakePlanner`、`historyCapturingPlanner` 局部 stub（它们服务的单元测试与评估框架职责不同）

## 未涉及

以下事项本期不做，留待后续：

1. **真实 LLM 输出录制** — 上下文工程改造完成后，需要重新录制 ScriptedLLM 才能评估新 prompt 在真实 LLM 输出下的端到端行为
2. **多轮 history 用例** — Case 结构保留 `History` 字段，本期留空；上下文工程改造后填充
3. **CI 集成** — 用户选择本地手动跑；后续如需 CI 门，加一个跑 `go test -tags=eval` 的 workflow 即可
4. **DeterministicPlanner 和 CapabilityAwarePlanner 的评估** — 本期只评估 EinoPlanner；其他两个 planner 是纯函数/规则路由，单元测试已足够
5. **上下文工程改造** — 独立子项目，依赖本框架先落地后才能量化验证改造效果
