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

// PocketSignVerifier is the interface the KYC use case uses to interact
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
	// It reports whether the Kyc is still current (not revoked,
	// 基本4情報 unchanged, not expired).
	//
	// # Possible errors
	//
	//   - Unavailable: the Pocket Sign Verify API is unreachable or not configured.
	Recheck(ctx context.Context, pocketSignUserID string) (*PocketSignRecheckResult, error)
}

// KycUseCase runs a fan's 本人確認 (identity check) via Pocket Sign and reads
// its result, the fan's [entity.Kyc].
type KycUseCase interface {
	// Start creates a Pocket Sign Stamp session for the calling fan and returns
	// its session id and the redirect URL for the PocketSign app.
	//
	// # Possible errors
	//
	//   - InvalidArgument: the method is unspecified.
	//   - NotFound: the user does not exist.
	//   - Unavailable: the Pocket Sign Stamp API is not configured or
	//     unreachable.
	Start(ctx context.Context, userID string, method entity.VerificationMethod) (sessionID string, redirectURL string, err error)

	// Create finalizes the Stamp session and records the fan's Kyc, which
	// makes the account KYC_VERIFIED.
	//
	// The raw certificate and card response are validated vendor-side; only the
	// pocket_sign_user_id is retained, as required by the privacy spec.
	//
	// # Possible errors
	//
	//   - NotFound: the user does not exist.
	//   - FailedPrecondition: session not yet completed by the fan, or cert invalid.
	//   - PermissionDenied: nonce mismatch (replay/tamper).
	//   - AlreadyExists: the Pocket Sign User.id is already bound to a different active
	//     account (duplicate-person). The error carries a clear recovery-path message.
	//   - Unavailable: the Pocket Sign Stamp API is not configured.
	Create(ctx context.Context, userID, sessionID string) (*entity.Kyc, error)

	// Recheck performs a 現況確認 (liveness) re-check of the fan's Kyc. On a
	// revoked/changed result it flags the Kyc as NEEDS_REVERIFICATION (not a
	// hard lock).
	//
	// # Possible errors
	//
	//   - FailedPrecondition: the user has no Kyc to re-check.
	//   - Unavailable: the Pocket Sign Verify API is not configured.
	Recheck(ctx context.Context, userID string) (*entity.Kyc, error)

	// Get returns the fan's Kyc, whatever its status. The account's
	// verification level follows from it: KYC_VERIFIED only while it is
	// Active ([KycVerificationLevel]).
	//
	// # Possible errors
	//
	//   - NotFound: the user has never passed KYC.
	Get(ctx context.Context, userID string) (*entity.Kyc, error)

	// Delete removes the fan's Kyc. Used for the privacy deletion path
	// (purpose-end or valid deletion request), subject to lawful retention
	// obligations.
	//
	// # Possible errors
	//
	//   - NotFound: the user has no Kyc to delete.
	Delete(ctx context.Context, userID string) error
}

// KycVerificationLevel returns the account's verification level backed by
// kyc: KYC_VERIFIED for an Active Kyc, UNVERIFIED for none or any other
// status.
func KycVerificationLevel(kyc *entity.Kyc) entity.VerificationLevel {
	if kyc != nil && kyc.Status == entity.VerificationStatusActive {
		return entity.VerificationLevelKycVerified
	}
	return entity.VerificationLevelUnverified
}

// kycUseCase is the concrete implementation.
type kycUseCase struct {
	kycRepo  entity.KycRepository
	userRepo entity.UserRepository
	verifier PocketSignVerifier
	logger   *logging.Logger
}

// NewKycUseCase creates a new KycUseCase.
func NewKycUseCase(
	kycRepo entity.KycRepository,
	userRepo entity.UserRepository,
	verifier PocketSignVerifier,
	logger *logging.Logger,
) KycUseCase {
	return &kycUseCase{
		kycRepo:  kycRepo,
		userRepo: userRepo,
		verifier: verifier,
		logger:   logger,
	}
}

