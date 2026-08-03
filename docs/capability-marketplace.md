# Capability Marketplace

能力市场是一个团队共享、发现和复用运维能力的平台。

## 功能特性

- ✅ **版本管理** — 语义化版本控制，支持多版本并存
- ✅ **评分评论** — 用户评价和使用反馈
- ✅ **下载统计** — 跟踪能力的流行度和使用趋势
- ✅ **权限控制** — 私有/团队/公开三级可见性
- ✅ **依赖管理** — 声明能力依赖关系并解析执行顺序
- ✅ **CI 校验** — 提交时 `capability-validator` 自动校验 schema、扫描密钥、dry-run
- ✅ **使用分析** — 执行成功率、耗时、环境分布

## API 端点

所有 marketplace 路由挂在 `/v1/marketplace/capabilities` 下。读操作对 viewer/operator/admin 开放；**发布需要 admin**（发布的能力会成为可执行的基础设施）。发布时的 `owner` 取自认证主体，绝不来自请求体。

### 1. 发布能力

```bash
POST /v1/marketplace/capabilities
Authorization: Bearer <token>
Content-Type: application/json

{
  "yaml_content": "schema_version: 1\nname: k8s.pod.restart\n...",
  "version": "1.0.0",
  "visibility": "public",
  "tags": ["kubernetes", "operations"],
  "category": "Infrastructure"
}
```

发布前会先 `capabilities.Validate` 校验 YAML——市场绝不发放运行时加载器会拒绝的能力。

**响应**:

```json
{
  "capability": {
    "id": "uuid-1",
    "name": "k8s.pod.restart",
    "domain": "kubernetes",
    "visibility": "public"
  },
  "version": {
    "id": "uuid-2",
    "version": "1.0.0",
    "status": "published"
  }
}
```

### 2. 搜索能力

```bash
GET /v1/marketplace/capabilities?query=restart&domain=kubernetes&sort_by=downloads
```

**查询参数**:

| 参数 | 类型 | 说明 |
|------|------|------|
| `query` | string | 搜索关键词（匹配 name 和 description） |
| `domain` | string | 过滤 domain（kubernetes、cache、database） |
| `category` | string | 过滤分类（Infrastructure、Observability） |
| `risk_level` | string | 过滤风险等级（low、medium、high） |
| `min_rating` | float | 最低评分（0.0 - 5.0） |
| `visibility` | string | 可见性（private、team、public） |
| `status` | string | 状态（published、deprecated） |
| `sort_by` | string | 排序方式（downloads、rating、created_at、usage） |
| `limit` | int | 每页结果数（默认 20，上限 100） |
| `offset` | int | 偏移量 |

`sort_by` 只接受固定的字面量集合，不会发生 SQL 注入；LIKE 通配符会转义。

**响应**:

```json
{
  "capabilities": [
    {
      "id": "uuid-1",
      "name": "k8s.pod.restart",
      "domain": "kubernetes",
      "description": "Restart a Kubernetes pod...",
      "tags": ["kubernetes", "operations"],
      "download_count": 42,
      "avg_rating": 4.5,
      "rating_count": 8
    }
  ],
  "total": 1,
  "next_offset": null
}
```

有更多页时 `next_offset` 为下一页的偏移量。

### 2.1 语义搜索（自然语言查询）

除了关键词匹配，市场还支持**语义搜索**：用自然语言描述要的操作，即使能力名里没有这些词也能召回。

```bash
GET /v1/marketplace/capabilities?semantic=true&query=我想重启%20Kafka
```

当 `semantic=true` 时，查询文本会被嵌入成向量并在能力索引里按余弦相似度检索——`kafka.broker.restart` 这类描述里写了 "restart Kafka broker" 的能力会命中，尽管查询词与能力名并不字面匹配。

**响应**:

```json
{
  "capabilities": [
    {
      "id": "uuid-1",
      "name": "kafka.broker.restart",
      "domain": "kubernetes"
    }
  ],
  "total": 1,
  "semantic": true
}
```

语义索引如何构建：

