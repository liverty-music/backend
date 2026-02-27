// Package cache provides in-memory caching utilities with TTL support.
package cache

import (
	"context"
	"sync"
	"time"
)

// entry represents a cached value with expiration metadata.
type entry struct {
	value      interface{}
	expiration time.Time
}

// MemoryCache is a thread-safe in-memory cache with TTL support.
// A background goroutine periodically removes expired entries.
// Close stops the goroutine and blocks until it exits.
type MemoryCache struct {
	mu      sync.RWMutex
	entries map[string]entry
	ttl     time.Duration

	cancel context.CancelFunc
	done   chan struct{}
}

// NewMemoryCache creates a new in-memory cache with the specified TTL and
// starts a background goroutine that removes expired entries at an interval
// derived from the TTL (ttl / 6). Call Close to stop the goroutine.
func NewMemoryCache(ttl time.Duration) *MemoryCache {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	c := &MemoryCache{
		entries: make(map[string]entry),
		ttl:     ttl,
		cancel:  cancel,
		done:    done,
	}

	go func() {
		defer close(done)
		ticker := time.NewTicker(ttl / 6)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				c.cleanup()
			}
		}
	}()

	return c
}

// Close stops the background cleanup goroutine and waits for it to exit.
func (c *MemoryCache) Close() error {
	if c.cancel != nil {
		c.cancel()
		<-c.done
	}
	return nil
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

// cleanup removes expired entries from the cache.
func (c *MemoryCache) cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for key, e := range c.entries {
		if now.After(e.expiration) {
			delete(c.entries, key)
		}
	}
}
