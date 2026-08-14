package assistant

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sync"
	"time"
)

// ResponseCache 缓存 LLM 响应，避免重复调用。
type ResponseCache struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry
	maxSize int
	ttl     time.Duration
}

type cacheEntry struct {
	response  *AgentRunResult
	createdAt time.Time
}

func NewResponseCache(maxSize int, ttl time.Duration) *ResponseCache {
	if maxSize <= 0 {
		maxSize = 100
	}
	if ttl <= 0 {
		ttl = 30 * time.Minute
	}
	return &ResponseCache{
		entries: make(map[string]cacheEntry, maxSize),
		maxSize: maxSize,
		ttl:     ttl,
	}
}

// Get 从缓存获取响应。miss 返回 nil。
// 缓存 key 同时包含请求者身份：同一 query 在不同用户之间不共享，
// 防止 B 用户命中 A 用户（以 admin 权限采集）的结果。
func (c *ResponseCache) Get(ctx context.Context, query string) *AgentRunResult {
	key := cacheKey(subjectFromCtx(ctx), query)
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || time.Since(entry.createdAt) > c.ttl {
		return nil
	}
	return entry.response
}

// Set 存入缓存。key 同样按请求者身份隔离。
func (c *ResponseCache) Set(ctx context.Context, query string, result *AgentRunResult) {
	key := cacheKey(subjectFromCtx(ctx), query)
	c.mu.Lock()
	defer c.mu.Unlock()
	// 简单淘汰：超过 maxSize 时清空
	if len(c.entries) >= c.maxSize {
		c.entries = make(map[string]cacheEntry, c.maxSize)
	}
	c.entries[key] = cacheEntry{
		response:  result,
		createdAt: time.Now(),
	}
}

// Invalidate 使缓存失效。
func (c *ResponseCache) Invalidate(ctx context.Context, query string) {
	key := cacheKey(subjectFromCtx(ctx), query)
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

// cacheKey 生成缓存键：请求者身份 + query 的 hash。
func cacheKey(subject, query string) string {
	h := sha256.Sum256([]byte(subject + "\x00" + query))
	return fmt.Sprintf("%x", h[:16])
}

// subjectFromCtx 取请求者标识做缓存隔离维度；无身份时用固定占位符，
// 使系统/内部发起的请求（如定时诊断）仍能互相去重，同时不与其他用户混淆。
func subjectFromCtx(ctx context.Context) string {
	if user, ok := toolUserFromContext(ctx); ok && user.Subject != "" {
		return user.Subject
	}
	return "system"
}
