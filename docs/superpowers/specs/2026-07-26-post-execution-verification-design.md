# 计划执行后自动验证

## 问题

写操作执行成功后,用户只能去审计页人工确认「到底生效没」。AI 助手停在「点了执行」,没有闭环到「确认变更生效」。

## 目标

写能力执行成功后,自动调用关联的 read 能力,在执行结果里返回「变更后状态」,让用户直接看到变更生效。

## 数据模型

### Capability 增加 VerifySpec

```yaml
verify:
  read_capability: kafka.consumer_group.lag.read
  input_mapping:
    cluster: "{cluster}"
    group: "{topic}"
  timeout_ms: 3000
```

- `read_capability`: 验证用的 read 能力名,必须已发布
- `input_mapping`: 从写操作 input 映射到 read input 的模板(简单字符串替换)
- `timeout_ms`: 验证调用超时,默认 3000ms

零值 `Verify` 表示该写能力不做自动验证。

### Execution 结构扩展

```go
type Execution struct {
    // ... 原有字段
    Verification *VerificationResult
}

type VerificationResult struct {
    ToolName  string         `json:"tool_name"`
    Status    string         `json:"status"`      // success / failed / skipped / denied
    Answer    map[string]any `json:"answer,omitempty"`
    Error     string         `json:"error,omitempty"`
    ElapsedMs int64          `json:"elapsed_ms"`
}
```

## 接口设计

### Verifier 接口(新)

execution 包新增一个可选依赖:

```go
type Verifier interface {
    Verify(ctx context.Context, plan store.PlanRecord, input map[string]any) (*VerificationResult, error)
}
```

`execution.Service` 持有可选的 `Verifier`。`ExecuteConfirmedPlan` 成功后,若 verifier 非空,调用它填充 `Execution.Verification`。

### CapabilityWriteRunner 实现 Verifier

`CapabilityWriteRunner` 同时实现 `Executor` 和 `Verifier`:

- 执行写:走原 `Execute`
- 验证:查写能力的 `Verify` 配置,解析 input_mapping,调 `CapabilityReadRunner.Read`,组装结果

`CapabilityWriteRunner` 需要新增对 `ReadRunner` 的引用。但为避免循环,write runner 持有的是 `execution.ReadRunner` 接口,不是具体的 `CapabilityReadRunner`。

### 权限与审计

verify 走 governed read 路径:
- 用 plan.ConfirmedBy 构造 internal identity
- 调 `policy.Evaluate` 检查 read 能力权限
- 调 read runner.Read 执行
- 写审计事件(action="verification_executed")

## 执行流程

1. `ExecuteConfirmedPlan` 执行写操作成功
2. 若 `s.verifier` 非空,调用 `s.verifier.Verify(ctx, plan, snapshot)`
3. verifier 查写能力的 `Verify` 配置:
   - 空 → 返回 nil
   - 非空 → 解析 mapping,调 governed read,返回 VerificationResult
4. 把 VerificationResult 放到 `Execution.Verification`
5. router 透传到 HTTP 响应

**关键约束**:
- verify 失败不影响主 execution 的 success 状态
- verify 必须有超时(ctx 带 timeout)
- verify 失败写审计(action="verification_failed"),成功写 action="verification_succeeded"

## 落地步骤(TDD)

1. **数据模型**: `Capability` 增加 `Verify *VerifySpec`,更新 loader/parser
2. **types**: execution 包新增 `VerificationResult`,Service 增加 verifier 字段
3. **Verifier 接口与实现**: `CapabilityWriteRunner.Verify` 方法
4. **execution.Service**: `ExecuteConfirmedPlan` 成功后调 verifier
5. **router**: 透传 verification
6. **examples**: 为 `topic.retention.set` 声明 verify(需新增 kafka lag read 能力 + write 能力 examples)
7. **前端**: ExecutionResultView 加 verification 区块 + 类型
8. **mock**: retention 写后 read 能看到 retention_hours 变化
9. **全量验证**: go vet/build/test + vitest + make dev-verify-trace

## 边界

- **静态工具 verify**: 静态 `topic.retention.set` 走 staticWriteExecutor,不参与 verify。只有通过 capability console 发布的写能力才能配 verify。
- **循环依赖**: CapabilityWriteRunner 持有 execution.ReadRunner 接口,不是具体类型。main.go 组装时传入 CapabilityReadRunner。
- **超时**: verify 的 ctx 带 timeout,避免 read 端点挂死。
- **权限**: verify 用 plan.ConfirmedBy 构造 identity,需重新走 policy.Evaluate(read 能力可能需要不同 role)。
