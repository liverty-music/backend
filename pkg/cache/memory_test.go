package cache_test

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/liverty-music/backend/pkg/cache"
	"github.com/stretchr/testify/assert"
)

func TestMemoryCache_SetAndGet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := cache.NewMemoryCache(1 * time.Hour)
		t.Cleanup(func() { assert.NoError(t, c.Close()) })

		c.Set("key1", "value1")
		assert.Equal(t, "value1", c.Get("key1"))
		assert.Nil(t, c.Get("nonexistent"))
	})
}

func TestMemoryCache_Expiration(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := cache.NewMemoryCache(100 * time.Millisecond)
		t.Cleanup(func() { assert.NoError(t, c.Close()) })

		c.Set("key1", "value1")
		assert.Equal(t, "value1", c.Get("key1"))

		// Advance fake clock past TTL.
		time.Sleep(150 * time.Millisecond)

		assert.Nil(t, c.Get("key1"))
	})
}

func TestMemoryCache_Delete(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := cache.NewMemoryCache(1 * time.Hour)
		t.Cleanup(func() { assert.NoError(t, c.Close()) })

		c.Set("key1", "value1")
		c.Delete("key1")

		assert.Nil(t, c.Get("key1"))
	})
}

func TestMemoryCache_GetAndDelete(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := cache.NewMemoryCache(1 * time.Hour)
		t.Cleanup(func() { assert.NoError(t, c.Close()) })

		c.Set("key1", "value1")

		// First call returns the value and removes it (single-use).
		assert.Equal(t, "value1", c.GetAndDelete("key1"))
		// Second call finds nothing — the entry was consumed.
		assert.Nil(t, c.GetAndDelete("key1"))
		assert.Nil(t, c.Get("key1"))

		// Absent key returns nil.
		assert.Nil(t, c.GetAndDelete("nonexistent"))
	})
}

func TestMemoryCache_GetAndDelete_Expired(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := cache.NewMemoryCache(1 * time.Hour)
		t.Cleanup(func() { assert.NoError(t, c.Close()) })

		c.Set("key1", "value1")
		time.Sleep(2 * time.Hour) // synctest fast-forwards; entry is now expired.

		// Expired entry returns nil (and is removed).
		assert.Nil(t, c.GetAndDelete("key1"))
	})
}

func TestMemoryCache_Clear(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := cache.NewMemoryCache(1 * time.Hour)
		t.Cleanup(func() { assert.NoError(t, c.Close()) })

		c.Set("key1", "value1")
		c.Set("key2", "value2")
		c.Clear()

		assert.Nil(t, c.Get("key1"))
		assert.Nil(t, c.Get("key2"))
	})
}

func TestMemoryCache_BackgroundCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		// TTL=60ms → cleanup interval = 60ms/6 = 10ms.
		c := cache.NewMemoryCache(60 * time.Millisecond)
		t.Cleanup(func() { assert.NoError(t, c.Close()) })

		c.Set("key1", "value1")

		// Advance past TTL + cleanup interval so the background goroutine fires.
		time.Sleep(70 * time.Millisecond)
		synctest.Wait()

		// After cleanup, set a fresh value and verify it's returned correctly.
		c.Set("key1", "value2")
		assert.Equal(t, "value2", c.Get("key1"))
	})
}

func TestMemoryCache_Close(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := cache.NewMemoryCache(1 * time.Hour)

		assert.NoError(t, c.Close())
	})
}

// TestMemoryCache_GetAndDelete_Atomicity asserts that exactly one goroutine
// wins the race when N goroutines all call GetAndDelete on the same key
// simultaneously. The single-winner property is what makes GetAndDelete safe
// for single-use nonce consumption in the Pocket Sign flow.
func TestMemoryCache_GetAndDelete_Atomicity(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const concurrency = 20
		c := cache.NewMemoryCache(1 * time.Hour)
		t.Cleanup(func() { assert.NoError(t, c.Close()) })

		c.Set("nonce", "secret-nonce-value")

		type result struct {
			got any
		}

		results := make(chan result, concurrency)
		for range concurrency {
			go func() {
				v := c.GetAndDelete("nonce")
				results <- result{got: v}
			}()
		}

		var winners int
		for range concurrency {
			r := <-results
			if r.got != nil {
				winners++
				assert.Equal(t, "secret-nonce-value", r.got,
					"the winner must receive the correct value")
			}
		}
		assert.Equal(t, 1, winners,
			"exactly one goroutine must win the GetAndDelete race; single-use semantics violated")
	})
}

// TestMemoryCache_Close_IsSafeToCallTwice asserts that calling Close a second
// time does not deadlock. The done channel is already closed after the first
// call, so a subsequent <-c.done returns immediately.
func TestMemoryCache_Close_IsSafeToCallTwice(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := cache.NewMemoryCache(1 * time.Hour)

		assert.NoError(t, c.Close(), "first Close must succeed")
		// Second call must return without hanging. If the implementation blocks
		// here, the synctest virtual clock will detect the deadlock.
		assert.NoError(t, c.Close(), "second Close must also return nil without deadlock")
	})
}

func TestMemoryCache_Concurrent(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := cache.NewMemoryCache(1 * time.Hour)
		t.Cleanup(func() { assert.NoError(t, c.Close()) })

		done := make(chan bool)

		for i := range 10 {
			go func(val int) {
				c.Set("key", val)
				done <- true
			}(i)
		}

		for range 10 {
			go func() {
				_ = c.Get("key")
				done <- true
			}()
		}

		for range 20 {
			<-done
		}

		assert.NotNil(t, c.Get("key"))
	})
}
