package entity

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"
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
	// Email is the user's email address, at most 165 characters.
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
	// Email is the user's email address, at most 165 characters.
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

// ValidateEmail reports whether the new user's email is at most
// [EmailMaxLen] characters. The email comes from the identity provider's
// verified claims, so its form is not checked again here.
func (n *NewUser) ValidateEmail() error {
	if utf8.RuneCountInString(n.Email) > EmailMaxLen {
		return errors.New("email must be at most 165 characters")
	}
	return nil
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
	GetByProviderUserID(ctx context.Context, providerUserID string) (*User, error)

	// GetByEmail retrieves the user whose email equals the given address when
	// letter case is ignored.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the address is empty.
	//  - NotFound: If no user has the address.
	//  - Internal: If more than one user has the address once letter case is
	//    ignored.
	GetByEmail(ctx context.Context, email string) (*User, error)

	// Delete removes a user.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	Delete(ctx context.Context, id string) error

	// SerializeDeletion runs fn while holding an exclusive lock keyed on the
	// user. LotteryEntryRepository.Create, CheckoutRepository.GetOrCreateHeld
	// and ResaleListingRepository.Create hold the same key shared while they
	// store, and store nothing once the user is gone, so a deletion that
	// checks what blocks it inside fn never misses a record created
	// concurrently. The lock is released when fn returns, and also when the
	// process holding it dies.
	//
	// # Possible errors
	//
	//  - Internal: the lock could not be taken.
	//  - Any error returned by fn, unchanged.
	SerializeDeletion(ctx context.Context, id string, fn func(ctx context.Context) error) error

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
}

// EmailVerifier sends a User's verification email (User.SendVerification and
// User.ResendVerification). The email is written in the User's preferred
// language (Japanese when none), is sent in the name of Liverty Music and
// links to the fan web's email verification screen with the User's id and a
// fresh code, valid for 1 hour.
type EmailVerifier interface {
	// SendVerification sends the verification email at sign-up.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the User's email is already verified; nothing is
	//    sent.
	//  - Internal: the code cannot be issued or the email cannot be sent.
	SendVerification(ctx context.Context, user *User) error

	// ResendVerification sends a verification email with a fresh code, even
	// when no earlier code was issued. Earlier codes stop working.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the User's email is already verified; nothing is
	//    sent.
	//  - Internal: any other failure to issue or send.
	ResendVerification(ctx context.Context, user *User) error
}

// EmailCodeVerifier confirms a User's email address at the identity provider
// with the code from the User's verification email (User.VerifyEmail).
type EmailCodeVerifier interface {
	// VerifyEmail marks the address of the identity providerUserID verified
	// when code is the latest one issued for it and is less than 1 hour old.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the code is wrong, older than 1 hour or already
	//    used; nothing changes.
	//  - NotFound: no identity has the provider user ID.
	//  - Unavailable: the identity provider cannot be reached.
	VerifyEmail(ctx context.Context, providerUserID, code string) error
}

// IdentityRemover removes a User's sign-in identity at the identity provider.
// Implementations call the Zitadel API.
type IdentityRemover interface {
	// DeleteIdentity removes the identity-provider user identified by
	// providerUserID (the Zitadel subject claim), so it can no longer sign in. An
	// identity that no longer exists counts as removed.
	//
	// # Possible errors
	//
	//  - Internal: the identity provider failed to remove the identity.
	DeleteIdentity(ctx context.Context, providerUserID string) error
}

// PasskeyRegistrationLinkSender sends a User's verified email address a link
// that registers a new passkey for the User's identity on whichever device
// the link is opened (User.SendPasskeyRegistrationLink). The link stops
// working 1 hour after it is sent and once a registration has been started
// with it; the identity's existing passkeys stay registered.
type PasskeyRegistrationLinkSender interface {
	// SendPasskeyRegistrationLink sends one email, in the user's preferred
	// language (Japanese when none), to the address the user's identity has
	// verified.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: the identity's address is not verified; nothing
	//    is sent.
	//  - NotFound: the user's identity no longer exists.
	//  - Internal: any other failure to send.
	SendPasskeyRegistrationLink(ctx context.Context, user *User) error
}

// PasskeyChangeKind is the kind of change made to a User's passkeys.
type PasskeyChangeKind string

const (
	// PasskeyChangeAdded is a passkey that finished registration.
	PasskeyChangeAdded PasskeyChangeKind = "added"
	// PasskeyChangeRemoved is a passkey that was removed.
	PasskeyChangeRemoved PasskeyChangeKind = "removed"
)

// IsValid reports whether k is a known kind of passkey change.
func (k PasskeyChangeKind) IsValid() bool {
	return k == PasskeyChangeAdded || k == PasskeyChangeRemoved
}

// PasskeyChange is a passkey added to or removed from a User's identity at
// the identity provider. Passkeys are not stored by the platform; this value
// only carries what the change email shows.
type PasskeyChange struct {
	// Kind is whether the passkey was added or removed.
	Kind PasskeyChangeKind
	// Name is the passkey's name at the identity provider; empty when not
	// known (always for a removal).
	Name string
	// ChangeTime is when the change was made.
	ChangeTime time.Time
}

// PasskeyChangeEmailSender emails a User that a passkey was added to or
// removed from their account (User.SendPasskeyChangeEmail).
type PasskeyChangeEmailSender interface {
	// SendPasskeyChangeEmail sends one email to the user's email address, in
	// the user's preferred language (Japanese when none), stating the kind of
	// change, the passkey's name when it is not empty and the time of the
	// change in Japan time, and telling the reader to open the "can't sign
	// in" screen and remove unknown passkeys in Settings when they did not
	// make the change.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the user has no email address.
	//  - Unavailable: email cannot be sent.
	SendPasskeyChangeEmail(ctx context.Context, user *User, change PasskeyChange) error
}
