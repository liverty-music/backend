package usecase

import (
	"context"

	"github.com/liverty-music/backend/internal/entity"
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
	//   - FailedPrecondition: its address is not verified.
	//   - Internal: any other failure.
	IssuePasskeyRegistrationLink(ctx context.Context, providerUserID string) (address, link string, err error)
}

// IdentityUserUseCase changes a fan's identity at the identity provider for
// the identity service's UserService. The identity service holds the
// identity-provider credentials and has no business rules of its own, so each
// method passes the call to one identity-provider port and returns its result
// and error code unchanged.
type IdentityUserUseCase interface {
	EmailVerificationCodes
	PasskeyRegistrationLinkIssuer
	entity.IdentityRemover
}

// IdentityOrganizationUseCase creates, provisions and removes Organizers'
// Organizations at the identity provider for the identity service's
// OrganizationService. Each method passes the call to the
// [entity.OrganizationRepository] and returns its result and error code
// unchanged.
type IdentityOrganizationUseCase interface {
	entity.OrganizationRepository
}

// identityUserUseCase implements IdentityUserUseCase.
type identityUserUseCase struct {
	emailCodes      EmailVerificationCodes
	passkeyLinks    PasskeyRegistrationLinkIssuer
	identityRemover entity.IdentityRemover
}

// NewIdentityUserUseCase returns an IdentityUserUseCase over the
// identity-provider ports.
func NewIdentityUserUseCase(emailCodes EmailVerificationCodes, passkeyLinks PasskeyRegistrationLinkIssuer, identityRemover entity.IdentityRemover) IdentityUserUseCase {
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

// IssuePasskeyRegistrationLink passes the call to the PasskeyRegistrationLinkIssuer.
func (uc *identityUserUseCase) IssuePasskeyRegistrationLink(ctx context.Context, providerUserID string) (string, string, error) {
	return uc.passkeyLinks.IssuePasskeyRegistrationLink(ctx, providerUserID)
}

// DeleteIdentity passes the call to the IdentityRemover.
func (uc *identityUserUseCase) DeleteIdentity(ctx context.Context, providerUserID string) error {
	return uc.identityRemover.DeleteIdentity(ctx, providerUserID)
}

// identityOrganizationUseCase implements IdentityOrganizationUseCase.
type identityOrganizationUseCase struct {
	organizations entity.OrganizationRepository
}

// NewIdentityOrganizationUseCase returns an IdentityOrganizationUseCase over
// the identity provider's Organizations.
func NewIdentityOrganizationUseCase(organizations entity.OrganizationRepository) IdentityOrganizationUseCase {
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

// Provision passes the call to the OrganizationRepository.
func (uc *identityOrganizationUseCase) Provision(ctx context.Context, providerOrganizerID, shortName, ownerEmail string) error {
	return uc.organizations.Provision(ctx, providerOrganizerID, shortName, ownerEmail)
}

// DeactivateIdentities passes the call to the OrganizationRepository.
func (uc *identityOrganizationUseCase) DeactivateIdentities(ctx context.Context, providerOrganizerID string) error {
	return uc.organizations.DeactivateIdentities(ctx, providerOrganizerID)
}

// Delete passes the call to the OrganizationRepository.
func (uc *identityOrganizationUseCase) Delete(ctx context.Context, providerOrganizerID string) error {
	return uc.organizations.Delete(ctx, providerOrganizerID)
}
