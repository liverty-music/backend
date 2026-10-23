// Package identity calls the identity service, the backend workload that holds
// the identity-provider credentials, over its cluster-internal Connect API. Its
// clients implement the same interfaces as the Zitadel clients they replace on
// the event consumer and the admin console API, and return the same error
// codes, so the usecases do not change.
package identity

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	backendorganizationv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/backend/organization/v1/organizationv1connect"
	backenduserv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/v2/liverty_music/rpc/backend/user/v1/userv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	backendorganizationv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/backend/organization/v1"
	backenduserv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/backend/user/v1"
	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/otelconnect"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/mail"
)

// Compile-time interface compliance checks.
var (
	_ mail.EmailVerificationCodeIssuer   = (*UserClient)(nil)
	_ entity.EmailCodeVerifier           = (*UserClient)(nil)
	_ entity.IdentityRemover             = (*UserClient)(nil)
	_ mail.PasskeyRegistrationLinkIssuer = (*UserClient)(nil)
	_ entity.OrganizationRepository      = (*OrganizationClient)(nil)
)

// newConnectClient returns a Connect client for baseURL (e.g.
// http://identity:9100) that speaks HTTP/2 without TLS: the identity service
// is reached only inside the cluster.
func newConnectClient(baseURL string) *connect.Client {
	p := new(http.Protocols)
	p.SetUnencryptedHTTP2(true)
	httpClient := &http.Client{Transport: &http.Transport{Protocols: p}}
	// otelconnect fails only on invalid options, and none are passed; on
	// failure the client calls without tracing rather than not at all.
	var interceptors []connect.ClientInterceptor
	if tracing, err := otelconnect.NewClientInterceptor(); err == nil {
		interceptors = append(interceptors, tracing)
	}
	return connect.NewClient(connecthttp.NewTransport(httpClient, baseURL), interceptors...)
}

// toAppErr maps an identity service error to the apperr code it carries, so
// the caller sees the codes the Zitadel clients returned. The message of a
// client error (e.g. the owner identity's user name in use) is kept; a
// failure to reach the identity service is Unavailable.
func toAppErr(err error, msg string, attrs ...slog.Attr) error {
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return apperr.Wrap(err, codes.Unavailable, msg, attrs...)
	}
	code := codes.Code(connectErr.Code())
	if !code.IsServerError() && connectErr.Message() != "" {
		// The identity service's message is the one the Zitadel client gave,
		// which apperr already ended with the code; New adds it back, and
		// wrapping would also append the cause.
		return apperr.New(code, strings.TrimSuffix(connectErr.Message(), " ("+code.String()+")"), attrs...)
	}
	return apperr.Wrap(err, code, msg, attrs...)
}

// UserClient changes a fan's identity through the identity service's
// UserService. The event consumer uses it for verification codes and passkey
// registration links, the fan API to verify an email address with a code, and
// the admin console API for identity removal.
type UserClient struct {
	client backenduserv1connect.UserServiceClient
}

// NewUserClient creates a UserClient for the identity service listener at
// baseURL.
func NewUserClient(baseURL string) *UserClient {
	return &UserClient{client: backenduserv1connect.NewUserServiceClient(newConnectClient(baseURL))}
}

func userIDOf(id string) *entityv1.User_ProviderUserId {
	return &entityv1.User_ProviderUserId{Value: id}
}

// IssueEmailVerificationCode implements [mail.EmailVerificationCodeIssuer]
// with SendEmailVerification. The code is a credential and is never logged.
func (c *UserClient) IssueEmailVerificationCode(ctx context.Context, providerUserID string) (address, code string, err error) {
	resp, err := c.client.SendEmailVerification(ctx, &backenduserv1.SendEmailVerificationRequest{ProviderUserId: userIDOf(providerUserID)})
	if err != nil {
		return "", "", toAppErr(err, "send email verification code", slog.String("provider_user_id", providerUserID))
	}
	return resp.GetEmail().GetValue(), resp.GetCode().GetValue(), nil
}

// ReissueEmailVerificationCode implements [mail.EmailVerificationCodeIssuer]
// with ResendEmailVerification. The code is a credential and is never logged.
func (c *UserClient) ReissueEmailVerificationCode(ctx context.Context, providerUserID string) (address, code string, err error) {
	resp, err := c.client.ResendEmailVerification(ctx, &backenduserv1.ResendEmailVerificationRequest{ProviderUserId: userIDOf(providerUserID)})
	if err != nil {
		return "", "", toAppErr(err, "resend email verification code", slog.String("provider_user_id", providerUserID))
	}
	return resp.GetEmail().GetValue(), resp.GetCode().GetValue(), nil
}

