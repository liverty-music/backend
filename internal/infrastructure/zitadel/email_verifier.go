// Package zitadel provides infrastructure-layer integration with the Zitadel
// identity management API for email verification operations.
package zitadel

import (
	"context"
	"fmt"
	"net/url"

	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/zitadel-go/v3/pkg/client/middleware"
	userv2 "github.com/zitadel/zitadel-go/v3/pkg/client/user/v2"
	zitadelconn "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel"
	userpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/user/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"

	grpccodes "google.golang.org/grpc/codes"
)

// emailCodeClient is the subset of the Zitadel UserServiceClient that
// EmailVerifier needs. Extracting this narrow interface allows unit testing
// without a real gRPC connection.
type emailCodeClient interface {
	SendEmailCode(ctx context.Context, in *userpb.SendEmailCodeRequest, opts ...grpc.CallOption) (*userpb.SendEmailCodeResponse, error)
	ResendEmailCode(ctx context.Context, in *userpb.ResendEmailCodeRequest, opts ...grpc.CallOption) (*userpb.ResendEmailCodeResponse, error)
}

// Compile-time interface compliance check.
var _ usecase.EmailVerifier = (*EmailVerifier)(nil)

// EmailVerifier calls the Zitadel User Service v2 API to send and resend
// email verification codes.
type EmailVerifier struct {
	client emailCodeClient
	logger *logging.Logger
}

// NewEmailVerifier creates a new EmailVerifier that authenticates to the
// Zitadel API using a machine user's private key JWT.
//
// issuerURL is the OIDC issuer URL (e.g., "https://dev-svijfm.us1.zitadel.cloud").
// keyPath is the file path to the machine key JSON.
// opts are additional zitadel connection options (e.g., WithInsecure for testing).
func NewEmailVerifier(ctx context.Context, issuerURL, keyPath string, logger *logging.Logger, opts ...zitadelconn.Option) (*EmailVerifier, error) {
	apiEndpoint, err := grpcEndpoint(issuerURL)
	if err != nil {
		return nil, fmt.Errorf("parse zitadel domain: %w", err)
	}

	connOpts := []zitadelconn.Option{
		zitadelconn.WithJWTProfileTokenSource(
			middleware.JWTProfileFromPath(ctx, keyPath),
		),
	}
	connOpts = append(connOpts, opts...)

	userClient, err := userv2.NewClient(
		ctx,
		issuerURL,
		apiEndpoint,
		[]string{oidc.ScopeOpenID, zitadelconn.ScopeZitadelAPI()},
		connOpts...,
	)
	if err != nil {
		return nil, fmt.Errorf("create zitadel user client: %w", err)
	}

	return &EmailVerifier{
		client: userClient,
		logger: logger,
	}, nil
}

// SendVerification triggers a verification email for the given Zitadel user.
func (v *EmailVerifier) SendVerification(ctx context.Context, externalID string) error {
	_, err := v.client.SendEmailCode(ctx, &userpb.SendEmailCodeRequest{
		UserId: externalID,
	})
	if err != nil {
		return apperr.Wrap(err, codes.Internal, "send email verification code")
	}
	return nil
}

// ResendVerification resends a verification email for the given Zitadel user.
// Returns FailedPrecondition if the email is already verified.
func (v *EmailVerifier) ResendVerification(ctx context.Context, externalID string) error {
	_, err := v.client.ResendEmailCode(ctx, &userpb.ResendEmailCodeRequest{
		UserId: externalID,
	})
	if err != nil {
		if st, ok := status.FromError(err); ok && st.Code() == grpccodes.FailedPrecondition {
			return apperr.Wrap(err, codes.FailedPrecondition, "email is already verified")
		}
		return apperr.Wrap(err, codes.Internal, "resend email verification code")
	}
	return nil
}

// grpcEndpoint extracts the host:port gRPC endpoint from an OIDC issuer URL.
// The issuer URL uses https:// scheme, but gRPC Dial expects host:port.
func grpcEndpoint(issuerURL string) (string, error) {
	u, err := url.Parse(issuerURL)
	if err != nil {
		return "", fmt.Errorf("invalid issuer URL %q: %w", issuerURL, err)
	}

	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("issuer URL %q has no host", issuerURL)
	}

	port := u.Port()
	if port == "" {
		port = "443"
	}

	return host + ":" + port, nil
}
