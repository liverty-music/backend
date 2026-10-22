package zitadel

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/zitadel/oidc/v3/pkg/oidc"
	zitadelconn "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel"
	userpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/user/v2"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// PasskeyRegistrationLinkClient is the subset of the v2 UserServiceClient the
// PasskeyRegistrationLinkIssuer uses.
type PasskeyRegistrationLinkClient interface {
	GetUserByID(ctx context.Context, in *userpb.GetUserByIDRequest, opts ...grpc.CallOption) (*userpb.GetUserByIDResponse, error)
	CreatePasskeyRegistrationLink(ctx context.Context, in *userpb.CreatePasskeyRegistrationLinkRequest, opts ...grpc.CallOption) (*userpb.CreatePasskeyRegistrationLinkResponse, error)
}

// PasskeyRegistrationLinkIssuer issues the one-time passkey registration
// code of an identity through the v2 User Service and builds the hosted
// login link that registers a passkey with it. It authenticates as the
// fan-passkey-recovery machine user, whose ORG_OWNER membership on the product
// org carries user.passkey.write (no narrower org role does). Only the event
// consumer builds it.
//
// The code is valid for 1 hour and is consumed when a registration starts.
// The link carries it, so neither the code nor the link is ever logged.
type PasskeyRegistrationLinkIssuer struct {
	client PasskeyRegistrationLinkClient
	// loginBaseURL is the hosted login origin, the issuer's scheme and host
	// (e.g. https://auth.liverty-music.app).
	loginBaseURL string
	logger       *logging.Logger
}

// NewPasskeyRegistrationLinkIssuer creates an issuer that authenticates to
// the Zitadel API with the machine key at keyPath, like NewEmailVerifier.
// Construction makes no network request.
func NewPasskeyRegistrationLinkIssuer(ctx context.Context, issuerURL, keyPath string, logger *logging.Logger, opts ...zitadelconn.Option) (*PasskeyRegistrationLinkIssuer, error) {
	apiEndpoint, err := grpcEndpoint(issuerURL)
	if err != nil {
		return nil, fmt.Errorf("parse zitadel domain: %w", err)
	}

	connOpts := []zitadelconn.Option{
		zitadelconn.WithJWTProfileTokenSource(jwtProfileFromPath(ctx, keyPath)),
		zitadelconn.WithDialOptions(grpc.WithStatsHandler(otelgrpc.NewClientHandler())),
	}
	connOpts = append(connOpts, opts...)

	conn, err := zitadelconn.NewConnection(
		ctx,
		issuerURL,
		apiEndpoint,
		[]string{oidc.ScopeOpenID, zitadelconn.ScopeZitadelAPI()},
		connOpts...,
	)
	if err != nil {
		return nil, fmt.Errorf("create zitadel connection: %w", err)
	}

	return NewPasskeyRegistrationLinkIssuerWithClient(userpb.NewUserServiceClient(conn.ClientConn), issuerURL, logger)
}

// NewPasskeyRegistrationLinkIssuerWithClient creates an issuer around the
// given client instead of a live Zitadel connection (used by tests of the
// senders built on it). issuerURL gives the hosted login origin.
func NewPasskeyRegistrationLinkIssuerWithClient(client PasskeyRegistrationLinkClient, issuerURL string, logger *logging.Logger) (*PasskeyRegistrationLinkIssuer, error) {
	u, err := url.Parse(issuerURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid issuer URL %q", issuerURL)
	}
	return &PasskeyRegistrationLinkIssuer{
		client:       client,
		loginBaseURL: u.Scheme + "://" + u.Host,
		logger:       logger,
	}, nil
}

// IssuePasskeyRegistrationLink reads the identity providerUserID and, when its
// email address is verified, issues a passkey registration code for it. It
// returns the verified address and the hosted login link that registers a
// passkey with the code without a sign-in:
// <login origin>/ui/v2/login/passkey/set?userId=…&code=…&codeId=…&organization=<the identity's organization>.
//
// # Possible errors
//
//   - NotFound: the identity does not exist (or is not a human identity).
//   - FailedPrecondition: the identity has no verified email address; no
//     code is issued.
//   - Internal: any other failure.
func (i *PasskeyRegistrationLinkIssuer) IssuePasskeyRegistrationLink(ctx context.Context, providerUserID string) (address, link string, err error) {
	attrs := slog.String("provider_user_id", providerUserID)
	resp, err := i.client.GetUserByID(ctx, &userpb.GetUserByIDRequest{UserId: providerUserID})
	if err != nil {
		if isNotFound(err) {
			return "", "", apperr.Wrap(err, codes.NotFound, "identity not found", attrs)
		}
		return "", "", apperr.Wrap(err, codes.Internal, "get zitadel user", attrs)
	}
	user := resp.GetUser()
	human := user.GetHuman()
	if human == nil {
		return "", "", apperr.New(codes.NotFound, "identity is not a human user", attrs)
	}
	email := human.GetEmail()
	if email.GetEmail() == "" || !email.GetIsVerified() {
		return "", "", apperr.New(codes.FailedPrecondition, "identity's email address is not verified", attrs)
	}
	organization := user.GetDetails().GetResourceOwner()
	if organization == "" {
		return "", "", apperr.New(codes.Internal, "identity has no organization", attrs)
	}

	created, err := i.client.CreatePasskeyRegistrationLink(ctx, &userpb.CreatePasskeyRegistrationLinkRequest{
		UserId: providerUserID,
		Medium: &userpb.CreatePasskeyRegistrationLinkRequest_ReturnCode{
			ReturnCode: &userpb.ReturnPasskeyRegistrationCode{},
		},
	})
	if err != nil {
		if isNotFound(err) {
			return "", "", apperr.Wrap(err, codes.NotFound, "identity not found", attrs)
		}
		return "", "", apperr.Wrap(err, codes.Internal, "create passkey registration link", attrs)
	}
	code := created.GetCode()
	if code.GetId() == "" || code.GetCode() == "" {
		return "", "", apperr.New(codes.Internal, "zitadel returned no passkey registration code", attrs)
	}

	query := url.Values{}
	query.Set("userId", providerUserID)
	query.Set("code", code.GetCode())
	query.Set("codeId", code.GetId())
	query.Set("organization", organization)
	i.logger.Info(ctx, "passkey registration code issued", attrs)
	return email.GetEmail(), i.loginBaseURL + "/ui/v2/login/passkey/set?" + query.Encode(), nil
}

// FanWebBaseURLFromIssuer derives the fan web origin from the Zitadel issuer
// URL by dropping the "auth." host prefix: https://auth.liverty-music.app →
// https://liverty-music.app, https://auth.dev.liverty-music.app →
// https://dev.liverty-music.app.
func FanWebBaseURLFromIssuer(issuerURL string) (string, error) {
	u, err := url.Parse(issuerURL)
	if err != nil {
		return "", fmt.Errorf("parse issuer url %q: %w", issuerURL, err)
	}
	host, ok := strings.CutPrefix(u.Host, "auth.")
	if !ok {
		return "", fmt.Errorf("issuer host %q does not start with 'auth.'", u.Host)
	}
	return fmt.Sprintf("%s://%s", u.Scheme, host), nil
}
