package entity

import (
	"context"
	"time"
)

// User represents a user domain entity.
type User struct {
	ID        string
	Name      string
	Email     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewUser represents data for creating a new user.
type NewUser struct {
	Name  string
	Email string
}

// UserRepository defines the interface for user data access.
type UserRepository interface {
	Create(ctx context.Context, params *NewUser) (*User, error)
	Get(ctx context.Context, id string) (*User, error)
	Delete(ctx context.Context, id string) error
}