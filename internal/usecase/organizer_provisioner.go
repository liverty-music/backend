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
	// EnsureTenantOrg creates the Organizer's Organization, named by its short
	// name, or returns the id of the Organization created by an earlier
	// attempt. It returns the Zitadel org id.
	//
	// # Possible errors
	//
	//  - Internal: the Organization could not be created or found.
	EnsureTenantOrg(ctx context.Context, shortName string) (providerOrganizerID string, err error)

	// FindTenantOrg returns the id of the Organization named by the
	// Organizer's short name, ignoring removed Organizations. Used for
	// Organizers whose Organization was never recorded.
	//
	// # Possible errors
	//
	//  - NotFound: no Organization has the short name.
	//  - Internal: the Organizations could not be searched.
	FindTenantOrg(ctx context.Context, shortName string) (providerOrganizerID string, err error)

	// ProvisionTenant completes the Organization providerOrganizerID: it sets
	// a passkey-primary login policy that reports unknown emails,
	// project-grants organizer-console, and creates the owner identity with
	// the email ownerEmail and the user name "<ownerEmail> (<shortName>)",
	// granted the owner role and invited to register a passkey. The email may
	// already belong to an identity in another Organization, which is neither
	// used nor changed. It is idempotent: a retry after a partial failure
	// completes the remaining steps without creating a second owner identity,
	// and never leaves the owner without the owner role.
	//
	// # Possible errors
	//
	//  - InvalidArgument: Zitadel rejected the owner email or the user name.
	//    Retrying cannot succeed.
	//  - AlreadyExists: the user name is taken outside the Organization.
	//    Retrying cannot succeed.
	//  - Internal: any other step failed; a retry may succeed.
	ProvisionTenant(ctx context.Context, providerOrganizerID, shortName, ownerEmail string) error

	// DeactivateOperators deactivates every operator in the Organizer's tenant org.
	DeactivateOperators(ctx context.Context, providerOrganizerID string) error

	// DeleteTenant removes the Organizer's tenant org together with every
	// operator in it. A tenant org that no longer exists counts as removed, so a
	// repeated call succeeds.
	//
	// # Possible errors
	//
	//  - Internal: the tenant org could not be removed.
	DeleteTenant(ctx context.Context, providerOrganizerID string) error
}
