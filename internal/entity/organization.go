package entity

import "context"

// OrganizationRepository creates, provisions and removes Organizations, the
// sign-in units at the identity provider. An Organizer's Organization is
// named by the Organizer's short name and holds the identities of that
// Organizer's members. The identity provider is the only record of an
// Organization; implementations call the Zitadel Management API.
//
// Provisioning is split so the caller can record the Organization's id as soon
// as it exists: Create creates the Organization, the caller records its id
// (resolving it with GetByName when Create reports AlreadyExists), and
// Provision completes the remaining steps against that id. A retry with a
// recorded id never creates or searches for the Organization again.
type OrganizationRepository interface {
	// Create creates the Organization named by the Organizer's short name and
	// returns its id at the identity provider.
	//
	// # Possible errors
	//
	//  - AlreadyExists: an Organization with that name exists.
	//  - Internal: the Organization could not be created.
	Create(ctx context.Context, shortName string) (providerOrganizerID string, err error)

	// GetByName returns the id of the Organization named by the Organizer's
	// short name, ignoring removed Organizations. Used for Organizers whose
	// Organization was never recorded.
	//
	// # Possible errors
	//
	//  - NotFound: no Organization has the short name.
	//  - Internal: the Organizations could not be searched.
	GetByName(ctx context.Context, shortName string) (providerOrganizerID string, err error)

	// Provision completes the Organization providerOrganizerID: it sets a
	// passkey-primary login policy that reports unknown emails, project-grants
	// organizer-console, and creates the owner identity with the email
	// ownerEmail and the user name "<ownerEmail> (<shortName>)", granted the
	// owner role and invited to register a passkey. The email may already
	// belong to an identity in another Organization, which is neither used nor
	// changed. It is idempotent: a retry after a partial failure completes the
	// remaining steps without creating a second owner identity, and never
	// leaves the owner without the owner role.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the identity provider rejected the owner email or
	//    the user name. Retrying cannot succeed.
	//  - AlreadyExists: the user name is taken outside the Organization.
	//    Retrying cannot succeed.
	//  - Internal: any other step failed; a retry may succeed.
	Provision(ctx context.Context, providerOrganizerID, shortName, ownerEmail string) error

	// DeactivateIdentities makes every identity in the Organization unable to
	// sign in: an identity that has signed in before is deactivated and one
	// that never completed its first sign-in is removed. The Organization
	// stays. Identities already turned off count as done, so a repeated call
	// succeeds.
	//
	// # Possible errors
	//
	//  - Internal: an identity could not be turned off.
	DeactivateIdentities(ctx context.Context, providerOrganizerID string) error

	// Delete removes the Organization together with every identity in it. An
	// Organization that no longer exists counts as removed, so a repeated call
	// succeeds.
	//
	// # Possible errors
	//
	//  - Internal: the Organization could not be removed.
	Delete(ctx context.Context, providerOrganizerID string) error
}
