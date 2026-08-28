package capabilities

import "context"

// CapabilityStore 抽象能力的持久化操作，使 Manager 与存储后端解耦。当前有
// 两个实现：
//   - FileCapabilityStore：单机文件模式，discovered/ 与 published/ 目录下 yaml
//   - SQLCapabilityStore：多节点一致的运行时事实源（DB 可用时由 main 优先使用）
//
// Manager 业务逻辑对后端无感知，新增后端实现无需改动业务层。
type CapabilityStore interface {
	// Configured 检查 store 是否就绪（文件模式 = 目录存在；DB 模式 = 表可连）。
	Configured() error

	// ListAll 返回所有已知能力（跨 discovered + published），按 source → name 排序。
	ListAll(ctx context.Context) ([]ManagedCapability, error)

	// Get 在 discovered 与 published 中按名称查找，discovered 优先。
	Get(ctx context.Context, name string) (ManagedCapability, error)

	// SaveDraft 写入/覆盖草稿（discovered），返回写入后的 ManagedCapability。
	SaveDraft(ctx context.Context, capability Capability) (ManagedCapability, error)

	// SavePublished 直接写入已发布能力（published），不删草稿。QuickPublish 用。
	SavePublished(ctx context.Context, capability Capability) (ManagedCapability, error)

	// Has 判断某个 source 下是否存在同名能力（用于冲突检查，不返回内容）。
	Has(ctx context.Context, source string, name string) (bool, error)

	// MoveDraftToPublished 发布：将 discovered/<name> 移到 published/<name>。
	// 如果草稿不存在返回 ErrCapabilityNotFound；如果 published 已存在同名返回冲突。
	MoveDraftToPublished(ctx context.Context, name string) (ManagedCapability, error)

	// MovePublishedToDraft 下架：将 published/<name> 移到 discovered/<name>。
	// 如果发布版不存在返回 ErrCapabilityNotFound；如果 discovered 已存在同名返回冲突。
	MovePublishedToDraft(ctx context.Context, name string) (ManagedCapability, error)

	// DeleteDraft 删除草稿（discovered）能力，用于清理误导入/作废的候选。
	// 已发布能力不能删，需先下架。
	DeleteDraft(ctx context.Context, name string) error
}
