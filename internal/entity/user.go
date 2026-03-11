package entity

import (
	"context"
	"fmt"
	"regexp"
)

// countryCodeRe validates ISO 3166-1 alpha-2 country codes (e.g., "JP", "US").
var countryCodeRe = regexp.MustCompile(`^[A-Z]{2}$`)

// iso31662Re validates ISO 3166-2 subdivision codes (e.g., "JP-13", "US-CA").
var iso31662Re = regexp.MustCompile(`^[A-Z]{2}-[A-Z0-9]{1,3}$`)

// Validate checks that the Home has a valid CountryCode, Level1, and optional Level2.
// It returns a non-nil error describing the first validation failure.
// The entity layer returns stdlib errors; callers in the usecase layer are responsible
// for wrapping them with the appropriate apperr code.
func (h *Home) Validate() error {
	if !countryCodeRe.MatchString(h.CountryCode) {
		return fmt.Errorf("country_code must be a valid ISO 3166-1 alpha-2 code (e.g., JP), got %q", h.CountryCode)
	}
	if !iso31662Re.MatchString(h.Level1) {
		return fmt.Errorf("level_1 must be a valid ISO 3166-2 code (e.g., JP-13), got %q", h.Level1)
	}
	if h.Level1[:2] != h.CountryCode {
		return fmt.Errorf("level_1 prefix %q does not match country_code %q", h.Level1[:2], h.CountryCode)
	}
	if h.Level2 != nil && (len(*h.Level2) == 0 || len(*h.Level2) > 20) {
		return fmt.Errorf("level_2 must be between 1 and 20 characters when provided, got length %d", len(*h.Level2))
	}
	return nil
}

// Home represents the user's home area as a structured geographic location.
//
// Corresponds to liverty_music.entity.v1.Home.
//
// The home area determines proximity classification:
//   - HOME: event venue matches the user's home at the applicable level.
//   - NEARBY: event is within 200km of the user's home centroid.
//   - AWAY: event is beyond 200km, has no known area, or the user has no home set.
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
	// Centroid is the approximate geographic center of the home area.
	// Resolved at write time from the Level1 code.
	// Nil when the country is unsupported and no centroid could be resolved.
	Centroid *Coordinates
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
	// Determines proximity classification (home/nearby/away).
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

// CreateUser creates a new User with an auto-generated UUIDv7 ID from the given parameters.
func CreateUser(params *NewUser) *User {
	return &User{
		ID:                newID(),
		ExternalID:        params.ExternalID,
		Email:             params.Email,
		Name:              params.Name,
		PreferredLanguage: params.PreferredLanguage,
		Country:           params.Country,
		TimeZone:          params.TimeZone,
		IsActive:          true,
	}
}

// NewHome creates a new Home with an auto-generated UUIDv7 ID.
func NewHome(countryCode, level1 string, level2 *string) *Home {
	return &Home{
		ID:          newID(),
		CountryCode: countryCode,
		Level1:      level1,
		Level2:      level2,
	}
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
