package entity

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"unicode/utf8"
)

// UserID is the opaque identifier for a fan account, as referenced by the
// ticketing records (lottery entries, checkouts, orders and tickets).
//
// Mirrors liverty_music.entity.v1.UserId.value (a UUID string).
type UserID string

// e164Re matches a phone number in E.164 form: "+" then 2 to 15 digits, the
// first not 0. Mirrors the protovalidate pattern on PhoneNumber and the CHECK
// constraints in storage.
var e164Re = regexp.MustCompile(`^\+[1-9][0-9]{1,14}$`)

// IsE164 reports whether s is a phone number in E.164 form.
func IsE164(s string) bool {
	return e164Re.MatchString(s)
}

// FullNameMaxLen is the longest full name accepted, in characters.
const FullNameMaxLen = 200

// ValidatePersonalDetails reports whether a full name and a phone number
// follow the User's personal details rule: a full name, when present, is 1 to
// 200 characters, and a phone number, when present, is in E.164 form. An
// empty value is absent, which is valid. The entity layer returns stdlib
// errors; callers wrap them with an apperr code.
func ValidatePersonalDetails(fullName, phoneNumber string) error {
	if utf8.RuneCountInString(fullName) > FullNameMaxLen {
		return errors.New("full name must be 1 to 200 characters")
	}
	if phoneNumber != "" && !IsE164(phoneNumber) {
		return errors.New("phone number must be in E.164 form")
	}
	return nil
}

// countryCodeRe validates ISO 3166-1 alpha-2 country codes (e.g., "JP", "US").
var countryCodeRe = regexp.MustCompile(`^[A-Z]{2}$`)

// iso31662Re validates ISO 3166-2 subdivision codes (e.g., "JP-13", "US-CA").
var iso31662Re = regexp.MustCompile(`^[A-Z]{2}-[A-Z0-9]{1,3}$`)

// languageCodeRe validates ISO 639-1 two-letter lowercase codes (e.g., "ja", "en").
// Mirrors the protovalidate constraint on the proto wire types
// (CreateRequest.preferred_language, UpdatePreferredLanguageRequest.preferred_language,
// User.preferred_language) so the entity-level invariant matches what the wire
// accepts.
var languageCodeRe = regexp.MustCompile(`^[a-z]{2}$`)

// IsValidLanguageCode reports whether s is a syntactically valid ISO 639-1
// two-letter language code. Single source of truth for the user-language
// format check — used by the use case (authoritative gate for all callers)
// and the RPC handler (defense-in-depth at the seam). Keeping it on the
// entity package avoids duplicating the regex across layers.
func IsValidLanguageCode(s string) bool {
	return languageCodeRe.MatchString(s)
}

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
	// ProviderUserID is the identity provider's user identifier (Zitadel sub claim).
	ProviderUserID string
	// Email is the user's email address.
	Email string
	// Name is the user's display name.
	Name string
	// PreferredLanguage is the user's preferred ISO 639-1 two-letter language code
	// (e.g., "en", "ja"). Non-empty when supplied at Create; may be empty for legacy
	// rows pending backfill via UpdatePreferredLanguage.
	PreferredLanguage string
	// TimeZone is the user's preferred time zone.
	TimeZone string
	// Home is the user's home area. Nil when not set.
	// Determines proximity classification (home/nearby/away).
	Home *Home
	// FullName is the user's legal name as on their ID, the 本人確認 (identity
	// check) name shown on the face of their tickets; 1 to 200 characters, or
	// empty until first given.
	FullName string
	// PhoneNumber is the user's contact phone number in E.164 form (e.g.
	// "+819012345678"), shown on the face of their tickets; empty until first
	// given.
	PhoneNumber string
}

// ValidatePersonalDetails reports whether the user's full name and phone
// number follow the personal details rule ([ValidatePersonalDetails]).
func (u *User) ValidatePersonalDetails() error {
	return ValidatePersonalDetails(u.FullName, u.PhoneNumber)
}

// HasPersonalDetails reports whether the user has given both a full name and
// a phone number.
func (u *User) HasPersonalDetails() bool {
	return u.FullName != "" && u.PhoneNumber != ""
}

// NewUser represents data for creating a new user.
type NewUser struct {
	// ProviderUserID is the identity provider's user identifier (Zitadel sub claim).
	ProviderUserID string
	// Email is the user's email address.
	Email string
	// Name is the user's display name.
	Name string
	// PreferredLanguage is the user's preferred ISO 639-1 two-letter language code
	// (e.g., "en", "ja"). Non-empty when supplied at Create; may be empty for legacy
	// rows pending backfill via UpdatePreferredLanguage.
	PreferredLanguage string
	// TimeZone is the user's preferred time zone.
	TimeZone string
	// Home is the user's home area. Nil when not provided during creation.
	Home *Home
}

// CreateUser creates a new User with an auto-generated UUIDv7 ID from the given parameters.
func CreateUser(params *NewUser) *User {
	return &User{
		ID:                NewID(),
		ProviderUserID:    params.ProviderUserID,
		Email:             params.Email,
		Name:              params.Name,
		PreferredLanguage: params.PreferredLanguage,
		TimeZone:          params.TimeZone,
	}
}

// NewHome creates a new Home with an auto-generated UUIDv7 ID.
func NewHome(countryCode, level1 string, level2 *string) *Home {
	return &Home{
		ID:          NewID(),
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

	// GetByProviderUserID retrieves a user by identity provider ID.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	GetByProviderUserID(ctx context.Context, externalID string) (*User, error)

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

	// UpdatePreferredLanguage sets the user's preferred display language.
	// Performs a focused UPDATE on the preferred_language column and returns
	// the refreshed user entity.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	UpdatePreferredLanguage(ctx context.Context, id, lang string) (*User, error)

	// UpdateHome sets or changes the user's home area.
	// Creates or updates the associated home record.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	UpdateHome(ctx context.Context, id string, home *Home) (*User, error)

	// UpdatePersonalDetails sets the user's full name and phone number to the
	// given values (an empty value stores it as absent), leaves the user's
	// other attributes unchanged and returns the updated user. Repeating it
	// with the same values succeeds without a further change.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the values break the personal details rule.
	//  - NotFound: If the user does not exist.
	UpdatePersonalDetails(ctx context.Context, id string, fullName, phoneNumber string) (*User, error)

	// List retrieves users with pagination.
	List(ctx context.Context, limit, offset int) ([]*User, error)
}

// EmailVerifier sends and resends email verification codes via the identity
// provider. Implementations call the Zitadel Management API.
type EmailVerifier interface {
	// SendVerification triggers a verification email for the given Zitadel user.
	//
	// # Possible errors
	//
	//  - Unavailable: The Zitadel API client is not configured.
	SendVerification(ctx context.Context, externalID string) error

	// ResendVerification resends a verification email for the given Zitadel user.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: The user's email is already verified.
	//  - Unavailable: The Zitadel API client is not configured.
	ResendVerification(ctx context.Context, externalID string) error
}

// IdentityRemover removes a User's sign-in identity at the identity provider.
// Implementations call the Zitadel API.
type IdentityRemover interface {
	// DeleteIdentity removes the identity-provider user identified by
	// externalID (the Zitadel subject claim), so it can no longer sign in. An
	// identity that no longer exists counts as removed.
	//
	// # Possible errors
	//
	//  - Internal: the identity provider failed to remove the identity.
	DeleteIdentity(ctx context.Context, externalID string) error
}
