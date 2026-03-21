// Package zitadel provides infrastructure-layer integration with the Zitadel
// identity management API for email verification operations.
package zitadel

import (
	"context"
	"fmt"

	"github.com/zitadel/zitadel-go/v3/pkg/client/middleware"
	userv2 "github.com/zitadel/zitadel-go/v3/pkg/client/user/v2"
	zitadelconn "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel"
	userpb "github.com/zitadel/zitadel-go/v3/pkg/client/zitadel/user/v2"
	"google.golang.org/grpc/status"

	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"

	grpccodes "google.golang.org/grpc/codes"
)

// Compile-time interface compliance check.
var _ usecase.EmailVerifier = (*EmailVerifier)(nil)

// EmailVerifier calls the Zitadel User Service v2 API to send and resend
// email verification codes.
type EmailVerifier struct {
	client *userv2.Client
	logger *logging.Logger
}

// NewEmailVerifier creates a new EmailVerifier that authenticates to the
// Zitadel API using a machine user's private key JWT.
//
// domain is the Zitadel instance URL (e.g., "https://dev-svijfm.us1.zitadel.cloud").
// keyPath is the file path to the machine key JSON.
func NewEmailVerifier(ctx context.Context, domain, keyPath string, logger *logging.Logger) (*EmailVerifier, error) {
	userClient, err := userv2.NewClient(
		ctx,
		domain,
		domain,
		nil,
		zitadelconn.WithJWTProfileTokenSource(
			middleware.JWTProfileFromPath(ctx, keyPath),
		),
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
