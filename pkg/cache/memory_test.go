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
