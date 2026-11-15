package usecase

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// EmailVerificationCodes issues and checks an identity's email verification
// codes at the identity provider. A code is a credential and is never logged.
type EmailVerificationCodes interface {
	// IssueEmailVerificationCode issues a new code for the identity's email
	// address and returns the address and the code; the identity provider
	// sends nothing.
	//
	// # Possible errors
	//
	//   - NotFound: the identity does not exist.
	//   - FailedPrecondition: its address is already verified.
	//   - Unavailable: the identity provider cannot be reached.
	//   - Internal: any other failure.
	IssueEmailVerificationCode(ctx context.Context, providerUserID string) (address, code string, err error)
	// VerifyEmail marks the identity's address verified with code.
	//
	// # Possible errors
	//
	//   - InvalidArgument: the code is wrong, expired or used.
	//   - NotFound: the identity does not exist.
	//   - Unavailable: the identity provider cannot be reached.
	//   - Internal: any other failure.
	VerifyEmail(ctx context.Context, providerUserID, code string) error
}

// PasskeyRegistrationLinkIssuer issues an identity's passkey registration link
// at the identity provider.
type PasskeyRegistrationLinkIssuer interface {
	// IssuePasskeyRegistrationLink returns the identity's verified email
	// address and a one-time passkey registration link.
	//
	// # Possible errors
	//
	//   - NotFound: the identity does not exist.
	//   - FailedPrecondition: its address is not verified; nothing is issued.
	//   - Internal: any other failure.
	IssuePasskeyRegistrationLink(ctx context.Context, providerUserID string) (address, link string, err error)
}

// PasskeyRegistrationCodes reads an identity's email address and issues its
// passkey registration links at the identity provider. A link is a credential
// and is never logged.
type PasskeyRegistrationCodes interface {
	// GetIdentityEmail reads the identity's email address, whether it is
	// verified, and its organization.
	//
	// # Possible errors
	//
	//   - NotFound: the identity does not exist.
	//   - Internal: any other failure.
	GetIdentityEmail(ctx context.Context, providerUserID string) (*entity.IdentityEmail, error)

	// CreatePasskeyRegistrationLink issues a one-time passkey registration
	// code for the identity of organization and returns the link that
	// registers a passkey with it without a sign-in.
	//
	// # Possible errors
	//
	//   - NotFound: the identity does not exist.
	//   - Internal: any other failure.
	CreatePasskeyRegistrationLink(ctx context.Context, providerUserID, organization string) (string, error)
}

// IdentityUserUseCase changes a fan's identity at the identity provider for
// the identity service's UserService. The identity service holds the
// identity-provider credentials. IssuePasskeyRegistrationLink issues a link
// only for a verified address ([entity.IdentityEmail.CheckPasskeyRecovery]);
// every other method passes the call to one identity-provider port and
// returns its result and error code unchanged.
type IdentityUserUseCase interface {
	EmailVerificationCodes
	PasskeyRegistrationLinkIssuer
	entity.IdentityRemover
}

// IdentityOrganizationUseCase creates, provisions and removes Organizers'
// Organizations at the identity provider for the identity service's
// OrganizationService. Provision runs the provisioning steps of
// [IdentityOrganizations] in order; every other method passes the call to it
// and returns its result and error code unchanged.
type IdentityOrganizationUseCase interface {
	entity.OrganizationRepository
}

// IdentityOrganizations is the identity provider's Organizations as the
// identity service changes them: Create, GetByName, DeactivateIdentities and
// Delete as [entity.OrganizationRepository] states them, and the steps that
// provision an Organization, each idempotent, so a retry repeats the steps
// already done without harm.
type IdentityOrganizations interface {
	Create(ctx context.Context, shortName string) (providerOrganizerID string, err error)
	GetByName(ctx context.Context, shortName string) (providerOrganizerID string, err error)
	DeactivateIdentities(ctx context.Context, providerOrganizerID string) error
	Delete(ctx context.Context, providerOrganizerID string) error

	// EnsurePasskeyLoginPolicy sets the passkey-primary login policy that
	// reports unknown emails on the Organization.
	//
	// # Possible errors
	//
	//   - Internal: the policy could not be set.
	EnsurePasskeyLoginPolicy(ctx context.Context, providerOrganizerID string) error

	// EnsureConsoleProjectGrant project-grants organizer-console to the
	// Organization.
	//
	// # Possible errors
	//
	//   - Internal: the grant could not be made.
	EnsureConsoleProjectGrant(ctx context.Context, providerOrganizerID string) error

	// EnsureOwnerIdentity creates the owner identity with the email
	// ownerEmail and the user name "<ownerEmail> (<shortName>)", invited to
	// register a passkey, or reuses the one an earlier attempt created in the
	// Organization, and returns its id. The email may already belong to an
	// identity in another Organization, which is neither used nor changed.
	//
	// # Possible errors
	//
	//   - InvalidArgument: the identity provider rejected the owner email or
	//     the user name. Retrying cannot succeed.
	//   - AlreadyExists: the user name is taken outside the Organization.
	//     Retrying cannot succeed.
	//   - Internal: any other failure; a retry may succeed.
	EnsureOwnerIdentity(ctx context.Context, providerOrganizerID, shortName, ownerEmail string) (ownerID string, err error)

	// EnsureOwnerGrant grants the identity ownerID the owner role on
	// organizer-console.
	//
	// # Possible errors
	//
	//   - Internal: the grant could not be made.
	EnsureOwnerGrant(ctx context.Context, providerOrganizerID, ownerID string) error
}

