package usecase

import "context"

// OrganizerProvisioner provisions and tears down an Organizer's isolated Zitadel
// tenant. It wraps the Zitadel Management API (an external service); unlike
// [entity.EmailVerifier], the interface lives in the usecase layer where it is
// consumed, not in entity.
//
// Provisioning is split so the caller can persist the tenant org id as soon as
// the org exists: EnsureTenantOrg creates (or finds) the org, the caller
// records its id, and ProvisionTenant completes the remaining steps against
// that id. A retry with a recorded id never creates or searches for the org
// again.
type OrganizerProvisioner interface {
	// CheckOperatorEmailAvailable reports whether operatorEmail can be used for
	// a new tenant's initial operator. Zitadel user names are unique across the
	// whole instance and default to the email, so an email that any existing
	// user, in any org, already has as its email, user name or login name
	// cannot be used.
	//
	// # Possible errors
	//
	//  - AlreadyExists: an existing user already uses the email.
	//  - Internal: the users could not be searched.
	CheckOperatorEmailAvailable(ctx context.Context, operatorEmail string) error

	// EnsureTenantOrg creates the Organizer's tenant org, named deterministically
	// from organizerID, or returns the id of the org created by an earlier
	// attempt. It returns the Zitadel org id.
	//
	// # Possible errors
	//
	//  - Internal: the org could not be created or found.
	EnsureTenantOrg(ctx context.Context, organizerID string) (zitadelOrgID string, err error)

	// FindTenantOrg returns the id of the Organizer's tenant org, looked up by
	// its deterministic name. Used for Organizers whose tenant link was never
	// recorded.
	//
	// # Possible errors
	//
	//  - NotFound: no tenant org exists for the Organizer.
	//  - Internal: the orgs could not be searched.
	FindTenantOrg(ctx context.Context, organizerID string) (zitadelOrgID string, err error)

	// ProvisionTenant completes the tenant org zitadelOrgID: it sets a
	// passkey-primary login policy, project-grants organizer-console, and seeds
	// the initial operator as owner with an invitation to register a passkey. It
	// is idempotent: a retry after a partial failure completes the remaining
	// steps without creating a second operator, and never leaves the operator
	// without an owner grant.
	//
	// # Possible errors
	//
	//  - AlreadyExists: the operator email belongs to a user outside the tenant
	//    org. Retrying cannot succeed.
	//  - InvalidArgument: Zitadel rejected the operator email. Retrying cannot
	//    succeed.
	//  - Internal: any other step failed; a retry may succeed.
	ProvisionTenant(ctx context.Context, organizerID, zitadelOrgID, operatorEmail string) error

	// DeactivateOperators deactivates every operator in the Organizer's tenant org.
	DeactivateOperators(ctx context.Context, zitadelOrgID string) error

	// DeleteTenant removes the Organizer's tenant org together with every
	// operator in it. A tenant org that no longer exists counts as removed, so a
	// repeated call succeeds.
	//
	// # Possible errors
	//
	//  - Internal: the tenant org could not be removed.
	DeleteTenant(ctx context.Context, zitadelOrgID string) error
}