// VerifyEmail implements [entity.EmailCodeVerifier]. The code is never logged.
func (c *UserClient) VerifyEmail(ctx context.Context, providerUserID, code string) error {
	_, err := c.client.VerifyEmail(ctx, &backenduserv1.VerifyEmailRequest{
		ProviderUserId: userIDOf(providerUserID),
		Code:           &entityv1.EmailVerificationCode{Value: code},
	})
	if err != nil {
		return toAppErr(err, "verify email", slog.String("provider_user_id", providerUserID))
	}
	return nil
}

// IssuePasskeyRegistrationLink implements [mail.PasskeyRegistrationLinkIssuer].
// The link is a credential and is never logged.
func (c *UserClient) IssuePasskeyRegistrationLink(ctx context.Context, providerUserID string) (address, link string, err error) {
	resp, err := c.client.IssuePasskeyRegistrationLink(ctx, &backenduserv1.IssuePasskeyRegistrationLinkRequest{ProviderUserId: userIDOf(providerUserID)})
	if err != nil {
		return "", "", toAppErr(err, "issue passkey registration link", slog.String("provider_user_id", providerUserID))
	}
	return resp.GetEmail().GetValue(), resp.GetLink().GetValue(), nil
}

// DeleteIdentity implements [entity.IdentityRemover].
func (c *UserClient) DeleteIdentity(ctx context.Context, providerUserID string) error {
	_, err := c.client.DeleteIdentity(ctx, &backenduserv1.DeleteIdentityRequest{ProviderUserId: userIDOf(providerUserID)})
	if err != nil {
		return toAppErr(err, "delete identity", slog.String("provider_user_id", providerUserID))
	}
	return nil
}

// OrganizationClient creates, provisions and removes Organizers'
// Organizations through the identity service's OrganizationService, for the
// admin console API.
type OrganizationClient struct {
	client backendorganizationv1connect.OrganizationServiceClient
}

// NewOrganizationClient creates an OrganizationClient for the identity service
// listener at baseURL.
func NewOrganizationClient(baseURL string) *OrganizationClient {
	return &OrganizationClient{client: backendorganizationv1connect.NewOrganizationServiceClient(newConnectClient(baseURL))}
}

func organizerIDOf(id string) *entityv1.Organizer_ProviderOrganizerId {
	return &entityv1.Organizer_ProviderOrganizerId{Value: id}
}

// Create implements [entity.OrganizationRepository].
func (c *OrganizationClient) Create(ctx context.Context, shortName string) (string, error) {
	resp, err := c.client.Create(ctx, &backendorganizationv1.CreateRequest{ShortName: &entityv1.OrganizerShortName{Value: shortName}})
	if err != nil {
		return "", toAppErr(err, "create organization", slog.String("short_name", shortName))
	}
	return resp.GetProviderOrganizerId().GetValue(), nil
}

// GetByName implements [entity.OrganizationRepository].
func (c *OrganizationClient) GetByName(ctx context.Context, shortName string) (string, error) {
	resp, err := c.client.Get(ctx, &backendorganizationv1.GetRequest{ShortName: &entityv1.OrganizerShortName{Value: shortName}})
	if err != nil {
		return "", toAppErr(err, "get organization by name", slog.String("short_name", shortName))
	}
	return resp.GetProviderOrganizerId().GetValue(), nil
}

// Provision implements [entity.OrganizationRepository].
func (c *OrganizationClient) Provision(ctx context.Context, providerOrganizerID, shortName, ownerEmail string) error {
	_, err := c.client.Provision(ctx, &backendorganizationv1.ProvisionRequest{
		ProviderOrganizerId: organizerIDOf(providerOrganizerID),
		ShortName:           &entityv1.OrganizerShortName{Value: shortName},
		OwnerEmail:          &entityv1.Email{Value: ownerEmail},
	})
	if err != nil {
		return toAppErr(err, "provision organization", slog.String("provider_organizer_id", providerOrganizerID))
	}
	return nil
}

// DeactivateIdentities implements [entity.OrganizationRepository].
func (c *OrganizationClient) DeactivateIdentities(ctx context.Context, providerOrganizerID string) error {
	_, err := c.client.DeactivateIdentities(ctx, &backendorganizationv1.DeactivateIdentitiesRequest{ProviderOrganizerId: organizerIDOf(providerOrganizerID)})
	if err != nil {
		return toAppErr(err, "deactivate organization identities", slog.String("provider_organizer_id", providerOrganizerID))
	}
	return nil
}

// Delete implements [entity.OrganizationRepository].
func (c *OrganizationClient) Delete(ctx context.Context, providerOrganizerID string) error {
	_, err := c.client.Delete(ctx, &backendorganizationv1.DeleteRequest{ProviderOrganizerId: organizerIDOf(providerOrganizerID)})
	if err != nil {
		return toAppErr(err, "delete organization", slog.String("provider_organizer_id", providerOrganizerID))
	}
	return nil
}
