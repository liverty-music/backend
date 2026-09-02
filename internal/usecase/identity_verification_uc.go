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

// PocketSignResult is the result of a successful Pocket Sign Stamp verification.
// The raw certificate and card response are validated vendor-side and never
// cross the boundary into our application; only these fields are retained.
type PocketSignResult struct {
	// PocketSignUserID is the Pocket Sign tenant-scoped person key (User.id).
	// Stable across card renewal, re-issue, cert-type change. Never the serial
	// or 個人番号.
	PocketSignUserID string
	// Method is which credential was used (JPKI or driver's licence).
	Method entity.VerificationMethod
}

// PocketSignRecheckResult is the outcome of a 現況確認 (liveness) re-check.
type PocketSignRecheckResult struct {
	// NeedsReverification is true when the Pocket Sign 現況確認 reports the
	// certificate is revoked, 基本4情報 changed, or expired.
	NeedsReverification bool
}

// PocketSignVerifier is the interface the identity use case uses to interact
// with the Pocket Sign Stamp API.
//
// The interface is defined here (where consumed) per AGENTS.md. Implementations
// live in internal/infrastructure/pocketsign/.
//
// Stamp flow:
//  1. StartVerify — backend calls CreateSession → returns (sessionID, redirectURL).
//     The frontend opens redirectURL in the PocketSign app; the fan reads their
//     card there. The app redirects back to the callback URL with ?session_id=<id>.
//  2. CompleteVerify — backend calls FinalizeSession(sessionID) → verifies the
//     nonce stored at step 1 matches metadata in the response, extracts
//     PocketSignUserID.
//  3. Recheck — calls verify.v2.UserService.CheckUserStatus for 現況確認.
type PocketSignVerifier interface {
	// StartVerify creates a Pocket Sign Stamp session. It returns the opaque
	// session id and the redirect URL the client must open in the PocketSign
	// app. A random nonce is generated and stored server-side keyed by
	// (userID, sessionID) for later comparison in CompleteVerify.
	//
	// # Possible errors
	//
	//   - Unavailable: the Pocket Sign Stamp API is unreachable or not configured.
	StartVerify(ctx context.Context, userID string, method entity.VerificationMethod) (sessionID string, redirectURL string, err error)

	// CompleteVerify finalizes the Pocket Sign Stamp session. It calls
	// FinalizeSession, validates the server-stored nonce against the response
	// metadata, checks the verification result, and extracts the
	// PocketSignUserID from the userAuthentication result.
	//
	// # Possible errors
	//
	//   - FailedPrecondition: the session is not yet completed (fan has not
	//     finished in the PocketSign app) or the cert failed validation
	//     (revoked/expired/signature mismatch).
	//   - PermissionDenied: nonce mismatch (replay or tamper attempt).
	//   - Unavailable: the Pocket Sign Stamp API is unreachable or not configured.
	CompleteVerify(ctx context.Context, userID, sessionID string) (*PocketSignResult, error)

	// Recheck performs a 現況確認 (liveness) check for the given pocket_sign_user_id.
	// It reports whether the verified identity is still current (not revoked,
	// 基本4情報 unchanged, not expired).
	//
	// # Possible errors
	//
	//   - Unavailable: the Pocket Sign Verify API is unreachable or not configured.
	Recheck(ctx context.Context, pocketSignUserID string) (*PocketSignRecheckResult, error)
}

