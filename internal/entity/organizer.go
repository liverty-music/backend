package entity

import (
	"context"
	"errors"
	"net/mail"
	"unicode/utf8"
)

// OrganizerStatus is the operational provisioning lifecycle of an Organizer. It
// is a backend-only marker (not exposed on the consumer proto) and is distinct
// from any business vetting flag: existence is the vetting.
type OrganizerStatus int16

const (
	// OrganizerStatusUnspecified is the zero value and is never persisted.
	OrganizerStatusUnspecified OrganizerStatus = 0
	// OrganizerStatusProvisioning means tenant provisioning is in progress and the
	// Organizer is not yet fully usable.
	OrganizerStatusProvisioning OrganizerStatus = 1
	// OrganizerStatusActive means provisioning completed and the Organizer is usable.
	OrganizerStatusActive OrganizerStatus = 2
	// OrganizerStatusDeactivated means the Organizer is turned off: its operations
	// are rejected and its artist associations have been freed.
	OrganizerStatusDeactivated OrganizerStatus = 3
)

// Organizer is a vetted business — a record label, management/agency, promoter, or
// a self-publishing artist — that an admin creates to represent artists. It runs
// against its own isolated Zitadel tenant its operators sign into. Existence is
// the vetting; there is no separate verified flag.
type Organizer struct {
	ID   string
	Name string
	// OperatorEmail is the initial operator's email, captured at creation and
	// persisted so the reconciler can complete provisioning after a partial
	// failure. It is backend-only (not exposed on the consumer proto).
	OperatorEmail       string
	ProviderOrganizerID string // empty until tenant provisioning persists the tenant link
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
// UUIDv7 id and the default platform fee rate.
func NewOrganizer(name string) *Organizer {
	return &Organizer{
		ID:                 NewID(),
		Name:               name,
		Status:             OrganizerStatusProvisioning,
		PlatformFeeRateBps: DefaultPlatformFeeRateBps,
	}
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
	// ContactEmail is the Organizer's contact email address.
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
	if addr, err := mail.ParseAddress(d.ContactEmail); err != nil || addr.Address != d.ContactEmail {
		return errors.New("contact email must be an email address")
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
	// Create inserts a new Organizer row (typically in the provisioning state).
	Create(ctx context.Context, o *Organizer) (*Organizer, error)

	// Get returns the Organizer by id.
	//
	// # Possible errors
	//   - NotFound: no organizer with the id exists.
	Get(ctx context.Context, id string) (*Organizer, error)

	// GetByProviderOrganizerID returns the Organizer whose provider_organizer_id matches the
	// given Zitadel organization id. The link is established during tenant
	// provisioning and is unique (the partial unique index covers non-NULL values).
	//
	// # Possible errors
	//   - NotFound: no organizer with the given provider_organizer_id exists.
	GetByProviderOrganizerID(ctx context.Context, providerOrganizerID string) (*Organizer, error)

	// List returns every Organizer.
	List(ctx context.Context) ([]*Organizer, error)

	// ListByStatus returns every Organizer in the given operational status. Used
	// by the reconciler to find rows stuck in the provisioning state.
	ListByStatus(ctx context.Context, status OrganizerStatus) ([]*Organizer, error)

	// SetProviderOrganizerID persists the tenant-org link produced during provisioning.
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
	// together with its first-party Series with their TicketSales, and their
	// Events (with each Event's TicketTypes, lottery entries, ticket journeys,
	// performers, Scanners, admissions and rejected scans), the
	// refunded Orders, their Tickets and their Reversed Settlements for those
	// Events, its SeriesImage records, its artist associations and its
	// payout-account record. It removes nothing when
	// the Organizer is not deactivated, when any of its Events has an Order that
	// is not Refunded or a Settlement that is not Reversed, or when the
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
