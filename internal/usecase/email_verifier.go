package usecase

import "context"

// EmailVerifier sends and resends email verification codes via the identity
// provider. Implementations call the Zitadel Management API.
type EmailVerifier interface {
	// SendVerification triggers a verification email for the given Zitadel user.
	//
	// # Possible errors
	//
	//  - Unavailable: The Zitadel API client is not configured.
	SendVerification(ctx context.Context, externalID string) error

	// ResendVerification resends a verification email for the given Zitadel user.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: The user's email is already verified.
	//  - Unavailable: The Zitadel API client is not configured.
	ResendVerification(ctx context.Context, externalID string) error
}