- **发布时自动索引**：每次 `POST /v1/marketplace/capabilities` 成功发布后，能力名、domain、operation、`ai.description` 与示例会被建成一条 knowledge 文档（ID 前缀 `capability:`）并嵌入；重复发布同一能力会替换旧文档而非叠加。
- **弃用时移除**：`Deprecate` 把该能力的文档从索引中删除，避免自然语言检索命中已下线的基础设施。
- **启用条件**：需在 `cmd/copilot-api` 里配置知识库与 embedding（见 `configuration.md` 的 `COPILOT_KNOWLEDGE_EMBEDDER_*`）。未配置时 `semantic=true` 返回 `503`，普通关键词搜索不受影响。
- 语义检索不分页，只回按相似度排序的前 `limit` 条；只返回 `published` 且未弃用的能力。

### 3. 查看能力详情

```bash
GET /v1/marketplace/capabilities/{id}
```

### 4. 查看所有版本

```bash
GET /v1/marketplace/capabilities/{id}/versions
```

### 5. 下载能力

```bash
GET /v1/marketplace/capabilities/{id}/download/{version_id}
Authorization: Bearer <token>
```

**响应**:

```json
{
  "version": "1.0.0",
  "yaml_content": "schema_version: 1\nname: k8s.pod.restart\n...",
  "yaml_hash": "sha256:<hex>"
}
```

下载会自动记录到 `capability_downloads` 表；该记录是 best-effort——统计写入失败不会拒绝返回 YAML。

### 6. 评分

```bash
POST /v1/marketplace/capabilities/{id}/ratings
Authorization: Bearer <token>
Content-Type: application/json

{
  "rating": 5,
  "review": "Works perfectly in our prod environment!",
  "version_used": "1.0.0",
  "environment": "prod"
}
```

`rating` 必须在 1–5。同一用户对同一能力的多次评分会更新之前的评分。

### 7. 查看评分

```bash
GET /v1/marketplace/capabilities/{id}/ratings?limit=10&offset=0
```

### 8. 使用统计

```bash
GET /v1/marketplace/capabilities/{id}/stats
```

**响应**:

```json
{
  "capability_id": "uuid-1",
  "total_downloads": 42,
  "total_executions": 150,
  "success_rate": 0.94,
  "avg_duration_ms": 3200,
  "by_environment": {
    "prod": 80,
    "staging": 50,
    "dev": 20
  }
}
```

## 版本管理

### 语义化版本