// IdentityVerificationUseCase defines the business logic for identity
// verification via Pocket Sign.
type IdentityVerificationUseCase interface {
	// StartVerify creates a Pocket Sign Stamp session for the authenticated user.
	// Returns a session id and the redirect URL for the PocketSign app.
	//
	// # Possible errors
	//
	//   - NotFound: the user_id does not exist.
	//   - Unavailable: the Pocket Sign Stamp API is not configured.
	StartVerify(ctx context.Context, userID string, method entity.VerificationMethod) (sessionID string, redirectURL string, err error)

	// CompleteVerify finalizes the Stamp session, creates a VerifiedIdentity,
	// and upgrades the account's verification_level to IDENTITY_VERIFIED.
	//
	// The raw certificate and card response are validated vendor-side; only the
	// pocket_sign_user_id is retained, as required by the privacy spec.
	//
	// # Possible errors
	//
	//   - NotFound: the user_id does not exist.
	//   - FailedPrecondition: session not yet completed by the fan, or cert invalid.
	//   - PermissionDenied: nonce mismatch (replay/tamper).
	//   - AlreadyExists: the Pocket Sign User.id is already bound to a different active
	//     account (duplicate-person). The error carries a clear recovery-path message.
	//   - Unavailable: the Pocket Sign Stamp API is not configured.
	CompleteVerify(ctx context.Context, userID, sessionID string) (*entity.VerifiedIdentity, error)

	// ReCheck performs a 現況確認 (liveness) re-check for the authenticated user.
	// On a revoked/changed result it flags the VerifiedIdentity as
	// NEEDS_REVERIFICATION (not a hard lock).
	//
	// # Possible errors
	//
	//   - NotFound: the user has no active VerifiedIdentity.
	//   - Unavailable: the Pocket Sign Verify API is not configured.
	ReCheck(ctx context.Context, userID string) (*entity.VerifiedIdentity, error)

	// GetMyVerificationStatus returns the current verification level and, when
	// present, the backing VerifiedIdentity for the user.
	//
	// # Possible errors
	//
	//   - NotFound: the user_id does not exist.
	GetMyVerificationStatus(ctx context.Context, userID string) (entity.VerificationLevel, *entity.VerifiedIdentity, error)

	// Delete removes the VerifiedIdentity for a user. Used for the privacy
	// deletion path (purpose-end or valid deletion request), subject to lawful
	// retention obligations.
	//
	// # Possible errors
	//
	//   - NotFound: the user has no VerifiedIdentity to delete.
	Delete(ctx context.Context, userID string) error
}

// identityVerificationUseCase is the concrete implementation.
type identityVerificationUseCase struct {
	verifiedIdentityRepo entity.VerifiedIdentityRepository
	userRepo             entity.UserRepository
	verifier             PocketSignVerifier
	logger               *logging.Logger
}

// NewIdentityVerificationUseCase creates a new IdentityVerificationUseCase.
func NewIdentityVerificationUseCase(
	verifiedIdentityRepo entity.VerifiedIdentityRepository,
	userRepo entity.UserRepository,
	verifier PocketSignVerifier,
	logger *logging.Logger,
) IdentityVerificationUseCase {
	return &identityVerificationUseCase{
		verifiedIdentityRepo: verifiedIdentityRepo,
		userRepo:             userRepo,
		verifier:             verifier,
		logger:               logger,
	}
}