// identityUserUseCase implements IdentityUserUseCase.
type identityUserUseCase struct {
	emailCodes      EmailVerificationCodes
	passkeyLinks    PasskeyRegistrationCodes
	identityRemover entity.IdentityRemover
}

// NewIdentityUserUseCase returns an IdentityUserUseCase over the
// identity-provider ports.
func NewIdentityUserUseCase(emailCodes EmailVerificationCodes, passkeyLinks PasskeyRegistrationCodes, identityRemover entity.IdentityRemover) IdentityUserUseCase {
	return &identityUserUseCase{emailCodes: emailCodes, passkeyLinks: passkeyLinks, identityRemover: identityRemover}
}

// IssueEmailVerificationCode passes the call to the EmailVerificationCodes.
func (uc *identityUserUseCase) IssueEmailVerificationCode(ctx context.Context, providerUserID string) (string, string, error) {
	return uc.emailCodes.IssueEmailVerificationCode(ctx, providerUserID)
}

// VerifyEmail passes the call to the EmailVerificationCodes.
func (uc *identityUserUseCase) VerifyEmail(ctx context.Context, providerUserID, code string) error {
	return uc.emailCodes.VerifyEmail(ctx, providerUserID, code)
}

// IssuePasskeyRegistrationLink reads the identity's email address and, when
// it is verified, issues a passkey registration link for it and returns the
// address and the link. An identity whose address is not verified fails with
// FailedPrecondition and gets no code.
func (uc *identityUserUseCase) IssuePasskeyRegistrationLink(ctx context.Context, providerUserID string) (string, string, error) {
	email, err := uc.passkeyLinks.GetIdentityEmail(ctx, providerUserID)
	if err != nil {
		return "", "", err
	}
	if err := email.CheckPasskeyRecovery(); err != nil {
		return "", "", apperr.Wrap(err, codes.FailedPrecondition, "passkey registration link not issued",
			slog.String("provider_user_id", providerUserID))
	}
	link, err := uc.passkeyLinks.CreatePasskeyRegistrationLink(ctx, providerUserID, email.Organization)
	if err != nil {
		return "", "", err
	}
	return email.Address, link, nil
}

// DeleteIdentity passes the call to the IdentityRemover.
func (uc *identityUserUseCase) DeleteIdentity(ctx context.Context, providerUserID string) error {
	return uc.identityRemover.DeleteIdentity(ctx, providerUserID)
}

// identityOrganizationUseCase implements IdentityOrganizationUseCase.
type identityOrganizationUseCase struct {
	organizations IdentityOrganizations
}

// NewIdentityOrganizationUseCase returns an IdentityOrganizationUseCase over
// the identity provider's Organizations.
func NewIdentityOrganizationUseCase(organizations IdentityOrganizations) IdentityOrganizationUseCase {
	return &identityOrganizationUseCase{organizations: organizations}
}

// Create passes the call to the OrganizationRepository.
func (uc *identityOrganizationUseCase) Create(ctx context.Context, shortName string) (string, error) {
	return uc.organizations.Create(ctx, shortName)
}

// GetByName passes the call to the OrganizationRepository.
func (uc *identityOrganizationUseCase) GetByName(ctx context.Context, shortName string) (string, error) {
	return uc.organizations.GetByName(ctx, shortName)
}

// Provision completes the Organization providerOrganizerID in this order: the
// login policy, the organizer-console project grant, the owner identity and
// the owner's grant. The owner is granted last, after its identity exists, so
// a retry after a partial failure never leaves the owner without the owner
// role. Each step's error is returned with its code.
//
// An Organizer without a short name cannot be provisioned: only the Organizer
// created before short names lacks one; it is given one by hand, after which
// a retry succeeds. Provision then fails with Internal and changes nothing.
func (uc *identityOrganizationUseCase) Provision(ctx context.Context, providerOrganizerID, shortName, ownerEmail string) error {
	if shortName == "" {
		return apperr.New(codes.Internal, "organizer has no short name",
			slog.String("zitadel_org_id", providerOrganizerID))
	}
	if err := uc.organizations.EnsurePasskeyLoginPolicy(ctx, providerOrganizerID); err != nil {
		return err
	}
	if err := uc.organizations.EnsureConsoleProjectGrant(ctx, providerOrganizerID); err != nil {
		return err
	}
	ownerID, err := uc.organizations.EnsureOwnerIdentity(ctx, providerOrganizerID, shortName, ownerEmail)
	if err != nil {
		return err
	}
	return uc.organizations.EnsureOwnerGrant(ctx, providerOrganizerID, ownerID)
}

// DeactivateIdentities passes the call to the OrganizationRepository.
func (uc *identityOrganizationUseCase) DeactivateIdentities(ctx context.Context, providerOrganizerID string) error {
	return uc.organizations.DeactivateIdentities(ctx, providerOrganizerID)
}

// Delete passes the call to the OrganizationRepository.
func (uc *identityOrganizationUseCase) Delete(ctx context.Context, providerOrganizerID string) error {
	return uc.organizations.Delete(ctx, providerOrganizerID)
}
