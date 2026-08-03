# Capability Template Library

常见运维场景的能力模板库，开箱即用。

## 目录结构

```
templates/
├── kubernetes/          # K8s 资源操作
│   ├── pod.restart.yaml
│   ├── deployment.scale.yaml
│   └── logs.tail.yaml
├── cache/              # 缓存管理
│   └── redis.flush.yaml
├── database/           # 数据库运维
│   └── mysql.slow_query.yaml
├── networking/         # 网络与流量控制
│   └── traffic.drain.yaml
└── observability/      # 可观测性
    └── metrics.query.yaml
```

## 快速开始

### 1. 选择模板

浏览对应目录，找到你需要的运维场景：

- **重启 Pod** → `kubernetes/pod.restart.yaml`
- **扩缩容** → `kubernetes/deployment.scale.yaml`
- **查日志** → `kubernetes/logs.tail.yaml`
- **清缓存** → `cache/redis.flush.yaml`
- **慢查询分析** → `database/mysql.slow_query.yaml`
- **流量切走** → `networking/traffic.drain.yaml`
- **查指标** → `observability/metrics.query.yaml`

### 2. 定制模板

复制模板到你的能力目录：

```bash
cp templates/kubernetes/pod.restart.yaml \
   examples/capabilities/published/my-company.k8s.pod.restart.yaml
```

修改关键字段：

```yaml
name: my-company.k8s.pod.restart  # 改成你的命名空间
backend:
    base_url: https://your-k8s-api  # 改成你的 API endpoint
auth:
    roles: [sre, ops]  # 改成你的角色体系
```

### 3. 部署能力

将定制后的 YAML 放入：
- `examples/capabilities/published/` — 生产就绪的能力
- `examples/capabilities/draft/` — 开发中的能力

重启 Copilot API，能力自动加载：

```bash
curl -H "Authorization: Bearer $TOKEN" \
     http://localhost:19090/api/capabilities
```

## 模板说明

### 风险等级

| 等级 | 说明 | 需要审批? | 示例 |
|------|------|----------|------|
| `low` | 只读操作，无影响 | ❌ | 查日志、读指标 |
| `medium` | 写操作，可恢复 | ✅ | 重启 Pod、扩缩容 |
| `high` | 不可逆操作 | ✅✅ | 清缓存、流量切换 |

### Backend Adapter

所有模板默认使用 `http` adapter，实际部署时你可以：

1. **保持 HTTP** — 对接你自己的管理 API
2. **换成 MCP** — 改为 `adapter: mcp`, `server: kubernetes-mcp`
3. **换成 Shell** — 改为 `adapter: shell`, `command: kubectl ...`

示例（改用 kubectl）：

```yaml
backend:
    adapter: shell
    command: kubectl delete pod {{pod_name}} -n {{namespace}}
    timeout_ms: 10000
```

### Auth 角色映射

模板中的角色是示例，映射到你的实际角色体系：

| 模板角色 | 你的角色 | 权限范围 |
|---------|---------|---------|
| `viewer` | `read-only`, `analyst` | 只读观测 |
| `operator` | `sre`, `ops` | 常规操作 |
| `admin` | `platform-admin`, `security` | 高风险操作 |

在 JWT token 中配置：

```json
{
  "sub": "alice",
  "roles": ["sre", "ops"],
  "allowed_environments": ["prod", "staging"]
}
```

## 最佳实践

### 1. 能力命名规范

```
<domain>.<resource_type>.<operation>
```

示例：
- `k8s.pod.restart`
- `redis.cache.flush`
- `mysql.slow_query.analyze`

### 2. 环境隔离

所有模板都包含 `environment` 参数，确保：

```yaml
auth:
    environment_scoped: true  # 强制环境隔离
```

用户在 JWT 中声明允许的环境：

```json
{
  "allowed_environments": ["staging", "dev"]
}
```

这样即使角色是 `admin`，也无法操作 `prod`。

### 3. Precheck Tools

对于高风险操作，定义前置检查：

```yaml
governance:
    precheck_tools:
        - k8s.pod.status.read  # 先检查 Pod 是否存在
        - k8s.deployment.replicas.count  # 确保有足够副本
```

AI Agent 会在执行前自动调用这些能力。

### 4. Rollback 策略

| 策略 | 说明 | 何时使用 |
|------|------|---------|
| `manual` | 人工回滚 | Pod 重启、扩缩容 |
| `revert_scale` | 自动恢复副本数 | 扩缩容失败 |
| `restore_backend` | 恢复后端到 LB | 流量切换 |
| `none` | 不可回滚 | 清缓存、删除数据 |

## 贡献新模板

欢迎提交新的运维场景模板！

### 新增模板清单

- [ ] 完整的 `input_schema`，包含类型、必填、validation
- [ ] 清晰的 `ai.description`，说明用途和注意事项
- [ ] 至少 3 个 `ai.examples`，覆盖中英文
- [ ] `safety_notes` 列出所有风险点
- [ ] `governance` 定义审批和前置检查
- [ ] `rollback` 策略明确

### 提交流程

1. Fork 本仓库
2. 在对应 domain 目录下创建 YAML
3. 运行验证：`make validate-capabilities`
4. 提交 PR，描述模板用途和测试情况

## 常见问题

### Q: 模板中的 base_url 怎么填？

A: 三种方式：
1. **直接写死** — `http://prometheus:9090`（适合单环境）
2. **环境变量** — `${PROMETHEUS_URL}`（部署时注入）
3. **动态路由** — 改用 MCP adapter，由 MCP server 处理路由

### Q: 如何测试模板？

A: 先改 `status: draft`，调用 API 测试：

```bash
curl -X POST http://localhost:19090/api/agent/chat \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "message": "重启 dev 环境 default namespace 的 test-pod",
    "session_id": "test-123"
  }'
```

查看 action plan 是否正确生成。

### Q: 能力加载失败？

检查 schema 是否合法：

```bash
# 验证 YAML 格式
yq eval examples/capabilities/templates/kubernetes/pod.restart.yaml

# 检查 API 日志
docker logs copilot-api 2>&1 | grep capability
```

## 路线图

- [ ] 更多 domain：消息队列、对象存储、服务网格
- [ ] 能力组合（Composition）：一个能力调用多个子能力
- [ ] 可视化编辑器：Web UI 创建能力，无需手写 YAML
- [ ] 社区贡献：能力市场，版本管理，评分评论

---

**维护者**: AIOps Platform Team  
**更新时间**: 2026-08-03
