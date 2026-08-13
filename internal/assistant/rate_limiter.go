package assistant

import (
	"sync"
	"time"
)

// RateLimiter 控制 LLM 调用频率，避免给远程 API 造成压力。
type RateLimiter struct {
	mu           sync.Mutex
	maxPerMinute int           // 每分钟最大请求数
	interval     time.Duration // 最小请求间隔
	lastCall     time.Time     // 已预定的最后一个时隙
	sem          chan struct{} // 并发控制
}

// NewRateLimiter 创建限流器。
// maxPerMinute: 每分钟最大请求数（0=不限）
// maxConcurrent: 最大并发数（0=不限）
func NewRateLimiter(maxPerMinute, maxConcurrent int) *RateLimiter {
	interval := time.Duration(0)
	if maxPerMinute > 0 {
		interval = time.Minute / time.Duration(maxPerMinute)
	}
	semSize := maxConcurrent
	if semSize <= 0 {
		semSize = 100 // 默认并发上限
	}
	return &RateLimiter{
		maxPerMinute: maxPerMinute,
		interval:     interval,
		sem:          make(chan struct{}, semSize),
	}
}

// Acquire 获取许可，阻塞直到可以发送请求。
// 在锁内预定时隙，保证并发下每个调用者拿到不同的时隙。
func (r *RateLimiter) Acquire() {
	r.sem <- struct{}{} // 并发控制
	if r.interval > 0 {
		r.mu.Lock()
		// 预定时隙：基于上次预定的时隙推算本次
		next := r.lastCall.Add(r.interval)
		if time.Now().After(next) {
			next = time.Now()
		}
		r.lastCall = next
		wait := time.Until(next)
		r.mu.Unlock()
		if wait > 0 {
			time.Sleep(wait)
		}
	}
}

// Release 释放并发许可。
func (r *RateLimiter) Release() {
	<-r.sem
}
