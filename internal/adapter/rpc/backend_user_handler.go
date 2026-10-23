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

// BackendUserHandler implements the identity service's UserService, which the
// event consumer and the admin console API call to change a fan's identity at
// the identity provider. The identity service holds the identity-provider
// credentials and has no business rules of its own, so each method calls one
// identity-provider client and maps its result. Which caller may call which
// procedure is enforced by each listener's allowlist, and the request is
// validated by the server's protovalidate interceptor.
type BackendUserHandler struct {
	emailVerifier   entity.EmailVerifier
	identityRemover entity.IdentityRemover
	passkeyLinks    PasskeyRegistrationLinkIssuer
}

// NewBackendUserHandler creates a new BackendUserHandler.
func NewBackendUserHandler(emailVerifier entity.EmailVerifier, identityRemover entity.IdentityRemover, passkeyLinks PasskeyRegistrationLinkIssuer) *BackendUserHandler {
	return &BackendUserHandler{emailVerifier: emailVerifier, identityRemover: identityRemover, passkeyLinks: passkeyLinks}
}

// SendEmailVerification sends the identity's email address a verification code.
func (h *BackendUserHandler) SendEmailVerification(ctx context.Context, req *backenduserv1.SendEmailVerificationRequest) (*backenduserv1.SendEmailVerificationResponse, error) {
	if err := h.emailVerifier.SendVerification(ctx, req.GetProviderUserId().GetValue()); err != nil {
		return nil, err
	}
	return &backenduserv1.SendEmailVerificationResponse{}, nil
}

// ResendEmailVerification sends the identity's email address a new
// verification code. Returns FailedPrecondition when it is already verified.
func (h *BackendUserHandler) ResendEmailVerification(ctx context.Context, req *backenduserv1.ResendEmailVerificationRequest) (*backenduserv1.ResendEmailVerificationResponse, error) {
	if err := h.emailVerifier.ResendVerification(ctx, req.GetProviderUserId().GetValue()); err != nil {
		return nil, err
	}
	return &backenduserv1.ResendEmailVerificationResponse{}, nil
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
