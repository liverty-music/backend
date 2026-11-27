// Package zitadel provides infrastructure-layer integration with the Zitadel
// identity management API for email verification operations.
package zitadel

import (
	"context"
	"log/slog"

	"github.com/liverty-music/backend/internal/infrastructure/zitadel/route"
	zitadelconn "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel"
	userpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/user/v2"
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
// r is the route to the sign-in service. keyPath is the file path to the
// machine key JSON. opts are additional zitadel connection options (e.g., to
// point a test at its own server).
func NewEmailVerifier(ctx context.Context, r route.Route, keyPath string, logger *logging.Logger, opts ...zitadelconn.Option) (*EmailVerifier, error) {
	conn, err := dial(ctx, r, keyPath, opts...)
	if err != nil {
		return nil, err
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
	if tokenUnreachable(err) {
		return apperr.Wrap(err, codes.Unavailable, msg, attrs...)
	}
	switch status.Code(err) {
	case grpccodes.NotFound:
		return apperr.Wrap(err, codes.NotFound, "identity not found", attrs...)
	case grpccodes.Unavailable, grpccodes.DeadlineExceeded:
		return apperr.Wrap(err, codes.Unavailable, msg, attrs...)
	default:
		return apperr.Wrap(err, codes.Internal, msg, attrs...)
	}
}
