package usecase

import "context"

// OrganizerProvisioner provisions and tears down an Organizer's isolated Zitadel
// tenant. It wraps the Zitadel Management API (an external service), so — like
// EmailVerifier — the interface lives in the usecase layer where it is consumed,
// not in entity.
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