// Start creates a Pocket Sign Stamp session for the given user.
func (uc *kycUseCase) Start(
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

// Create finalizes the Stamp session and records the Kyc. Only the
// pocket_sign_user_id is stored; the raw certificate/response is validated
// vendor-side and never persisted here per the privacy spec.
func (uc *kycUseCase) Create(
	ctx context.Context,
	userID, sessionID string,
) (*entity.Kyc, error) {
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

	// Dedupe: check whether the pocket_sign_user_id is already bound to an
	// active account. If it belongs to a DIFFERENT user, reject with
	// AlreadyExists and a clear recovery-path message. If it belongs to THIS
	// user (renewal path), the existing record is deactivated and a fresh one
	// is created.
	existing, err := uc.kycRepo.GetByPocketSignUserID(ctx, result.PocketSignUserID)
	if err != nil && !errors.Is(err, apperr.ErrNotFound) {
		return nil, err
	}

	if existing != nil {
		if existing.UserID != userID {
			return nil, apperr.New(codes.AlreadyExists,
				"this person is already verified on another account; "+
					"if you believe this is an error, please contact support to resolve the conflict")
		}
		// Renewal path (same user, same pocket_sign_user_id): deactivate the old
		// record before creating the new one so the partial unique index
		// constraint is not violated. The new row is inserted in ACTIVE status.
		if err := uc.kycRepo.UpdateStatus(ctx, existing.ID, entity.VerificationStatusNeedsReverification); err != nil {
			return nil, err
		}
	}

	// Only pocket_sign_user_id + method are stored; no 基本4情報, no raw
	// serial, no 個人番号.
	created, err := uc.kycRepo.Create(ctx, entity.NewKyc(userID, result.PocketSignUserID, result.Method))
	if err != nil {
		return nil, err
	}

	uc.logger.Info(ctx, "kyc created",
		slog.String("user_id", userID),
		slog.String("kyc_id", created.ID),
		slog.Int("method", int(created.Method)),
	)
	return created, nil
}

// Recheck performs a 現況確認 for the user. On a revoked/changed result the
// Kyc is flagged as NEEDS_REVERIFICATION (not a hard lock).
func (uc *kycUseCase) Recheck(ctx context.Context, userID string) (*entity.Kyc, error) {
	kyc, err := uc.kycRepo.GetByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, apperr.Wrap(err, codes.FailedPrecondition, "the account has no kyc to re-check")
		}
		return nil, err
	}

	recheckResult, err := uc.verifier.Recheck(ctx, kyc.PocketSignUserID)
	if err != nil {
		return nil, err
	}

	if recheckResult.NeedsReverification {
		if err := uc.kycRepo.UpdateStatus(ctx, kyc.ID, entity.VerificationStatusNeedsReverification); err != nil {
			return nil, err
		}
		kyc.Status = entity.VerificationStatusNeedsReverification
		uc.logger.Info(ctx, "kyc flagged for re-verification",
			slog.String("user_id", userID),
			slog.String("kyc_id", kyc.ID),
		)
	}

	return kyc, nil
}

// Get returns the user's Kyc.
func (uc *kycUseCase) Get(ctx context.Context, userID string) (*entity.Kyc, error) {
	return uc.kycRepo.GetByUserID(ctx, userID)
}

// Delete removes the Kyc for privacy purposes (purpose-end or valid deletion
// request).
func (uc *kycUseCase) Delete(ctx context.Context, userID string) error {
	kyc, err := uc.kycRepo.GetByUserID(ctx, userID)
	if err != nil {
		return err
	}

	if err := uc.kycRepo.Delete(ctx, kyc.ID); err != nil {
		return err
	}

	uc.logger.Info(ctx, "kyc deleted",
		slog.String("user_id", userID),
		slog.String("kyc_id", kyc.ID),
	)
	return nil
}
