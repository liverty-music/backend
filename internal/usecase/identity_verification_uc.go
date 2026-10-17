package usecase

import (
	"context"
	"errors"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// IdentityVerifierResult is the result of a successful Pocket Sign Stamp verification.
// The raw certificate and card response are validated vendor-side and never
// cross the boundary into our application; only these fields are retained.
type IdentityVerifierResult struct {
	// ProviderUserID is the Pocket Sign person key (User.id), scoped to
	// Liverty Music's account at the verification provider.
	// Stable across card renewal, re-issue, cert-type change. Never the serial
	// or 個人番号.
	ProviderUserID string
	// Method is which credential was used (JPKI or driver's licence).
	Method entity.VerificationMethod
}

// IdentityVerifierRecheckResult is the outcome of a 現況確認 (liveness) re-check.
type IdentityVerifierRecheckResult struct {
	// NeedsReverification is true when the Pocket Sign 現況確認 reports the
	// certificate is revoked, 基本4情報 changed, or expired.
	NeedsReverification bool
}

// IdentityVerifier is the interface the identity verification use case uses to interact
// with the Pocket Sign Stamp API.
//
// The interface is defined here (where consumed) per AGENTS.md. Implementations
// live in internal/infrastructure/pocketsign/.
//
// Stamp flow:
//  1. StartVerify — backend calls CreateSession → returns (sessionID, redirectURL).
//     The frontend opens redirectURL in the verification provider's app; the fan reads their
//     card there. The app redirects back to the callback URL with ?session_id=<id>.
//  2. CompleteVerify — backend calls FinalizeSession(sessionID) → verifies the
//     nonce stored at step 1 matches metadata in the response, extracts
//     ProviderUserID.
//  3. Recheck — calls verify.v2.UserService.CheckUserStatus for 現況確認.
type IdentityVerifier interface {
	// StartVerify creates a verification session at the verification provider
	// (Pocket Sign Stamp). It returns the opaque session id and the redirect URL
	// the client must open in the provider's app. A random nonce is generated and stored server-side keyed by
	// (userID, sessionID) for later comparison in CompleteVerify.
	//
	// # Possible errors
	//
	//   - Unavailable: the Pocket Sign Stamp API is unreachable or not configured.
	StartVerify(ctx context.Context, userID string, method entity.VerificationMethod) (sessionID string, redirectURL string, err error)

	// CompleteVerify finalizes the Pocket Sign Stamp session. It calls
	// FinalizeSession, validates the server-stored nonce against the response
	// metadata, checks the verification result, and extracts the
	// ProviderUserID from the userAuthentication result.
	//
	// # Possible errors
	//
	//   - FailedPrecondition: the session is not yet completed (fan has not
	//     finished in the verification provider's app) or the cert failed validation
	//     (revoked/expired/signature mismatch).
	//   - PermissionDenied: nonce mismatch (replay or tamper attempt).
	//   - Unavailable: the Pocket Sign Stamp API is unreachable or not configured.
	CompleteVerify(ctx context.Context, userID, sessionID string) (*IdentityVerifierResult, error)

	// Recheck performs a 現況確認 (liveness) check for the given provider_user_id.
	// It reports whether the IdentityVerification is still current (not revoked,
	// 基本4情報 unchanged, not expired).
	//
	// # Possible errors
	//
	//   - Unavailable: the Pocket Sign Verify API is unreachable or not configured.
	Recheck(ctx context.Context, providerUserID string) (*IdentityVerifierRecheckResult, error)
}

// IdentityVerificationUseCase runs a fan's 本人確認 (identity check) via Pocket Sign and reads
// its result, the fan's [entity.IdentityVerification].
type IdentityVerificationUseCase interface {
	// Start creates a Pocket Sign Stamp session for the calling fan and returns
	// its session id and the redirect URL for the verification provider's app.
	//
	// # Possible errors
	//
	//   - InvalidArgument: the method is unspecified.
	//   - NotFound: the user does not exist.
	//   - Unavailable: the Pocket Sign Stamp API is not configured or
	//     unreachable.
	Start(ctx context.Context, userID string, method entity.VerificationMethod) (sessionID string, redirectURL string, err error)

	// Create finalizes the Stamp session and records the fan's Active IdentityVerification.
	//
	// The raw certificate and card response are validated vendor-side; only the
	// provider_user_id is retained, as required by the privacy spec.
	//
	// # Possible errors
	//
	//   - NotFound: the user does not exist.
	//   - FailedPrecondition: session not yet completed by the fan, or cert invalid.
	//   - PermissionDenied: nonce mismatch (replay/tamper).
	//   - AlreadyExists: the Pocket Sign User.id is already bound to a different active
	//     account (duplicate-person). The error carries a clear recovery-path message.
	//   - Unavailable: the Pocket Sign Stamp API is not configured.
	Create(ctx context.Context, userID, sessionID string) (*entity.IdentityVerification, error)

	// Recheck performs a 現況確認 (liveness) re-check of the fan's IdentityVerification. On a
	// revoked/changed result it flags the IdentityVerification as NEEDS_REVERIFICATION (not a
	// hard lock).
	//
	// # Possible errors
	//
	//   - FailedPrecondition: the user has no IdentityVerification to re-check.
	//   - Unavailable: the Pocket Sign Verify API is not configured.
	Recheck(ctx context.Context, userID string) (*entity.IdentityVerification, error)

	// Get returns the fan's IdentityVerification, whatever its status.
	//
	// # Possible errors
	//
	//   - NotFound: the user has never verified their identity.
	Get(ctx context.Context, userID string) (*entity.IdentityVerification, error)

	// Delete removes the fan's IdentityVerification. Used for the privacy deletion path
	// (purpose-end or valid deletion request), subject to lawful retention
	// obligations.
	//
	// # Possible errors
	//
	//   - NotFound: the user has no IdentityVerification to delete.
	Delete(ctx context.Context, userID string) error
}

// identityVerificationUseCase is the concrete implementation.
type identityVerificationUseCase struct {
	identityVerificationRepo entity.IdentityVerificationRepository
	userRepo                 entity.UserRepository
	verifier                 IdentityVerifier
	logger                   *logging.Logger
}

// NewIdentityVerificationUseCase creates a new IdentityVerificationUseCase.
func NewIdentityVerificationUseCase(
	identityVerificationRepo entity.IdentityVerificationRepository,
	userRepo entity.UserRepository,
	verifier IdentityVerifier,
	logger *logging.Logger,
) IdentityVerificationUseCase {
	return &identityVerificationUseCase{
		identityVerificationRepo: identityVerificationRepo,
		userRepo:                 userRepo,
		verifier:                 verifier,
		logger:                   logger,
	}
}

// Start creates a Pocket Sign Stamp session for the given user.
func (uc *identityVerificationUseCase) Start(
	ctx context.Context,
	userID string,
	method entity.VerificationMethod,
) (string, string, error) {
	// Verify the user exists before creating a session.
	if _, err := uc.userRepo.Get(ctx, userID); err != nil {
		return "", "", err
	}

	if method == entity.VerificationMethodUnspecified {
		return "", "", apperr.New(codes.InvalidArgument, "method must be specified")
	}

	sessionID, redirectURL, err := uc.verifier.StartVerify(ctx, userID, method)
	if err != nil {
		return "", "", err
	}

	uc.logger.Info(ctx, "pocket sign stamp session created",
		slog.String("user_id", userID),
		slog.Int("method", int(method)),
		slog.String("session_id", sessionID),
	)
	return sessionID, redirectURL, nil
}

// Create finalizes the Stamp session and records the IdentityVerification. Only the
// provider_user_id is stored; the raw certificate/response is validated
// vendor-side and never persisted here per the privacy spec.
func (uc *identityVerificationUseCase) Create(
	ctx context.Context,
	userID, sessionID string,
) (*entity.IdentityVerification, error) {
	// Verify the user exists.
	if _, err := uc.userRepo.Get(ctx, userID); err != nil {
		return nil, err
	}

	// Call the Pocket Sign Stamp API to finalize the session. The verifier
	// validates the nonce and cert result; only ProviderUserID + Method cross
	// the boundary.
	result, err := uc.verifier.CompleteVerify(ctx, userID, sessionID)
	if err != nil {
		return nil, err
	}

	// Dedupe: check whether the provider_user_id is already bound to an
	// active account. If it belongs to a DIFFERENT user, reject with
	// AlreadyExists and a clear recovery-path message. If it belongs to THIS
	// user (renewal path), the existing record is deactivated and a fresh one
	// is created.
	existing, err := uc.identityVerificationRepo.GetByProviderUserID(ctx, result.ProviderUserID)
	if err != nil && !errors.Is(err, apperr.ErrNotFound) {
		return nil, err
	}

	if existing != nil {
		if existing.UserID != userID {
			return nil, apperr.New(codes.AlreadyExists,
				"this person is already verified on another account; "+
					"if you believe this is an error, please contact support to resolve the conflict")
		}
		// Renewal path (same user, same provider_user_id): deactivate the old
		// record before creating the new one so the partial unique index
		// constraint is not violated. The new row is inserted in ACTIVE status.
		if err := uc.identityVerificationRepo.UpdateStatus(ctx, existing.ID, entity.VerificationStatusNeedsReverification); err != nil {
			return nil, err
		}
	}

	// Only provider_user_id + method are stored; no 基本4情報, no raw
	// serial, no 個人番号.
	created, err := uc.identityVerificationRepo.Create(ctx, entity.NewIdentityVerification(userID, result.ProviderUserID, result.Method))
	if err != nil {
		return nil, err
	}

	uc.logger.Info(ctx, "identity verification created",
		slog.String("user_id", userID),
		slog.String("identity_verification_id", created.ID),
		slog.Int("method", int(created.Method)),
	)
	return created, nil
}

// Recheck performs a 現況確認 for the user. On a revoked/changed result the
// IdentityVerification is flagged as NEEDS_REVERIFICATION (not a hard lock).
func (uc *identityVerificationUseCase) Recheck(ctx context.Context, userID string) (*entity.IdentityVerification, error) {
	identityVerification, err := uc.identityVerificationRepo.GetByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, apperr.Wrap(err, codes.FailedPrecondition, "the account has no identity verification to re-check")
		}
		return nil, err
	}

	recheckResult, err := uc.verifier.Recheck(ctx, identityVerification.ProviderUserID)
	if err != nil {
		return nil, err
	}

	if recheckResult.NeedsReverification {
		if err := uc.identityVerificationRepo.UpdateStatus(ctx, identityVerification.ID, entity.VerificationStatusNeedsReverification); err != nil {
			return nil, err
		}
		identityVerification.Status = entity.VerificationStatusNeedsReverification
		uc.logger.Info(ctx, "identity verification flagged for re-verification",
			slog.String("user_id", userID),
			slog.String("identity_verification_id", identityVerification.ID),
		)
	}

	return identityVerification, nil
}

// Get returns the user's IdentityVerification.
func (uc *identityVerificationUseCase) Get(ctx context.Context, userID string) (*entity.IdentityVerification, error) {
	return uc.identityVerificationRepo.GetByUserID(ctx, userID)
}

// Delete removes the IdentityVerification for privacy purposes (purpose-end or valid deletion
// request).
func (uc *identityVerificationUseCase) Delete(ctx context.Context, userID string) error {
	identityVerification, err := uc.identityVerificationRepo.GetByUserID(ctx, userID)
	if err != nil {
		return err
	}

	if err := uc.identityVerificationRepo.Delete(ctx, identityVerification.ID); err != nil {
		return err
	}

	uc.logger.Info(ctx, "identity verification deleted",
		slog.String("user_id", userID),
		slog.String("identity_verification_id", identityVerification.ID),
	)
	return nil
}
