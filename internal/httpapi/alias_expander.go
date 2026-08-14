package httpapi

import (
	"context"
	"sync"
	"time"

	"github.com/gracegaoya/ai-operations-copilot/internal/store"
)

// AliasExpander expands canonical environment identifiers with their aliases.
// For example, given ["prod"] it may return ["prod", "生产", "ecommerce-prod"]
// so that policy checks accept any name the user might type.
//
// A nil AliasExpander is a no-op: Expand returns the input unchanged.
type AliasExpander interface {
	Expand(ctx context.Context, envs []string) []string
}

// cachedAliasExpander caches aliases in memory and refreshes periodically
// from the store. This avoids a database query on every authentication.
type cachedAliasExpander struct {
	store    store.EnvironmentAliasStore
	mu       sync.RWMutex
	aliases  map[string][]string // canonical env -> aliases
	lastLoad time.Time
	ttl      time.Duration
}

// NewCachedAliasExpander returns an AliasExpander that loads aliases from
// store and caches them for ttl. The first call to Expand triggers a load;
// subsequent calls use the cache until ttl expires.
func NewCachedAliasExpander(s store.EnvironmentAliasStore, ttl time.Duration) AliasExpander {
	if s == nil {
		return nil
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &cachedAliasExpander{store: s, ttl: ttl}
}

func (e *cachedAliasExpander) Expand(ctx context.Context, envs []string) []string {
	if e == nil || len(envs) == 0 {
		return envs
	}
	e.refreshIfNeeded(ctx)

	e.mu.RLock()
	defer e.mu.RUnlock()

	result := make([]string, 0, len(envs)*2)
	seen := make(map[string]struct{}, len(envs)*2)
	for _, env := range envs {
		if _, ok := seen[env]; !ok {
			result = append(result, env)
			seen[env] = struct{}{}
		}
		for _, alias := range e.aliases[env] {
			if _, ok := seen[alias]; !ok {
				result = append(result, alias)
				seen[alias] = struct{}{}
			}
		}
	}
	return result
}

func (e *cachedAliasExpander) refreshIfNeeded(ctx context.Context) {
	e.mu.RLock()
	fresh := time.Since(e.lastLoad) < e.ttl
	e.mu.RUnlock()
	if fresh {
		return
	}

	aliases, err := e.store.ListAliases(ctx, nil)
	if err != nil {
		return // keep stale cache; don't block auth on DB errors
	}

	m := make(map[string][]string, len(aliases))
	for _, a := range aliases {
		m[a.Environment] = append(m[a.Environment], a.Alias)
	}

	e.mu.Lock()
	e.aliases = m
	e.lastLoad = time.Now()
	e.mu.Unlock()
}
