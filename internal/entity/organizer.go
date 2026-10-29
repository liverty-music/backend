package entity

import (
	"context"
	"errors"
	"net/mail"
	"regexp"
	"slices"
	"unicode/utf8"
)

// OrganizerStatus is the operational provisioning lifecycle of an Organizer.
// Admin reads expose it; fan-facing reads never do. It is distinct from any
// business vetting flag: existence is the vetting.
type OrganizerStatus int16

const (
	// OrganizerStatusUnspecified is the zero value and is never persisted.
	OrganizerStatusUnspecified OrganizerStatus = 0
	// OrganizerStatusProvisioning means Organization provisioning is in progress and the
	// Organizer is not yet fully usable.
	OrganizerStatusProvisioning OrganizerStatus = 1
	// OrganizerStatusActive means provisioning completed and the Organizer is usable.
	OrganizerStatusActive OrganizerStatus = 2
	// OrganizerStatusDeactivated means the Organizer is turned off: its operations
	// are rejected and its artist associations have been freed.
	OrganizerStatusDeactivated OrganizerStatus = 3
)

// Organizer is a vetted business — a record label, management/agency, promoter, or
// a self-publishing artist — that an admin creates to represent artists. The
// people who run it sign in through its own Organization at the identity
// provider, named by its short name. Existence is the vetting; there is no
// separate verified flag.
type Organizer struct {
	ID   string
	Name string
	// ShortName names the Organizer's Organization at the identity provider
	// and completes its identities' user names. It is unique and never
	// changes. Empty only for an Organizer created before short names existed,
	// until it is given one.
	ShortName string
	// OwnerEmail is the email of the person who owns the Organizer, captured at
	// creation and persisted so the reconciler can complete provisioning after
	// a partial failure. It is not exposed on any proto.
	OwnerEmail          string
	ProviderOrganizerID string // empty until provisioning records the Organization link
	Status              OrganizerStatus
	// BusinessDetails is the 特商法 販売業者 disclosure entered by an admin at
	// vetting; nil until entered.
	BusinessDetails *BusinessDetails
	// PlatformFeeRateBps is the platform fee applied to the Organizer's future
	// Orders, in basis points (0 to 3000).
	PlatformFeeRateBps int
}

const (
	// DefaultPlatformFeeRateBps is a new Organizer's platform fee rate: 8%.
	DefaultPlatformFeeRateBps = 800
	// MaxPlatformFeeRateBps is the highest platform fee rate allowed: 30%.
	MaxPlatformFeeRateBps = 3000
)

// NewOrganizer creates a new Organizer in the provisioning state with a generated
// UUIDv7 id and the default platform fee rate. It does not validate its
// arguments; see [Organizer.Validate].
func NewOrganizer(name, shortName, ownerEmail string) *Organizer {
	return &Organizer{
		ID:                 NewID(),
		Name:               name,
		ShortName:          shortName,
		OwnerEmail:         ownerEmail,
		Status:             OrganizerStatusProvisioning,
		PlatformFeeRateBps: DefaultPlatformFeeRateBps,
	}
}

// EmailMaxLen is the longest email accepted anywhere, in characters, so that
// any address can become an Organizer identity's user name
// "<email> (<short name>)" within the identity provider's 200 characters.
// Mirrors max_len on liverty_music.entity.v1.Email.
const EmailMaxLen = 165

// ValidateEmail reports whether s is a bare email address of at most
// [EmailMaxLen] characters.
func ValidateEmail(s string) error {
	if addr, err := mail.ParseAddress(s); err != nil || addr.Address != s {
		return errors.New("must be an email address")
	}
	if utf8.RuneCountInString(s) > EmailMaxLen {
		return errors.New("must be at most 165 characters")
	}
	return nil
}

