package entity

import "context"

// Home represents the user's home area as a structured geographic location.
//
// Corresponds to liverty_music.entity.v1.Home.
//
// The home area determines dashboard lane classification:
//   - "Home" lane: event venue matches the user's home at the applicable level.
//   - "Nearby" lane: event has a known area that differs from the user's home.
//   - "Away" lane: event has no known area or the user has no home set.
//
// Code system contract:
//   - Level1 is always an ISO 3166-2 subdivision code, worldwide.
//   - Level2 uses a country-specific standard determined by CountryCode
//     (e.g., US → FIPS county code, DE → AGS). Phase 1 always omits Level2.
type Home struct {
	// ID is the unique identifier for the home record (UUID).
	ID string
	// CountryCode is the ISO 3166-1 alpha-2 country code (e.g., "JP", "US").
	CountryCode string
	// Level1 is the ISO 3166-2 subdivision code (e.g., "JP-13", "US-NY").
	Level1 string
	// Level2 is an optional finer-grained area code within the Level1 subdivision.
	// The code system depends on CountryCode. Nil in Phase 1 (Japan-only).
	Level2 *string
}

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
	// Home is the user's home area. Nil when not set.
	// Determines dashboard lane classification (home/nearby/away).
	Home *Home
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
	// Home is the user's home area. Nil when not provided during creation.
	Home *Home
}

// UserRepository defines the interface for user data access.
type UserRepository interface {
	// Create creates a new user.
	// If params.Home is non-nil, the home area is persisted atomically.
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

	// UpdateHome sets or changes the user's home area.
	// Creates or updates the associated home record.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	UpdateHome(ctx context.Context, id string, home *Home) (*User, error)

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