// StartVerify creates a Pocket Sign Stamp session for the given user.
func (uc *identityVerificationUseCase) StartVerify(
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

// CompleteVerify finalizes the Stamp session and creates a VerifiedIdentity.
// Only the pocket_sign_user_id is stored; the raw certificate/response is
// validated vendor-side and never persisted here per the privacy spec.
func (uc *identityVerificationUseCase) CompleteVerify(
	ctx context.Context,
	userID, sessionID string,
) (*entity.VerifiedIdentity, error) {
	// Verify the user exists.
	if _, err := uc.userRepo.Get(ctx, userID); err != nil {
		return nil, err
	}

	// Call the Pocket Sign Stamp API to finalize the session. The verifier
	// validates the nonce and cert result; only PocketSignUserID + Method cross
	// the boundary.
	result, err := uc.verifier.CompleteVerify(ctx, userID, sessionID)
	if err != nil {
		return nil, err
	}

	// 3.1 / 3.2 dedupe: check whether the pocket_sign_user_id is already bound
	// to an active account. If it belongs to a DIFFERENT user, reject with
	// AlreadyExists and a clear recovery-path message. If it belongs to THIS
	// user (renewal path), the existing record is deactivated and a fresh one
	// is created.
	existing, err := uc.verifiedIdentityRepo.GetByPocketSignUserID(ctx, result.PocketSignUserID)
	if err != nil && !errors.Is(err, apperr.ErrNotFound) {
		return nil, err
	}

	if existing != nil {
		if existing.UserID != userID {
			// 3.2: another account already holds this Pocket Sign User.id.
			// Reject with a clear message directing to account recovery/support.
			return nil, apperr.New(codes.AlreadyExists,
				"this identity is already bound to another account; "+
					"if you believe this is an error, please contact support to resolve the conflict")
		}
		// Renewal path (same user, same pocket_sign_user_id): deactivate the old
		// record before creating the new one so the partial unique index
		// constraint is not violated. The new row is inserted in ACTIVE status.
		if err := uc.verifiedIdentityRepo.UpdateStatus(ctx, existing.ID, entity.VerificationStatusNeedsReverification); err != nil {
			return nil, err
		}
	}

	// 2.2: create the VerifiedIdentity. Only pocket_sign_user_id + method are
	// stored; no 基本4情報, no raw serial, no 個人番号.
	vi := entity.NewVerifiedIdentity(userID, result.PocketSignUserID, result.Method)
	created, err := uc.verifiedIdentityRepo.Create(ctx, vi)
	if err != nil {
		return nil, err
	}

	uc.logger.Info(ctx, "verified identity created",
		slog.String("user_id", userID),
		slog.String("verified_identity_id", created.ID),
		slog.Int("method", int(created.Method)),
	)
	return created, nil
}

// ReCheck performs a 現況確認 for the user. On a revoked/changed result the
// VerifiedIdentity is flagged as NEEDS_REVERIFICATION (not a hard lock).
func (uc *identityVerificationUseCase) ReCheck(ctx context.Context, userID string) (*entity.VerifiedIdentity, error) {
	vi, err := uc.verifiedIdentityRepo.GetByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}

	recheckResult, err := uc.verifier.Recheck(ctx, vi.PocketSignUserID)
	if err != nil {
		return nil, err
	}

	if recheckResult.NeedsReverification {
		if err := uc.verifiedIdentityRepo.UpdateStatus(ctx, vi.ID, entity.VerificationStatusNeedsReverification); err != nil {
			return nil, err
		}
		vi.Status = entity.VerificationStatusNeedsReverification
		uc.logger.Info(ctx, "verified identity flagged for re-verification",
			slog.String("user_id", userID),
			slog.String("verified_identity_id", vi.ID),
		)
	}

	return vi, nil
}

// GetMyVerificationStatus returns the user's current verification level and
// backing identity (nil when UNVERIFIED).
func (uc *identityVerificationUseCase) GetMyVerificationStatus(ctx context.Context, userID string) (entity.VerificationLevel, *entity.VerifiedIdentity, error) {
	// Confirm the user exists.
	if _, err := uc.userRepo.Get(ctx, userID); err != nil {
		return entity.VerificationLevelUnverified, nil, err
	}

	vi, err := uc.verifiedIdentityRepo.GetByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return entity.VerificationLevelUnverified, nil, nil
		}
		return entity.VerificationLevelUnverified, nil, err
	}

	// Only an ACTIVE record counts as IDENTITY_VERIFIED.
	if vi.Status == entity.VerificationStatusActive {
		return entity.VerificationLevelIdentityVerified, vi, nil
	}
	return entity.VerificationLevelUnverified, vi, nil
}

// Delete removes the VerifiedIdentity for privacy purposes (purpose-end or
// valid deletion request).
func (uc *identityVerificationUseCase) Delete(ctx context.Context, userID string) error {
	vi, err := uc.verifiedIdentityRepo.GetByUserID(ctx, userID)
	if err != nil {
		return err
	}

	if err := uc.verifiedIdentityRepo.Delete(ctx, vi.ID); err != nil {
		return err
	}

	uc.logger.Info(ctx, "verified identity deleted",
		slog.String("user_id", userID),
		slog.String("verified_identity_id", vi.ID),
	)
	return nil
}
