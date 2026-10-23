package rpc

import (
	"context"

	backenduserv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/backend/user/v1/userv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	backenduserv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/backend/user/v1"

	"github.com/liverty-music/backend/internal/entity"
)

// Compile-time assertion that BackendUserHandler satisfies the generated interface.
var _ backenduserv1connect.UserServiceHandler = (*BackendUserHandler)(nil)

// PasskeyRegistrationLinkIssuer issues an identity's passkey registration link
// at the identity provider. It returns the identity's verified email address
// and the link; it fails with NotFound when the identity does not exist,
// FailedPrecondition when its address is not verified, and Internal otherwise.
type PasskeyRegistrationLinkIssuer interface {
	IssuePasskeyRegistrationLink(ctx context.Context, providerUserID string) (address, link string, err error)
}

// EmailVerificationCodes issues and checks an identity's email verification
// codes at the identity provider. A code is a credential and is never logged.
type EmailVerificationCodes interface {
	// IssueEmailVerificationCode issues a new code for the identity's email
	// address and returns the address and the code; the identity provider
	// sends nothing. It fails with NotFound when the identity does not exist,
	// FailedPrecondition when its address is already verified, Unavailable
	// when the identity provider cannot be reached and Internal otherwise.
	IssueEmailVerificationCode(ctx context.Context, providerUserID string) (address, code string, err error)
	// VerifyEmail marks the identity's address verified with code. It fails
	// with InvalidArgument when the code is wrong, expired or used, NotFound
	// when the identity does not exist, Unavailable when the identity
	// provider cannot be reached and Internal otherwise.
	VerifyEmail(ctx context.Context, providerUserID, code string) error
}

// BackendUserHandler implements the identity service's UserService, which the
// event consumer, the fan API and the admin console API call to change a fan's identity at
// the identity provider. The identity service holds the identity-provider
// credentials and has no business rules of its own, so each method calls one
// identity-provider client and maps its result. Which caller may call which
// procedure is enforced by each listener's allowlist, and the request is
// validated by the server's protovalidate interceptor.
type BackendUserHandler struct {
	emailCodes      EmailVerificationCodes
	identityRemover entity.IdentityRemover
	passkeyLinks    PasskeyRegistrationLinkIssuer
}

// NewBackendUserHandler creates a new BackendUserHandler.
func NewBackendUserHandler(emailCodes EmailVerificationCodes, identityRemover entity.IdentityRemover, passkeyLinks PasskeyRegistrationLinkIssuer) *BackendUserHandler {
	return &BackendUserHandler{emailCodes: emailCodes, identityRemover: identityRemover, passkeyLinks: passkeyLinks}
}

// SendEmailVerification issues a verification code for the identity's email
// address and returns the address and the code, which the caller emails.
func (h *BackendUserHandler) SendEmailVerification(ctx context.Context, req *backenduserv1.SendEmailVerificationRequest) (*backenduserv1.SendEmailVerificationResponse, error) {
	address, code, err := h.emailCodes.IssueEmailVerificationCode(ctx, req.GetProviderUserId().GetValue())
	if err != nil {
		return nil, err
	}
	return &backenduserv1.SendEmailVerificationResponse{
		Email: &entityv1.Email{Value: address},
		Code:  &entityv1.EmailVerificationCode{Value: code},
	}, nil
}

// ResendEmailVerification issues a new verification code for the identity's
// email address, when the fan asks for one, and returns the address and the
// code. Returns FailedPrecondition when the address is already verified.
func (h *BackendUserHandler) ResendEmailVerification(ctx context.Context, req *backenduserv1.ResendEmailVerificationRequest) (*backenduserv1.ResendEmailVerificationResponse, error) {
	address, code, err := h.emailCodes.IssueEmailVerificationCode(ctx, req.GetProviderUserId().GetValue())
	if err != nil {
		return nil, err
	}
	return &backenduserv1.ResendEmailVerificationResponse{
		Email: &entityv1.Email{Value: address},
		Code:  &entityv1.EmailVerificationCode{Value: code},
	}, nil
}

// VerifyEmail marks the identity's email address verified with the code from
// its verification email. The code is never logged.
func (h *BackendUserHandler) VerifyEmail(ctx context.Context, req *backenduserv1.VerifyEmailRequest) (*backenduserv1.VerifyEmailResponse, error) {
	if err := h.emailCodes.VerifyEmail(ctx, req.GetProviderUserId().GetValue(), req.GetCode().GetValue()); err != nil {
		return nil, err
	}
	return &backenduserv1.VerifyEmailResponse{}, nil
}

// IssuePasskeyRegistrationLink returns the identity's verified email address
// and a passkey registration link. The link is a credential and is never
// logged.
func (h *BackendUserHandler) IssuePasskeyRegistrationLink(ctx context.Context, req *backenduserv1.IssuePasskeyRegistrationLinkRequest) (*backenduserv1.IssuePasskeyRegistrationLinkResponse, error) {
	address, link, err := h.passkeyLinks.IssuePasskeyRegistrationLink(ctx, req.GetProviderUserId().GetValue())
	if err != nil {
		return nil, err
	}
	return &backenduserv1.IssuePasskeyRegistrationLinkResponse{
		Email: &entityv1.Email{Value: address},
		Link:  &entityv1.Url{Value: link},
	}, nil
}

// DeleteIdentity removes the identity. An identity already gone counts as
// removed.
func (h *BackendUserHandler) DeleteIdentity(ctx context.Context, req *backenduserv1.DeleteIdentityRequest) (*backenduserv1.DeleteIdentityResponse, error) {
	if err := h.identityRemover.DeleteIdentity(ctx, req.GetProviderUserId().GetValue()); err != nil {
		return nil, err
	}
	return &backenduserv1.DeleteIdentityResponse{}, nil
}
