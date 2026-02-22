package entity

import (
	"context"
	"time"
)

// User represents a user who registers for concert notifications.
//
// Corresponds to liverty_music.entity.v1.User.
type User struct {
	// ID is the unique identifier for the user (UUID).
	ID string
	// ExternalID is the identity provider's user identifier (Zitadel sub claim).
	ExternalID string
	// Email is the user's email address.
	Email string
	// Name is the user's display name.
	Name string
	// PreferredLanguage is the user's preferred language code (e.g., "en", "ja").
	PreferredLanguage string
	// Country is the user's country code.
	Country string
	// TimeZone is the user's preferred time zone.
	TimeZone string
	// SafeAddress is the predicted Safe (ERC-4337) address derived from the user's ID.
	// Empty until lazily computed on first ticket mint.
	SafeAddress string
	// IsActive indicates if the user account is active.
	IsActive bool
	// CreateTime is the timestamp when the user was created.
	CreateTime time.Time
	// UpdateTime is the timestamp when the user was last updated.
	UpdateTime time.Time
}

// NewUser represents data for creating a new user.
type NewUser struct {
	// ExternalID is the identity provider's user identifier (Zitadel sub claim).
	ExternalID string
	// Email is the user's email address.
	Email string
	// Name is the user's display name.
	Name string
	// PreferredLanguage is the user's preferred language code.
	PreferredLanguage string
	// Country is the user's country code.
	Country string
	// TimeZone is the user's preferred time zone.
	TimeZone string
}

// UserRepository defines the interface for user data access.
type UserRepository interface {
	// Create creates a new user.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If email or name is invalid.
	//  - AlreadyExists: If a user with the same email already exists.
	Create(ctx context.Context, params *NewUser) (*User, error)

	// Get retrieves a user by ID.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	Get(ctx context.Context, id string) (*User, error)

	// GetByExternalID retrieves a user by identity provider ID.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	GetByExternalID(ctx context.Context, externalID string) (*User, error)

	// GetByEmail retrieves a user by email address.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	GetByEmail(ctx context.Context, email string) (*User, error)

	// Update updates user information.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	Update(ctx context.Context, id string, params *NewUser) (*User, error)

	// Delete removes a user.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	Delete(ctx context.Context, id string) error

	// UpdateSafeAddress sets the predicted Safe address for a user.
	// This is called lazily on first ticket mint.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	UpdateSafeAddress(ctx context.Context, id, safeAddress string) error

	// List retrieves users with pagination.
	List(ctx context.Context, limit, offset int) ([]*User, error)
}
