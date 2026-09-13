package entity

import "context"

// PayoutOnboardingStatus is the Organizer's readiness to receive payouts.
//
// It is derived from the Stripe connected account's transfers capability and
// is the platform's own enum — never a raw Stripe status string. Switching
// payment provider is therefore an adapter change, not a schema break.
// Values mirror the proto enum
// liverty_music.entity.v1.PayoutOnboardingStatus.
type PayoutOnboardingStatus int16

const (
	// PayoutOnboardingStatusUnspecified is the zero value and is never
	// persisted.
	PayoutOnboardingStatusUnspecified PayoutOnboardingStatus = 0
	// PayoutOnboardingStatusPending means the connected account exists but
	// the transfers capability is not yet active (identity verification /
	// KYB is still in progress). Payout is withheld.
	PayoutOnboardingStatusPending PayoutOnboardingStatus = 1
	// PayoutOnboardingStatusActive means the transfers capability is active
	// (KYC/KYB cleared). The Organizer is eligible to receive the scheduled
	// post-event Transfer.
	PayoutOnboardingStatusActive PayoutOnboardingStatus = 2
	// PayoutOnboardingStatusRestricted means the capability was disabled or
	// rejected. Payout is withheld until the status returns to Active.
	PayoutOnboardingStatusRestricted PayoutOnboardingStatus = 3
)

// String returns the lowercase status name.
func (s PayoutOnboardingStatus) String() string {
	switch s {
	case PayoutOnboardingStatusPending:
		return "pending"
	case PayoutOnboardingStatusActive:
		return "active"
	case PayoutOnboardingStatusRestricted:
		return "restricted"
	default:
		return "UNSPECIFIED"
	}
}

// IsValid reports whether s is a recognized, persistable status.
func (s PayoutOnboardingStatus) IsValid() bool {
	return s >= PayoutOnboardingStatusPending && s <= PayoutOnboardingStatusRestricted
}

// IsPayoutEligible reports whether an Organizer with this status may receive a
// payout. Only Active accounts are eligible; Pending and Restricted accounts
// have their payouts withheld (not failed).
func (s PayoutOnboardingStatus) IsPayoutEligible() bool {
	return s == PayoutOnboardingStatusActive
}

// OrganizerConnectedAccount is the opaque, provider-agnostic reference to an
// Organizer's payout-recipient account plus its onboarding status.
//
// The account is provisioned as a RECIPIENT (transfers capability only, never
// card_payments): the platform is the Stripe settlement merchant and holds
// buyer funds, while this account only receives the post-event Transfer. It
// stores only a token meaningful to the provider (a Stripe "acct_..." id),
// never bank or identity data. Mirrors
// liverty_music.entity.v1.OrganizerConnectedAccount.
type OrganizerConnectedAccount struct {
	// OrganizerID is the Organizer that owns this payout-recipient account.
	OrganizerID string
	// AccountRef is the provider's connected-account reference (e.g. a Stripe
	// "acct_..." id). Opaque; meaningful only to the provider.
	AccountRef string
	// Status is the account's payout-onboarding readiness. Payout eligibility
	// is exactly Status == Active.
	Status PayoutOnboardingStatus
}

// OrganizerConnectedAccountRepository defines the persistence contract for
// OrganizerConnectedAccount records. Interfaces are defined where consumed
// (AGENTS.md rule).
type OrganizerConnectedAccountRepository interface {
	// Upsert inserts an OrganizerConnectedAccount row if none exists for the
	// given OrganizerID, or updates the AccountRef and Status if it does. Used
	// during Connect onboarding to idempotently persist the account.
	//
	// # Possible errors
	//
	//  - Internal: database query or execution failure.
	Upsert(ctx context.Context, a *OrganizerConnectedAccount) error

	// GetByOrganizerID returns the OrganizerConnectedAccount for the given
	// Organizer.
	//
	// # Possible errors
	//
	//  - NotFound: no connected account exists for this Organizer.
	//  - Internal: database query failure.
	GetByOrganizerID(ctx context.Context, organizerID string) (*OrganizerConnectedAccount, error)

	// UpdateStatus updates only the Status field of the connected account.
	// Called by the payout sweeper to refresh capability status before each
	// release attempt.
	//
	// # Possible errors
	//
	//  - NotFound: no connected account exists for this Organizer.
	//  - Internal: database execution failure.
	UpdateStatus(ctx context.Context, organizerID string, status PayoutOnboardingStatus) error
}
