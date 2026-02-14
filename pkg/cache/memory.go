// Package cache provides in-memory caching utilities with TTL support.
package cache

import (
	"sync"
	"time"
)

// entry represents a cached value with expiration metadata.
type entry struct {
	value      interface{}
	expiration time.Time
}

// MemoryCache is a thread-safe in-memory cache with TTL support.
type MemoryCache struct {
	mu      sync.RWMutex
	entries map[string]entry
	ttl     time.Duration
}

// NewMemoryCache creates a new in-memory cache with the specified TTL.
func NewMemoryCache(ttl time.Duration) *MemoryCache {
	return &MemoryCache{
		entries: make(map[string]entry),
		ttl:     ttl,
	}
}

// Get retrieves a value from the cache. Returns nil if not found or expired.
func (c *MemoryCache) Get(key string) interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	e, ok := c.entries[key]
	if !ok {
		return nil
	}

	if time.Now().After(e.expiration) {
		return nil
	}

	return e.value
}

// Set stores a value in the cache with the configured TTL.
func (c *MemoryCache) Set(key string, value interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[key] = entry{
		value:      value,
		expiration: time.Now().Add(c.ttl),
	}
}

// Delete removes a value from the cache.
func (c *MemoryCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, key)
}

// Clear removes all entries from the cache.
func (c *MemoryCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries = make(map[string]entry)
}

// Cleanup removes expired entries from the cache.
// Should be called periodically via a background goroutine.
func (c *MemoryCache) Cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for key, e := range c.entries {
		if now.After(e.expiration) {
			delete(c.entries, key)
		}
	}
}
