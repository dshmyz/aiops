# Capability CI Pipeline

自动化能力文件验证管道，确保每次提交的能力都符合 schema、安全且可在运行时加载。

## 功能特性

- ✅ **YAML 语法验证** (`validate-syntax`) — 严格键匹配解析，拼写错误的字段在此层被拦截
- ✅ **Schema 验证** (`validate-schema`) — 运行与运行时加载器**完全相同**的 `capabilities.Validate`，并验证能力可注册为工具
- ✅ **必填字段检查** (`check-required`) — 运行时容忍但实际不可用的能力（缺 AI 描述、缺回滚源等）
- ✅ **密钥扫描** (`scan-secrets`) — 硬编码凭据 **硬失败**（泄漏进 git 历史无法挽回）
- ✅ **Dry-run 测试** (`dry-run`) — 解析 base_url、渲染请求路径、跨文件解析依赖图
- ✅ **PR 评论** — 自动在 PR 中发布/更新验证报告

## 工作流程

### 触发条件

当以下情况时自动运行：

1. **Pull Request** 修改了 `capabilities/**/*.yaml` 或 `examples/capabilities/**/*.yaml`
2. **Push to main** 修改了能力文件

### 验证步骤

```mermaid
graph LR
    A[检测变更文件] --> B[YAML 语法]
    B --> C[Schema 验证]
    C --> D[必填字段]
    D --> E[密钥扫描]
    E --> F[Dry-run 测试]
    F --> G[生成报告]
    G --> H[PR 评论]
```

每一步是一个独立的 `capability-validator <check>` 进程；每个进程把发现写入共享状态（默认 `.capability-validation.json`）。即使某步失败，状态也会被保存，这样带 `if: always()` 的 report 步骤仍能汇总全部错误。

## 命令行

```bash
capability-validator <check> --files <newline-或逗号分隔的路径>
```

| check | 说明 |
|---|---|
| `validate-syntax` | 把每个文件当作 YAML 用严格键匹配（`KnownFields(true)`）解析 |
| `validate-schema` | 运行运行时同款 `capabilities.Validate`，再 `capabilities.ToTool` 验证可注册 |
| `check-required` | 检查运行时允许、但运营上不可用的字段缺失 |
| `scan-secrets` | 硬失败：提交进 YAML 的硬编码凭据 |
| `dry-run` | 解析 base_url、渲染路径、跨文件解析依赖图 |
| `report` | 把累计发现渲染成 markdown 报告 |
| `all` | 依次运行所有检查，然后写报告 |

公共 flag：`--files`（必填，换行/逗号/空格分隔）、`--output`（报告路径，默认 `validation-report.md`）、`--state`（跨步骤状态路径，默认 `.capability-validation.json`）。

### 1. YAML 语法验证

```bash
go run ./cmd/capability-validator validate-syntax --files "examples/capabilities/templates/kubernetes/pod.restart.yaml"
```

未知字段会报错，而不是被静默忽略（`yaml.Decoder` + `KnownFields(true)`）。

### 2. Schema 验证

```bash
go run ./cmd/capability-validator validate-schema --files "examples/capabilities/published/kafka.topic.retention.write.yaml"
```

校验内容与运行时加载器完全一致，例如：

- `schema_version` 必须为 `1`
- `status` 属于枚举：`discovered` / `needs_review` / `published` / `deprecated`
- `operation` 为 `read` / `write`；`risk` 为 `low` / `medium` / `high`
- `backend.path` 的 `{name}` 变量都必须在 `input_schema` 中声明
- read 能力的方法必须是 `GET`
- `governance.rollback`、`ai.description` 等结构校验
- 依赖规格（`depends_on`）合法：无自依赖、无重复、type/phase 有效

之后调用 `capabilities.ToTool`——能通过 schema 但无法注册为工具的能力仍视为失败。

### 3. 必填字段检查

运行时能容忍、但运营上使得能力不可用的字段缺失：

- `ai.description` 缺失 → 错误（planner 靠描述选择能力）；过短 → 警告
- `ai.examples` 为空 → 警告
- `backend.timeout_ms` 未设 → 警告（用适配器默认值）
- 高风险 write 能力无 `governance.rollback.source` → 错误（无法回滚的变更）
- write 能力无 `verify` 规格 → 警告
- 无界的整数输入（可能造成破坏性写入）→ 警告

### 4. 密钥扫描

扫描硬编码的敏感信息，**命中即硬失败**：

- AWS Access Key ID (`AKIA[0-9A-Z]{16}`)
- 私钥块 (`-----BEGIN ... PRIVATE KEY-----`)
- Bearer token / JWT / 赋值式凭据

**豁免**: 值完全由占位符组成的行引用的是秘密而非包含秘密：

```yaml
# ❌ 会失败
password: "my-secret-password-123"

# ✅ 引用环境变量，不报警
password: ${DB_PASSWORD}
token: {{ .Secrets.db_password }}
password: $DB_PASSWORD
```

### 5. Dry-run 测试

不调用后端，检查所有能在本地证明的事：

- `backend.base_url` 是合法绝对 URL（`published` 能力必须有）
- 用 schema 合成输入渲染 `backend.path`，路径变量若无法被 schema 满足则失败
- 跨文件解析依赖图：`required` 依赖缺失（在变更集内）→ 失败；依赖在变更集外 → 降级为警告

### 6. 生成报告

```bash
go run ./cmd/capability-validator report --files ... --output validation-report.md
```

输出 Summary / Failures / Warnings 三段的 markdown。

## 本地使用

```bash
cd aiops && go mod download
go run ./cmd/capability-validator all \
  --files "$(find examples/capabilities -name '*.yaml' | tr '\n' ',')"
```

## 配置

### GitHub Actions

工作流在 [`.github/workflows/capability-validation.yml`](../../.github/workflows/capability-validation.yml)。注意 `go-version` 需与 `go.mod`（`go 1.25`）一致。

**调整 Go 版本**:

```yaml
- name: Set up Go
  uses: actions/setup-go@v5
  with:
    go-version: '1.25'  # 与 go.mod 一致
```

### 自定义验证规则

五个检查实现在 `cmd/capability-validator/checks.go`，`Report`/`Finding` 在 `report.go`，命令分发在 `main.go`（stdlib `flag`，无 cobra 依赖）。每个 `run*` 函数的签名（`func(files []string, report *Report) error`）；通过 `report.Errorf`/`report.Warnf` 记录发现。

例如新增密钥模式，在 `secretPatterns` 追加即可：

```go
var secretPatterns = []secretPattern{
    // ...
    {"GitHub token", regexp.MustCompile(`ghp_[A-Za-z0-9]{36}`)},
    {"Slack webhook", regexp.MustCompile(`hooks\.slack\.com/services/`)},
}
```

## 集成到其他 CI

同一个二进制可在任何 CI 里以相同子命令调用；各 step 用同一个 `--state` 路径即可跨进程累计发现。

## 路线图

- [ ] **JSON Schema 验证** — 使用标准 JSON Schema 定义能力格式
- [ ] **参数类型检查** — 验证参数引用与类型匹配（可能需导出 `applyTemplate`/`buildPath`）
- [ ] **性能测试** — Benchmark 能力执行时间
- [ ] **安全评分** — 基于风险级别和操作类型打分

---

**维护者**: AIOps Platform Team  
**更新时间**: 2026-08-03
