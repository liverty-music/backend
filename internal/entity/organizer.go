package entity

import "context"

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

// Organizer is a vetted seller — a record label, management/agency, promoter, or
// a self-publishing artist — that an admin creates to represent artists. It runs
// against its own isolated Zitadel tenant its operators sign into. Existence is
// the vetting; there is no separate verified flag.
type Organizer struct {
	ID   string
	Name string
	// OperatorEmail is the initial operator's email, captured at creation and
	// persisted so the reconciler can complete provisioning after a partial
	// failure. It is backend-only (not exposed on the consumer proto).
	OperatorEmail string
	ZitadelOrgID  string // empty until tenant provisioning persists the tenant link
	Status        OrganizerStatus
}

// NewOrganizer creates a new Organizer in the provisioning state with a generated
// UUIDv7 id.
func NewOrganizer(name string) *Organizer {
	return &Organizer{
		ID:     newID(),
		Name:   name,
		Status: OrganizerStatusProvisioning,
	}
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

	// List returns every Organizer.
	List(ctx context.Context) ([]*Organizer, error)

	// ListByStatus returns every Organizer in the given operational status. Used
	// by the reconciler to find rows stuck in the provisioning state.
	ListByStatus(ctx context.Context, status OrganizerStatus) ([]*Organizer, error)

	// SetZitadelOrgID persists the tenant-org link produced during provisioning.
	//
	// # Possible errors
	//   - NotFound: no organizer with the id exists.
	SetZitadelOrgID(ctx context.Context, id, zitadelOrgID string) error

	// SetStatus updates the operational lifecycle status.
	//
	// # Possible errors
	//   - NotFound: no organizer with the id exists.
	SetStatus(ctx context.Context, id string, status OrganizerStatus) error

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
}

// OrganizerProvisioner provisions and tears down an Organizer's isolated Zitadel
// tenant. It is implemented by the Zitadel Management-API client.
type OrganizerProvisioner interface {
	// ProvisionTenant is idempotent and compensating: keyed on organizerID, it
	// creates the tenant org (if absent) with a passkey-primary login policy,
	// project-grants organizer-console, and seeds the initial operator as owner
	// with a passkey-registration init link. It returns the Zitadel org id. A
	// retry after a partial failure completes the remaining steps without
	// creating a duplicate org and never leaves the operator without an owner grant.
	ProvisionTenant(ctx context.Context, organizerID, name, operatorEmail string) (zitadelOrgID string, err error)

	// DeactivateOperators deactivates every operator in the Organizer's tenant org.
	DeactivateOperators(ctx context.Context, zitadelOrgID string) error
}
