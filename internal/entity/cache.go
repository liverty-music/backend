package entity

// Cache provides key-value storage with automatic expiration.
// Implementations handle TTL, thread safety, and cleanup.
type Cache interface {
	// Get retrieves a value by key. Returns nil if not found or expired.
	Get(key string) any
	// Set stores a value with the implementation's configured TTL.
	Set(key string, value any)
}