遵循 [SemVer](https://semver.org/) 规范:

- `1.0.0` — Major.Minor.Patch
- `1.1.0` — 新增功能（向后兼容）
- `1.0.1` — Bug 修复
- `2.0.0` — 不兼容变更

### 发布新版本

1. 修改 YAML 文件
2. 更新 `version` 字段
3. 填写 `changelog` 说明改动
4. 如果有不兼容变更，填写 `breaking_changes`
5. 调用 `POST /api/marketplace/capabilities`

旧版本仍然保留，用户可以选择使用特定版本。

> 服务层已提供 `Deprecate` 用于把能力标记为 `deprecated`，但当前 HTTP 路由尚未暴露该端点；
> 如需通过 API 弃用，可在 `serveMarketplace` 中补一条 `PATCH /v1/marketplace/capabilities/{id}` 路由。

## 权限和可见性

### 可见性级别

| 级别 | 说明 | 谁能看到 |
|------|------|---------|
| `private` | 私有 | 只有创建者 |
| `team` | 团队 | 同一 organization_id 的成员 |
| `public` | 公开 | 所有人 |

### 角色权限

| 操作 | 所需角色 |
|------|---------|
| 搜索 / 查看 / 下载 / 评分 | viewer、operator、admin |
| 发布能力 | admin（仅此） |

- `owner` 自动从认证主体提取，忽略请求体中的 `owner` 字段
- `organization_id` 用于团队可见性控制

### 下载权限

| 可见性 | 下载权限 |
|--------|---------|
| `public` | 任何认证用户 |
| `team` | 同一 organization_id |
| `private` | 仅创建者 |

## 使用场景

### 场景 1: 团队内共享最佳实践

1. SRE 团队编写一个高质量的 `k8s.pod.restart` 能力
2. 设置 `visibility: team`
3. 团队成员搜索并下载
4. 复用到自己的 Copilot 环境

### 场景 2: 构建公司能力库

1. 平台团队创建标准能力（MySQL 慢查询、Redis 清缓存）
2. 设置 `visibility: public`（在公司内部 Copilot 实例）
3. 所有业务团队可搜索、下载、评分
4. 根据评分和使用量，持续优化热门能力

### 场景 3: 开源社区分享

1. 贡献者上传通用能力（Prometheus 查询、Grafana 告警）
2. 设置 `visibility: public`
3. 其他组织下载并在自己环境部署
4. 通过评分和 issue 反馈改进

## 依赖管理

依赖在能力 YAML 里声明（`depends_on`），由运行时解析器在执行前构建执行顺序。

### 声明依赖

```yaml
# "重启服务" 依赖 "流量切走"：先 drain 流量，服务重启后再恢复流量
name: service.restart
operation: write
depends_on:
  - capability: service.traffic.drain
    type: required        # required | optional | suggested（默认 required）
    phase: pre            # pre（默认）| post
    input_mapping:        # 把本能力输入映射到依赖能力输入
      lb_name: '{lb_name}'
      backend_id: '{host}'
  - capability: service.health.check
    type: suggested
    phase: post
```

### 依赖解析

`internal/capabilities` 的 `DependencyResolver.Resolve(name, input)` 使用带环检测的 DFS 拓扑排序：

1. `pre` 依赖先于本能力执行，`post` 依赖后执行
2. `required` 依赖失败 → 阻止整个执行链；`optional`/`suggested` 失败 → 降级跳过
3. 菱形依赖只调度一次（去重）；无法满足的必需依赖报错
4. 依赖输入通过 `input_mapping` 映射；未映射的同名输入自动透传；`environment` 始终透传

### 校验与环检测

- `capabilities.Validate` 校验单个能力的依赖规格（无自依赖、无重复、type/phase 合法）
- `capabilities.ValidateDependencies` 校验全图：未注册的必需依赖、published 依赖非 published、以及 0/1/2 着色环检测（`a -> b -> a`）
- 加载器 `LoadPublished` 在启动时即校验全图，环或缺失依赖会拒绝启动

> 数据库并不用 CHECK 约束防环（那只能防自环）；环在加载期由 `ValidateDependencies` 检测。

## 最佳实践

### 1. 命名规范

```
<domain>.<resource_type>.<operation>[.<qualifier>]
```

示例:
- ✅ `k8s.pod.restart`
- ✅ `mysql.slow_query.analyze`
- ✅ `redis.cache.flush.pattern`
- ❌ `restart-pod`（缺少 domain）
- ❌ `kubernetes-pod-restart-tool`（过于冗长）

### 2. 版本策略

- **初始版本**: `1.0.0`（不要用 `0.x.x`，表示还不稳定）
- **Bug 修复**: 增加 patch 版本（1.0.1）
- **新增功能**: 增加 minor 版本（1.1.0）
- **不兼容变更**: 增加 major 版本（2.0.0）+ 详细的 breaking_changes 说明

### 3. Changelog 写作

好的 changelog:

```
Added support for StatefulSet restart (not just Deployment/ReplicaSet).
Fixed timeout when pod has >100 active connections.
```

糟糕的 changelog:

```
Bug fixes and improvements.
```

### 4. 测试后再发布

1. 在 `dev` 环境测试
2. 在 `staging` 验证
3. 收集小范围用户反馈
4. 发布到 marketplace

### 5. 响应反馈

- 定期查看评分和评论
- 对低分评价回复并改进
- 对高频问题更新文档或改进 YAML

## 数据库 Schema

详见 [`migrations/015_capability_marketplace.sql`](../../migrations/015_capability_marketplace.sql)

核心表:

- `capability_registry` — 能力元数据
- `capability_versions` — 版本历史
- `capability_ratings` — 用户评分
- `capability_downloads` — 下载记录
- `capability_usage_stats` — 执行统计
- `capability_dependencies` — 依赖关系

## 路线图

- [x] **CI/CD 集成** — `capability-validator` 在 `capability-validation.yml` 中校验每个提交
- [x] **依赖解析器** — `DependencyResolver` 拓扑排序 + 环检测
- [ ] **CLI 工具** — `copilot-cli capability search/install/publish`
- [ ] **Web UI** — 可视化浏览和管理能力
- [ ] **弃用 API** — 暴露 `PATCH /v1/marketplace/capabilities/{id}` 路由
- [ ] **社区市场** — 公开的能力分享平台
- [x] **语义搜索** — 向量检索，自然语言查询（"我想重启 Kafka"）

---

**维护者**: AIOps Platform Team  
**更新时间**: 2026-08-03
