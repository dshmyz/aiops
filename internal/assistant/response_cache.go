package assistant

import (
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
func (c *ResponseCache) Get(query string) *AgentRunResult {
	key := cacheKey(query)
	c.mu.RLock()
	entry, ok := c.entries[key]
	c.mu.RUnlock()
	if !ok || time.Since(entry.createdAt) > c.ttl {
		return nil
	}
	return entry.response
}

// Set 存入缓存。
func (c *ResponseCache) Set(query string, result *AgentRunResult) {
	key := cacheKey(query)
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
func (c *ResponseCache) Invalidate(query string) {
	key := cacheKey(query)
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

// cacheKey 生成缓存键（query 的 hash）。
func cacheKey(query string) string {
	h := sha256.Sum256([]byte(query))
	return fmt.Sprintf("%x", h[:16])
}
