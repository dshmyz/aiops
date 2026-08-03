# Capability Marketplace

能力市场是一个团队共享、发现和复用运维能力的平台。

## 功能特性

- ✅ **版本管理** — 语义化版本控制，支持多版本并存
- ✅ **评分评论** — 用户评价和使用反馈
- ✅ **下载统计** — 跟踪能力的流行度和使用趋势
- ✅ **权限控制** — 私有/团队/公开三级可见性
- ✅ **依赖管理** — 声明能力依赖关系（开发中）
- ✅ **使用分析** — 执行成功率、耗时、环境分布

## API 端点

### 1. 发布能力

```bash
POST /api/marketplace/capabilities
Authorization: Bearer <token>
Content-Type: application/json

{
  "yaml_content": "schema_version: 1\nname: k8s.pod.restart\n...",
  "version": "1.0.0",
  "visibility": "public",
  "tags": ["kubernetes", "operations"],
  "category": "Infrastructure",
  "changelog": "Initial release"
}
```

**响应**:

```json
{
  "capability": {
    "id": "uuid-1",
    "name": "k8s.pod.restart",
    "domain": "kubernetes",
    "visibility": "public",
    "download_count": 0,
    "rating_count": 0
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
GET /api/marketplace/capabilities?query=restart&domain=kubernetes&sort_by=downloads
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
| `sort_by` | string | 排序方式（downloads、rating、created_at、usage） |
| `limit` | int | 每页结果数（默认 20） |
| `offset` | int | 偏移量 |

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
      "usage_count": 150,
      "avg_rating": 4.5,
      "rating_count": 8
    }
  ],
  "total": 1,
  "limit": 20,
  "offset": 0
}
```

### 3. 查看能力详情

```bash
GET /api/marketplace/capabilities/:id
```

### 4. 查看所有版本

```bash
GET /api/marketplace/capabilities/:id/versions
```

**响应**:

```json
{
  "versions": [
    {
      "id": "uuid-3",
      "version": "1.1.0",
      "status": "published",
      "changelog": "Added support for StatefulSets",
      "published_at": "2026-08-03T10:00:00Z"
    },
    {
      "id": "uuid-2",
      "version": "1.0.0",
      "status": "published",
      "changelog": "Initial release",
      "published_at": "2026-08-01T08:00:00Z"
    }
  ]
}
```

### 5. 下载能力

```bash
GET /api/marketplace/capabilities/:id/download/:version_id
Authorization: Bearer <token>
```

**响应**:

```json
{
  "yaml_content": "schema_version: 1\nname: k8s.pod.restart\n...",
  "version": "1.0.0",
  "download_url": "/api/marketplace/capabilities/uuid-1/download/uuid-2"
}
```

下载会自动记录到 `capability_downloads` 表。

### 6. 评分

```bash
POST /api/marketplace/capabilities/:id/ratings
Authorization: Bearer <token>
Content-Type: application/json

{
  "rating": 5,
  "review": "Works perfectly in our prod environment!",
  "version_used": "1.0.0",
  "environment": "prod"
}
```

同一用户对同一能力的多次评分会更新之前的评分。

### 7. 查看评分

```bash
GET /api/marketplace/capabilities/:id/ratings?limit=10&offset=0
```

### 8. 使用统计

```bash
GET /api/marketplace/capabilities/:id/stats
```

**响应**:

```json
{
  "capability_id": "uuid-1",
  "total_downloads": 42,
  "total_executions": 150,
  "success_rate": 0.94,
  "avg_execution_time": 3200,
  "executions_by_env": {
    "prod": 80,
    "staging": 50,
    "dev": 20
  },
  "executions_last_30d": [
    {"date": "2026-08-03", "count": 12, "success": 11},
    {"date": "2026-08-02", "count": 8, "success": 8}
  ]
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

### 版本弃用

```bash
PATCH /api/marketplace/capabilities/:id/versions/:version_id
Content-Type: application/json

{
  "status": "deprecated",
  "deprecation_reason": "Security vulnerability fixed in 1.1.0"
}
```

## 权限和可见性

### 可见性级别

| 级别 | 说明 | 谁能看到 |
|------|------|---------|
| `private` | 私有 | 只有创建者 |
| `team` | 团队 | 同一 organization_id 的成员 |
| `public` | 公开 | 所有人 |

### JWT 权限

发布能力需要 JWT 中包含：

```json
{
  "sub": "user-123",
  "roles": ["operator", "admin"],
  "organization_id": "my-company"
}
```

- `owner_id` 自动从 JWT `sub` 提取
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

## 依赖管理（开发中）

### 声明依赖

能力可以依赖其他能力:

```yaml
dependencies:
  - name: service.health.check
    type: required
    version: ">=1.0.0 <2.0.0"
    execution_order: 1
  - name: lb.backend.connections.count
    type: optional
    version: "^1.2.0"
    execution_order: 2
```

### 依赖解析

系统自动：

1. 检查依赖的能力是否已安装
2. 验证版本约束
3. 按 `execution_order` 顺序执行
4. 如果 required 依赖缺失，阻止能力执行

### 循环依赖检测

数据库约束防止循环依赖:

```sql
CHECK (capability_id != depends_on_capability_id)
```

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

- [ ] **CLI 工具** — `copilot-cli capability search/install/publish`
- [ ] **Web UI** — 可视化浏览和管理能力
- [ ] **依赖解析器** — 自动安装依赖，版本冲突检测
- [ ] **CI/CD 集成** — GitHub Actions 自动发布能力
- [ ] **社区市场** — 公开的能力分享平台
- [ ] **语义搜索** — 向量检索，自然语言查询（"我想重启 Kafka"）

---

**维护者**: AIOps Platform Team  
**更新时间**: 2026-08-03
