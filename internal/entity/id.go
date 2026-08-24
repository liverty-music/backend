package entity

import "uuid"

// NewID generates a new UUIDv7 string for use as a primary key.
//
// It is the single ID-minting site for the whole backend: repositories, use
// cases, and messaging all route through here so the UUID version and string
// format stay uniform. The standard-library uuid.NewV7 returns no error — it
// panics internally only if the process entropy source fails, which is treated
// as a non-recoverable runtime error.
func NewID() string {
	return uuid.NewV7().String()
}
