package cache

import (
	"testing"
	"time"
)

func TestMemoryCache_SetAndGet(t *testing.T) {
	cache := NewMemoryCache(1 * time.Hour)

	// Test basic set and get
	cache.Set("key1", "value1")
	if got := cache.Get("key1"); got != "value1" {
		t.Errorf("Get() = %v, want %v", got, "value1")
	}

	// Test non-existent key
	if got := cache.Get("nonexistent"); got != nil {
		t.Errorf("Get() = %v, want nil", got)
	}
}

func TestMemoryCache_Expiration(t *testing.T) {
	cache := NewMemoryCache(100 * time.Millisecond)

	cache.Set("key1", "value1")

	// Value should be available immediately
	if got := cache.Get("key1"); got != "value1" {
		t.Errorf("Get() = %v, want %v", got, "value1")
	}

	// Wait for expiration
	time.Sleep(150 * time.Millisecond)

	// Value should be expired
	if got := cache.Get("key1"); got != nil {
		t.Errorf("Get() after expiration = %v, want nil", got)
	}
}

func TestMemoryCache_Delete(t *testing.T) {
	cache := NewMemoryCache(1 * time.Hour)

	cache.Set("key1", "value1")
	cache.Delete("key1")

	if got := cache.Get("key1"); got != nil {
		t.Errorf("Get() after Delete() = %v, want nil", got)
	}
}

func TestMemoryCache_Clear(t *testing.T) {
	cache := NewMemoryCache(1 * time.Hour)

	cache.Set("key1", "value1")
	cache.Set("key2", "value2")
	cache.Clear()

	if got := cache.Get("key1"); got != nil {
		t.Errorf("Get() after Clear() = %v, want nil", got)
	}
	if got := cache.Get("key2"); got != nil {
		t.Errorf("Get() after Clear() = %v, want nil", got)
	}
}

func TestMemoryCache_Cleanup(t *testing.T) {
	cache := NewMemoryCache(100 * time.Millisecond)

	cache.Set("key1", "value1")
	cache.Set("key2", "value2")

	// Wait for expiration
	time.Sleep(150 * time.Millisecond)

	// Add a fresh entry
	cache.Set("key3", "value3")

	// Run cleanup
	cache.Cleanup()

	// Expired entries should be removed
	if got := cache.Get("key1"); got != nil {
		t.Errorf("Get() after Cleanup() = %v, want nil", got)
	}
	if got := cache.Get("key2"); got != nil {
		t.Errorf("Get() after Cleanup() = %v, want nil", got)
	}

	// Fresh entry should still exist
	if got := cache.Get("key3"); got != "value3" {
		t.Errorf("Get() after Cleanup() = %v, want %v", got, "value3")
	}
}

func TestMemoryCache_Concurrent(t *testing.T) {
	cache := NewMemoryCache(1 * time.Hour)

	// Test concurrent reads and writes
	done := make(chan bool)

	// Writer goroutines
	for i := 0; i < 10; i++ {
		go func(val int) {
			cache.Set("key", val)
			done <- true
		}(i)
	}

	// Reader goroutines
	for i := 0; i < 10; i++ {
		go func() {
			_ = cache.Get("key")
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 20; i++ {
		<-done
	}

	// Should not panic and should have a value
	if got := cache.Get("key"); got == nil {
		t.Error("Get() after concurrent access = nil, want non-nil")
	}
}
