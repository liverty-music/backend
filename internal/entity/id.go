package entity

import "github.com/google/uuid"

// newID generates a new UUIDv7 string for use as a primary key.
// It panics only if the underlying entropy source fails, which is treated
// as a non-recoverable runtime error.
func newID() string {
	return uuid.Must(uuid.NewV7()).String()
}