// shortNameRe matches a short name: 3 to 32 lowercase letters, digits and
// hyphens, starting with a letter. Mirrors the protovalidate pattern on
// liverty_music.entity.v1.OrganizerShortName and the CHECK constraint in
// storage.
var shortNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{2,31}$`)

// reservedShortNames are the names of the product and admin Organizations,
// which no Organizer may take.
var reservedShortNames = []string{"admin", "liverty-music"}

// ValidateShortName reports whether s is a valid, unreserved short name.
func ValidateShortName(s string) error {
	if !shortNameRe.MatchString(s) {
		return errors.New("short name must be 3 to 32 lowercase letters, digits and hyphens, starting with a letter")
	}
	if slices.Contains(reservedShortNames, s) {
		return errors.New("short name is reserved")
	}
	return nil
}

// Validate reports whether the Organizer's name is 1 to 200 characters, its
// short name is valid and unreserved, and its owner email is an email
// address of at most 165 characters.
func (o *Organizer) Validate() error {
	if !lenBetween(o.Name, 1, 200) {
		return errors.New("name must be 1 to 200 characters")
	}
	if err := ValidateShortName(o.ShortName); err != nil {
		return err
	}
	if err := ValidateEmail(o.OwnerEmail); err != nil {
		return errors.New("owner email " + err.Error())
	}
	return nil
}

// HasCompleteBusinessDetails reports whether the Organizer has business
// details with all five values present and valid.
func (o *Organizer) HasCompleteBusinessDetails() bool {
	return o.BusinessDetails != nil && o.BusinessDetails.Validate() == nil
}

// ValidatePlatformFeeRate reports whether rateBps is within 0 to 30%.
func ValidatePlatformFeeRate(rateBps int) error {
	if rateBps < 0 || rateBps > MaxPlatformFeeRateBps {
		return errors.New("platform fee rate must be 0 to 3000 basis points")
	}
	return nil
}

// BusinessDetails is an Organizer's 特商法 (Specified Commercial Transactions
// Act) 販売業者 disclosure, shown to fans at checkout and in the confirmation
// email. It is replaced as a whole.
//
// Mirrors the legal_name, representative_name, business_address,
// business_phone_number and contact_email fields of
// liverty_music.entity.v1.Organizer.
type BusinessDetails struct {
	// LegalName is the Organizer's legal name, 1 to 200 characters.
	LegalName string
	// RepresentativeName is the representative or responsible person, 1 to 100
	// characters.
	RepresentativeName string
	// BusinessAddress is the Organizer's business address, 1 to 300
	// characters.
	BusinessAddress string
	// BusinessPhoneNumber is the Organizer's business phone number in E.164
	// form.
	BusinessPhoneNumber string
	// ContactEmail is the Organizer's contact email address, at most 165
	// characters.
	ContactEmail string
}

// Validate reports whether all five business details are present and valid.
func (d *BusinessDetails) Validate() error {
	if !lenBetween(d.LegalName, 1, 200) {
		return errors.New("legal name must be 1 to 200 characters")
	}
	if !lenBetween(d.RepresentativeName, 1, 100) {
		return errors.New("representative must be 1 to 100 characters")
	}
	if !lenBetween(d.BusinessAddress, 1, 300) {
		return errors.New("business address must be 1 to 300 characters")
	}
	if !IsE164(d.BusinessPhoneNumber) {
		return errors.New("business phone number must be in E.164 form")
	}
	if err := ValidateEmail(d.ContactEmail); err != nil {
		return errors.New("contact email " + err.Error())
	}
	return nil
}

// lenBetween reports whether s has between lo and hi characters inclusive.
func lenBetween(s string, lo, hi int) bool {
	n := utf8.RuneCountInString(s)
	return n >= lo && n <= hi
}

// OrganizerRepository persists Organizers and their artist associations.
type OrganizerRepository interface {
	// Create stores the Organizer with its id, name, short name, owner email,
	// provider organizer ID and status, and returns it as stored. An Organizer
	// without an id receives a new one. It does not deduplicate by name or
	// owner email.
	//
	// # Possible errors
	//   - AlreadyExists: another Organizer, in any status, has the short name;
	//     nothing is stored.
	Create(ctx context.Context, o *Organizer) (*Organizer, error)

	// Get returns the Organizer by id.
	//
	// # Possible errors
	//   - NotFound: no organizer with the id exists.
	Get(ctx context.Context, id string) (*Organizer, error)

	// GetByProviderOrganizerID returns the Organizer whose provider_organizer_id matches the
	// given Zitadel organization id. The link is established during
	// provisioning and is unique (the partial unique index covers non-NULL values).
	//
	// # Possible errors
	//   - NotFound: no organizer with the given provider_organizer_id exists.
	GetByProviderOrganizerID(ctx context.Context, providerOrganizerID string) (*Organizer, error)

	// List returns every Organizer.
	List(ctx context.Context) ([]*Organizer, error)

	// ListByOwnerEmail returns every active Organizer whose owner email equals
	// email, compared without regard to letter case, ordered by name. It
	// returns an empty list when none matches.
	ListByOwnerEmail(ctx context.Context, email string) ([]*Organizer, error)

	// ListByStatus returns every Organizer in the given operational status. Used
	// by the reconciler to find rows stuck in the provisioning state.
	ListByStatus(ctx context.Context, status OrganizerStatus) ([]*Organizer, error)

	// SetProviderOrganizerID persists the Organization link produced during provisioning.
	//
	// # Possible errors
	//   - NotFound: no organizer with the id exists.
	SetProviderOrganizerID(ctx context.Context, id, providerOrganizerID string) error

	// SetStatus updates the operational lifecycle status.
	//
	// # Possible errors
	//   - NotFound: no organizer with the id exists.
	SetStatus(ctx context.Context, id string, status OrganizerStatus) error

	// CompareAndSetStatus atomically transitions status from `from` to `to`. It
	// reports whether the transition was applied — false means the current status
	// was not `from` (e.g. a concurrent Deactivate superseded it), which the
	// caller must not treat as its own success.
	CompareAndSetStatus(ctx context.Context, id string, from, to OrganizerStatus) (bool, error)

	// AssociateArtist links an existing artist to the organizer. Existence of the
	// organizer and artist is validated by the caller; this enforces the
	// at-most-one-organizer-per-artist rule.
	//
	// # Possible errors
	//   - AlreadyExists: the artist is already represented by an organizer.
	AssociateArtist(ctx context.Context, organizerID, artistID string) error

	// DisassociateArtist removes the link between an organizer and an artist. It is
	// idempotent: removing a link that does not exist succeeds.
	DisassociateArtist(ctx context.Context, organizerID, artistID string) error

	// ListArtists returns the artists the organizer represents.
	ListArtists(ctx context.Context, organizerID string) ([]*Artist, error)

	// FreeArtists removes all of the organizer's artist associations, freeing them
	// for re-association. Used by deactivation.
	FreeArtists(ctx context.Context, organizerID string) error

	// IsArtistRepresentedByActiveOrganizer reports whether the given artist is
	// currently associated with at least one active (status=2) organizer. Used by
	// SearchNewConcerts to skip the discovery pipeline for first-party artists;
	// when the organizer is deactivated the method returns false and discovery
	// resumes automatically.
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	IsArtistRepresentedByActiveOrganizer(ctx context.Context, artistID string) (bool, error)

	// SetBusinessDetails replaces the Organizer's business details as a whole,
	// leaves its other attributes unchanged and returns the updated Organizer.
	//
	// # Possible errors
	//   - InvalidArgument: any detail is missing or breaks the business details
	//     rule; nothing changed.
	//   - NotFound: no organizer with the id exists.
	SetBusinessDetails(ctx context.Context, id string, details BusinessDetails) (*Organizer, error)

	// SetPlatformFeeRate stores the platform fee rate applied to the
	// Organizer's future Orders.
	//
	// # Possible errors
	//   - InvalidArgument: the rate is outside 0 to 3000 basis points.
	//   - NotFound: no organizer with the id exists.
	SetPlatformFeeRate(ctx context.Context, id string, rateBps int) error

	// Delete permanently removes, in one transaction, a deactivated Organizer
	// together with its first-party Series with their SeatTypes and
	// TicketSales, and their Events (with each Event's TicketStocks, ticket
	// journeys, performers, Scanners, admissions and rejected scans), the Void
	// and Refunded Orders for those Events with their lottery entries,
	// Checkouts and Tickets, the Tickets' ResaleListings and the Orders'
	// Reversed Settlements, its SeriesImage records, its artist associations
	// and its payout-account record. It removes nothing when the Organizer is
	// not deactivated, when any of its Events has a Paid Order, a Settlement
	// that is not Reversed, or a purchase in progress (a Pending Order, or a
	// Void Order whose card authorization is not yet released), or when the
	// Organizer has a payout-account record.
	//
	// With dryRun it runs the same checks and removes nothing, so a caller can
	// learn whether deletion is blocked before removing anything kept outside
	// the database.
	//
	// # Possible errors
	//
	//  - NotFound: no organizer with the id exists.
	//  - FailedPrecondition: the organizer is not deactivated, or a purchase or
	//    payout account blocks deletion.
	Delete(ctx context.Context, id string, dryRun bool) error
}

// SignInLinkSender sends the email that carries the organizer console
// sign-in links (Organizer.SendSignInLinks).
type SignInLinkSender interface {
	// SendSignInLinks sends one email, in Japanese, to the address to, listing
	// each Organizer by name with a link to the organizer console that names
	// its Organization by its short name.
	//
	// # Possible errors
	//   - Internal: the email could not be sent.
	SendSignInLinks(ctx context.Context, to string, organizers []*Organizer) error
}
