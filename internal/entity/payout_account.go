package entity

import "context"

// PayoutOnboardingStatus is the Organizer's readiness to receive payouts.
//
// It is derived from the payout account's transfers capability at Stripe and
// is the platform's own enum — never a raw Stripe status string. Switching
// payment provider is therefore an adapter change, not a schema break.
// Values mirror the proto enum
// liverty_music.entity.v1.PayoutOnboardingStatus.
type PayoutOnboardingStatus int16

const (
	// PayoutOnboardingStatusUnspecified is the zero value and is never
	// persisted.
	PayoutOnboardingStatusUnspecified PayoutOnboardingStatus = 0
	// PayoutOnboardingStatusPending means the payout account exists but
	// the transfers capability is not yet active (identity verification /
	// KYB is still in progress). Payout is withheld.
	PayoutOnboardingStatusPending PayoutOnboardingStatus = 1
	// PayoutOnboardingStatusActive means the transfers capability is active
	// (payout account verification cleared). The Organizer is eligible to receive the scheduled
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

// PayoutAccount is the opaque, provider-agnostic reference to an
// Organizer's payout-recipient account plus its onboarding status.
//
// The account is provisioned as a RECIPIENT (transfers capability only, never
// card_payments): the platform is the Stripe settlement merchant and holds
// fans' funds, while this account only receives the post-event Transfer. It
// stores only a token meaningful to the provider (a Stripe "acct_..." id),
// never bank or identity data. Mirrors
// liverty_music.entity.v1.PayoutAccount.
type PayoutAccount struct {
	// OrganizerID is the Organizer that owns this payout-recipient account.
	OrganizerID string
	// ProviderAccountID is the payment provider's ID for the payout account (e.g. a Stripe
	// "acct_..." id). Opaque; meaningful only to the provider.
	ProviderAccountID string
	// Status is the account's payout-onboarding readiness. Payout eligibility
	// is exactly Status == Active.
	Status PayoutOnboardingStatus
}

// PayoutAccountRepository defines the persistence contract for
// PayoutAccount records. Interfaces are defined where consumed
// (AGENTS.md rule).
type PayoutAccountRepository interface {
	// Upsert inserts an PayoutAccount row if none exists for the
	// given OrganizerID, or updates the ProviderAccountID and Status if it does. Used
	// during Connect onboarding to idempotently persist the account.
	//
	// # Possible errors
	//
	//  - Internal: database query or execution failure.
	Upsert(ctx context.Context, a *PayoutAccount) error

	// GetByOrganizerID returns the PayoutAccount for the given
	// Organizer.
	//
	// # Possible errors
	//
	//  - NotFound: no payout account exists for this Organizer.
	//  - Internal: database query failure.
	GetByOrganizerID(ctx context.Context, organizerID string) (*PayoutAccount, error)

	// UpdateStatus updates only the Status field of the payout account.
	// Called by the payout sweeper to refresh capability status before each
	// release attempt.
	//
	// # Possible errors
	//
	//  - NotFound: no payout account exists for this Organizer.
	//  - Internal: database execution failure.
	UpdateStatus(ctx context.Context, organizerID string, status PayoutOnboardingStatus) error

	// Create provisions a new Accounts v2 payout-recipient
	// payout account for the Organizer. The account requests ONLY the
	// transfers capability on stripe_balance (never card_payments) and is
	// configured with losses_collector = application so the platform absorbs
	// negative balances. Returns the payment provider's ID for the account ("acct_...").
	//
	// contactEmail is the Organizer's business contact address. Stripe rejects
	// a recipient configuration without one ("If configuration.recipient is
	// supplied, the Account must have a contact email"), and it is the address
	// Stripe uses to reach the account. Apart from it the request carries no
	// personal data: the Organizer supplies name, date of birth, address and
	// documents directly to Stripe through the hosted onboarding link, so the
	// account is created unverified and becomes payout-eligible only once
	// Stripe reports the transfers capability active.
	//
	// # Possible errors
	//
	//  - Unavailable: the payment provider is unreachable or platform Connect
	//    is not yet enabled.
	Create(ctx context.Context, organizerID string, contactEmail string) (providerAccountID string, err error)

	// GetStatus retrieves the payout-onboarding status of the
	// payout account identified by providerAccountID. It maps the provider's
	// transfers capability state to the platform's own
	// PayoutOnboardingStatus enum without exposing raw provider status.
	//
	// # Possible errors
	//
	//  - NotFound: the account does not exist.
	//  - Unavailable: the payment provider is unreachable.
	GetStatus(ctx context.Context, providerAccountID string) (PayoutOnboardingStatus, error)

	// CreateOnboardingLink returns a provider-hosted URL where the Organizer
	// can start or continue payout account verification. The link is single-use and
	// short-lived; callers must not cache it.
	//
	// # Possible errors
	//
	//  - NotFound: the account does not exist.
	//  - Unavailable: the payment provider is unreachable.
	CreateOnboardingLink(ctx context.Context, providerAccountID string, returnURL string) (onboardingURL string, err error)
}
