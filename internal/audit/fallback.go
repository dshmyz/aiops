package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FallbackConfig 配置 audit.Service 的防丢失兜底机制（R6 事件准 - 防丢失）。
// DB 写入失败时按退避重试，重试全失败则把事件序列化到本地 JSON 文件，
// 后台 goroutine 周期性把积压事件重放回 DB，DB 恢复后自动补齐。
// 进程崩溃也不丢：启动时立即扫描 fallback 目录重放上次积压的事件。
type FallbackConfig struct {
	// Dir 是 fallback 文件存放目录。空值用默认 "data/audit-fallback"。
	Dir string
	// MaxRetries 是同步重试次数（不含首次尝试）。0 表示只尝试一次不重试。
	// 默认 3。
	MaxRetries int
	// InitialBackoff 是首次重试前的等待时长。默认 100ms。
	InitialBackoff time.Duration
	// MaxBackoff 是退避上限。默认 1s。
	MaxBackoff time.Duration
	// ReplayInterval 是后台重放周期。默认 30s。
	// 启动时会立即重放一次，不等首个周期。
	ReplayInterval time.Duration
}

// DefaultFallbackConfig 返回生产可用的默认配置。
func DefaultFallbackConfig() FallbackConfig {
	return FallbackConfig{
		Dir:            "data/audit-fallback",
		MaxRetries:     3,
		InitialBackoff: 100 * time.Millisecond,
		MaxBackoff:     1 * time.Second,
		ReplayInterval: 30 * time.Second,
	}
}

// withDefaults 把零值字段替换为默认值，返回规范化后的配置。
func (c FallbackConfig) withDefaults() FallbackConfig {
	out := c
	if out.Dir == "" {
		out.Dir = DefaultFallbackConfig().Dir
	}
	if out.MaxRetries == 0 {
		out.MaxRetries = DefaultFallbackConfig().MaxRetries
	}
	if out.InitialBackoff == 0 {
		out.InitialBackoff = DefaultFallbackConfig().InitialBackoff
	}
	if out.MaxBackoff == 0 {
		out.MaxBackoff = DefaultFallbackConfig().MaxBackoff
	}
	if out.ReplayInterval == 0 {
		out.ReplayInterval = DefaultFallbackConfig().ReplayInterval
	}
	return out
}

// fallbackState 持有兜底机制的运行时状态。
type fallbackState struct {
	cfg   FallbackConfig
	clock func() time.Time
}

// appendWithRetry 调用 AppendAudit，失败按指数退避重试 MaxRetries 次。
// 首次尝试不计入重试次数。返回 nil 表示写入成功，返回最后一次错误表示全部失败。
// ctx 取消时立即返回 ctx.Err()。
func (s *Service) appendWithRetry(ctx context.Context, event Event) error {
	var lastErr error
	backoff := s.fallback.cfg.InitialBackoff
	for attempt := 0; attempt <= s.fallback.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > s.fallback.cfg.MaxBackoff {
				backoff = s.fallback.cfg.MaxBackoff
			}
		}
		err := s.store.AppendAudit(ctx, event)
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return lastErr
}

// writeFallbackFile 把事件序列化写入 <dir>/<event.ID>.json。
// 目录不存在时自动创建。返回 nil 表示落盘成功，事件不会丢失。
func writeFallbackFile(dir string, event Event) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir fallback dir: %w", err)
	}
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	path := filepath.Join(dir, event.ID+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write fallback file: %w", err)
	}
	return nil
}

// startReplayLoop 启动后台 goroutine 周期性重放 fallback 目录中的积压事件。
// 启动时立即重放一次（覆盖进程重启场景），之后按 ReplayInterval 周期重放。
// goroutine 在 ctx 取消时退出，由 Service.Close 通过 wg 等待退出。
func (s *Service) startReplayLoop(ctx context.Context) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		// 启动时立即重放一次，把上次进程崩溃前积压的事件补齐。
		s.replayOnce(ctx)
		ticker := time.NewTicker(s.fallback.cfg.ReplayInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.replayOnce(ctx)
			}
		}
	}()
}

// replayOnce 扫描 fallback 目录，逐个把 .json 文件中的事件重放回 DB。
// 单个文件失败（DB 仍不可用）不影响其他文件，保留文件等下次重放。
// 目录不存在视为无积压，静默返回。
func (s *Service) replayOnce(ctx context.Context) {
	entries, err := os.ReadDir(s.fallback.cfg.Dir)
	if err != nil {
		// 目录不存在或不可读：无积压事件，静默返回。
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
		s.replayFile(ctx, filepath.Join(s.fallback.cfg.Dir, entry.Name()))
	}
}

// replayFile 读单个 fallback 文件，写回 DB，成功则删除文件。
// DB 失败时保留文件等下次重放。文件损坏（无法 unmarshal）则删除避免反复重试。
func (s *Service) replayFile(ctx context.Context, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var event Event
	if err := json.Unmarshal(data, &event); err != nil {
		// 损坏的 fallback 文件无法恢复，删除避免反复重试占用资源。
		_ = os.Remove(path)
		return
	}
	if err := s.store.AppendAudit(ctx, event); err != nil {
		// DB 仍不可用，保留文件等下次重放。
		return
	}
	_ = os.Remove(path)
}
