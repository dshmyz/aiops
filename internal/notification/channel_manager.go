package notification

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// Channel 是一条通知外发通道的运行态配置（DB 记录去时间戳）。
type Channel struct {
	ID      string
	Type    string // feishu | webhook
	Name    string
	URL     string
	Secret  string
	Enabled bool
}

// ChannelManager 是通知通道的热更新 holder：实现 Notifier，通道来自 DB
// （DB 为空时回退 env，保持旧部署行为）。增删改经 Upsert/Delete 写 DB 后
// 重建内部 notifier 链，无需重启即生效。
//
// 消费方（httpapi.Router 与 alert.PlanCreator）只持有 notification.Notifier
// 接口，注入本管理器即零改动获得热更新。
type ChannelManager struct {
	store store.NotificationChannelStore
	mu    sync.RWMutex
	// notifiers 在每次 reload 时按 enabled 通道重建，NotifyConfirmation 只做
	// 快照扇出，不在每次发送时分配。
	notifiers []Notifier
	channels  []Channel
}

// NewChannelManager 创建通道管理器。
func NewChannelManager(st store.NotificationChannelStore) *ChannelManager {
	return &ChannelManager{store: st}
}

// Load 从 DB 读取通道并重建 notifier 链。DB 为空时用 env 通道回退（不落库）。
func (m *ChannelManager) Load(ctx context.Context) error {
	records, err := m.store.List(ctx)
	if err != nil {
		return err
	}
	chs := make([]Channel, 0, len(records))
	for _, r := range records {
		chs = append(chs, Channel{
			ID: r.ID, Type: r.Type, Name: r.Name,
			URL: r.URL, Secret: r.Secret, Enabled: r.Enabled,
		})
	}
	if len(chs) == 0 {
		chs = seedFromEnv()
	}
	m.mu.Lock()
	m.channels = chs
	m.notifiers = buildNotifiers(chs)
	m.mu.Unlock()
	return nil
}

// Upsert 写 DB 后重建 notifier 链，返回落库记录（含分配的 ID）。
func (m *ChannelManager) Upsert(ctx context.Context, ch store.NotificationChannelRecord) (store.NotificationChannelRecord, error) {
	if ch.ID == "" {
		ch.ID = uuid.NewString()
	}
	if err := m.store.Upsert(ctx, ch); err != nil {
		return ch, err
	}
	return ch, m.Load(ctx)
}

// Delete 从 DB 删除后重建 notifier 链。
func (m *ChannelManager) Delete(ctx context.Context, id string) error {
	if err := m.store.Delete(ctx, id); err != nil {
		return err
	}
	return m.Load(ctx)
}

// List 返回当前通道快照（含 enabled 状态，供管理界面展示）。
func (m *ChannelManager) List() []Channel {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]Channel(nil), m.channels...)
}

// NotifyConfirmation 把确认请求扇出到当前所有 enabled 通道，日志通知恒兜底。
func (m *ChannelManager) NotifyConfirmation(ctx context.Context, req ConfirmationRequest) error {
	m.mu.RLock()
	notifiers := append([]Notifier(nil), m.notifiers...)
	m.mu.RUnlock()
	if len(notifiers) == 0 {
		return nil
	}
	return NewMultiNotifier(notifiers...).NotifyConfirmation(ctx, req)
}

// buildNotifiers 按 enabled 通道构建通知器，日志通知恒附加为兜底。
func buildNotifiers(channels []Channel) []Notifier {
	var notifiers []Notifier
	for _, ch := range channels {
		if !ch.Enabled || ch.URL == "" {
			continue
		}
		switch ch.Type {
		case "feishu":
			notifiers = append(notifiers, NewFeishuNotifier(ch.URL))
		case "webhook":
			notifiers = append(notifiers, NewWebhookNotifier(ch.URL, ch.Secret))
		}
	}
	notifiers = append(notifiers, NewLogNotifier())
	return notifiers
}

// seedFromEnv 在 DB 无通道时回退到旧 env 配置，保证现有部署行为不变。
func seedFromEnv() []Channel {
	var chs []Channel
	if url := strings.TrimSpace(os.Getenv("COPILOT_FEISHU_WEBHOOK_URL")); url != "" {
		chs = append(chs, Channel{ID: "env-feishu", Type: "feishu", Name: "飞书", URL: url, Enabled: true})
	}
	if url := strings.TrimSpace(os.Getenv("COPILOT_WEBHOOK_URL")); url != "" {
		chs = append(chs, Channel{
			ID: "env-webhook", Type: "webhook", Name: "通用 Webhook",
			URL: url, Secret: strings.TrimSpace(os.Getenv("COPILOT_WEBHOOK_SECRET")), Enabled: true,
		})
	}
	return chs
}
