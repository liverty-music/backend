// Package zitadel provides infrastructure-layer integration with the Zitadel
// identity management API for email verification operations.
package zitadel

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"

	"github.com/zitadel/oidc/v3/pkg/oidc"
	zitadelconn "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel"
	userpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/user/v2"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	grpccodes "google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// EmailCodeClient is the subset of the v2 UserServiceClient the
// EmailVerifier uses.
type EmailCodeClient interface {
	GetUserByID(ctx context.Context, in *userpb.GetUserByIDRequest, opts ...grpc.CallOption) (*userpb.GetUserByIDResponse, error)
	SendEmailCode(ctx context.Context, in *userpb.SendEmailCodeRequest, opts ...grpc.CallOption) (*userpb.SendEmailCodeResponse, error)
	VerifyEmail(ctx context.Context, in *userpb.VerifyEmailRequest, opts ...grpc.CallOption) (*userpb.VerifyEmailResponse, error)
}

// EmailVerifier issues and checks email verification codes through the v2
// User Service. Zitadel sends no email: the code is returned to the caller,
// which emails it. A code is a credential, so it is never logged.
type EmailVerifier struct {
	client EmailCodeClient
	logger *logging.Logger
}

// NewEmailVerifier creates a new EmailVerifier that authenticates to the
// Zitadel API using a machine user's private key JWT.
//
// issuerURL is the OIDC issuer URL (e.g., "https://auth.dev.liverty-music.app").
// keyPath is the file path to the machine key JSON.
// opts are additional zitadel connection options (e.g., WithInsecure for testing).
func NewEmailVerifier(ctx context.Context, issuerURL, keyPath string, logger *logging.Logger, opts ...zitadelconn.Option) (*EmailVerifier, error) {
	apiEndpoint, err := grpcEndpoint(issuerURL)
	if err != nil {
		return nil, fmt.Errorf("parse zitadel domain: %w", err)
	}

	connOpts := []zitadelconn.Option{
		zitadelconn.WithJWTProfileTokenSource(
			jwtProfileFromPath(ctx, keyPath),
		),
		zitadelconn.WithDialOptions(
			grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
		),
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

	return NewEmailVerifierWithClient(userpb.NewUserServiceClient(conn.ClientConn), logger), nil
}

// NewEmailVerifierWithClient creates an EmailVerifier around the given client
// instead of a live Zitadel connection (used by tests).
func NewEmailVerifierWithClient(client EmailCodeClient, logger *logging.Logger) *EmailVerifier {
	return &EmailVerifier{client: client, logger: logger}
}

// IssueEmailVerificationCode issues a new verification code for the email
// address of the identity providerUserID and returns the address and the
// code. Earlier codes stop working. SendEmailCode does not check whether the
// address is already verified, so the identity is read first.
//
// # Possible errors
//
//   - NotFound: the identity does not exist (or is not a human identity).
//   - FailedPrecondition: the address is already verified; no code is issued.
//   - Unavailable: Zitadel cannot be reached.
//   - Internal: any other failure.
func (v *EmailVerifier) IssueEmailVerificationCode(ctx context.Context, providerUserID string) (address, code string, err error) {
	attrs := slog.String("provider_user_id", providerUserID)
	resp, err := v.client.GetUserByID(ctx, &userpb.GetUserByIDRequest{UserId: providerUserID})
	if err != nil {
		return "", "", wrapGRPC(err, "get zitadel user", attrs)
	}
	email := resp.GetUser().GetHuman().GetEmail()
	if email.GetEmail() == "" {
		return "", "", apperr.New(codes.NotFound, "identity has no email address", attrs)
	}
	if email.GetIsVerified() {
		return "", "", apperr.New(codes.FailedPrecondition, "email is already verified", attrs)
	}

	sent, err := v.client.SendEmailCode(ctx, &userpb.SendEmailCodeRequest{
		UserId:       providerUserID,
		Verification: &userpb.SendEmailCodeRequest_ReturnCode{ReturnCode: &userpb.ReturnEmailVerificationCode{}},
	})
	if err != nil {
		return "", "", wrapGRPC(err, "issue email verification code", attrs)
	}
	if sent.GetVerificationCode() == "" {
		return "", "", apperr.New(codes.Internal, "zitadel returned no email verification code", attrs)
	}
	v.logger.Info(ctx, "email verification code issued", attrs)
	return email.GetEmail(), sent.GetVerificationCode(), nil
}

// VerifyEmail marks the email address of the identity providerUserID verified
// with code. A wrong, expired or used code changes nothing.
//
// # Possible errors
//
//   - InvalidArgument: the code is wrong, expired or already used, or the
//     address is already verified.
//   - NotFound: the identity does not exist.
//   - Unavailable: Zitadel cannot be reached.
//   - Internal: any other failure.
func (v *EmailVerifier) VerifyEmail(ctx context.Context, providerUserID, code string) error {
	attrs := slog.String("provider_user_id", providerUserID)
	_, err := v.client.VerifyEmail(ctx, &userpb.VerifyEmailRequest{UserId: providerUserID, VerificationCode: code})
	if err != nil {
		// Zitadel answers a wrong code with InvalidArgument and an expired or
		// missing code, or an address already verified, with
		// FailedPrecondition: all mean this code cannot verify.
		if st, ok := status.FromError(err); ok && (st.Code() == grpccodes.InvalidArgument || st.Code() == grpccodes.FailedPrecondition) {
			return apperr.Wrap(err, codes.InvalidArgument, "email verification code is not valid", attrs)
		}
		return wrapGRPC(err, "verify email", attrs)
	}
	v.logger.Info(ctx, "email verified", attrs)
	return nil
}

// wrapGRPC maps a Zitadel gRPC error: NotFound stays NotFound, an unreachable
// Zitadel is Unavailable and anything else is Internal.
func wrapGRPC(err error, msg string, attrs ...slog.Attr) error {
	switch status.Code(err) {
	case grpccodes.NotFound:
		return apperr.Wrap(err, codes.NotFound, "identity not found", attrs...)
	case grpccodes.Unavailable, grpccodes.DeadlineExceeded:
		return apperr.Wrap(err, codes.Unavailable, msg, attrs...)
	default:
		return apperr.Wrap(err, codes.Internal, msg, attrs...)
	}
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
