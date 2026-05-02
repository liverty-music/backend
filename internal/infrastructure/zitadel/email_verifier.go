// Package zitadel provides infrastructure-layer integration with the Zitadel
// identity management API for email verification operations.
package zitadel

import (
	"context"
	"fmt"
	"net/url"

	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/zitadel-go/v3/pkg/client/middleware"
	zitadelconn "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel"
	mgmtpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/management"
	userpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/user/v2"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	"log/slog"

	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"

	grpccodes "google.golang.org/grpc/codes"
)

// emailSendClient is the subset of the v2 UserServiceClient used by
// SendVerification (initial verification email at sign-up time).
type emailSendClient interface {
	SendEmailCode(ctx context.Context, in *userpb.SendEmailCodeRequest, opts ...grpc.CallOption) (*userpb.SendEmailCodeResponse, error)
}

// emailResendClient is the subset of the v1 ManagementServiceClient used by
// ResendVerification (post-sign-up "Resend verification email" button).
//
// v1 Management ResendHumanEmailVerification is used instead of v2
// ResendEmailCode because v2 only resends an EXISTING code: if SMTP was
// inactive at sign-up time (the §13.16 cutover incident path), no code was
// ever generated and v2 fails with `Code is empty (EMAIL-5w5ilin4yt)`.
// v1 generates a fresh code AND sends the email, which matches the
// user-intent of the Settings-page "Resend verification email" button.
// See `cutover-warning-fixes` design doc D1 for the full rationale.
type emailResendClient interface {
	ResendHumanEmailVerification(ctx context.Context, in *mgmtpb.ResendHumanEmailVerificationRequest, opts ...grpc.CallOption) (*mgmtpb.ResendHumanEmailVerificationResponse, error)
}

// Compile-time interface compliance check.
var _ usecase.EmailVerifier = (*EmailVerifier)(nil)

// EmailVerifier calls Zitadel APIs to send and resend email verification
// codes. SendVerification uses the v2 User Service; ResendVerification uses
// the v1 Management Service (see emailResendClient docstring).
type EmailVerifier struct {
	sendClient   emailSendClient
	resendClient emailResendClient
	logger       *logging.Logger
}

// NewEmailVerifier creates a new EmailVerifier that authenticates to the
// Zitadel API using a machine user's private key JWT. A single underlying
// gRPC connection is shared between the v2 User Service and v1 Management
// Service stubs, so there is exactly one auth/refresh goroutine per process.
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
			middleware.JWTProfileFromPath(ctx, keyPath),
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

	return &EmailVerifier{
		sendClient:   userpb.NewUserServiceClient(conn.ClientConn),
		resendClient: mgmtpb.NewManagementServiceClient(conn.ClientConn),
		logger:       logger,
	}, nil
}

// SendVerification triggers a verification email for the given Zitadel user.
func (v *EmailVerifier) SendVerification(ctx context.Context, externalID string) error {
	_, err := v.sendClient.SendEmailCode(ctx, &userpb.SendEmailCodeRequest{
		UserId: externalID,
	})
	if err != nil {
		return apperr.Wrap(err, codes.Internal, "send email verification code")
	}
	v.logger.Info(ctx, "email verification sent",
		slog.String("external_id", externalID),
	)
	return nil
}

// ResendVerification resends a verification email for the given Zitadel user.
// Always succeeds for users whose email is unverified, including the case
// where SMTP was inactive at sign-up time and no prior code exists — the v1
// Management endpoint generates a fresh code and sends the email.
// Returns FailedPrecondition if the email is already verified.
func (v *EmailVerifier) ResendVerification(ctx context.Context, externalID string) error {
	_, err := v.resendClient.ResendHumanEmailVerification(ctx, &mgmtpb.ResendHumanEmailVerificationRequest{
		UserId: externalID,
	})
	if err != nil {
		v.logger.Error(ctx, "failed to resend email verification", err,
			slog.String("external_id", externalID),
		)
		if st, ok := status.FromError(err); ok && st.Code() == grpccodes.FailedPrecondition {
			return apperr.Wrap(err, codes.FailedPrecondition, "email is already verified")
		}
		return apperr.Wrap(err, codes.Internal, "resend email verification code")
	}
	v.logger.Info(ctx, "email verification resent",
		slog.String("external_id", externalID),
	)
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
