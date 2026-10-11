package entity

import (
	"context"
	"time"
)

// VerificationMethod records how a person proved their identity.
// Values mirror the proto enum VerificationMethod.
type VerificationMethod int16

const (
	// VerificationMethodUnspecified is the zero value and is never persisted.
	VerificationMethodUnspecified VerificationMethod = 0
	// VerificationMethodJPKI indicates マイナンバーカード 公的個人認証 via Pocket Sign.
	// Yields a stable per-person tenant-scoped key (strong dedupe).
	VerificationMethodJPKI VerificationMethod = 1
	// VerificationMethodDriverLicence indicates the 運転免許証 IC (Verify CardInfo)
	// fallback. Does not yield an equivalent stable per-person key (weak dedupe).
	VerificationMethodDriverLicence VerificationMethod = 2
)

// DedupeStrength states how strong the one-person guarantee is for a
// IdentityVerification. It follows from the method.
type DedupeStrength int16

const (
	// DedupeStrengthUnspecified is the zero value and is never persisted.
	DedupeStrengthUnspecified DedupeStrength = 0
	// DedupeStrengthStrong is backed by a stable per-person key (Pocket Sign
	// User.id) that survives card renewal/re-issue — the JPKI path.
	DedupeStrengthStrong DedupeStrength = 1
	// DedupeStrengthWeak is backed only by a document-scoped identifier that can
	// change (driver's-licence fallback).
	DedupeStrengthWeak DedupeStrength = 2
)

// VerificationStatus is the freshness lifecycle of a IdentityVerification, driven
// by the periodic 現況確認 re-check.
type VerificationStatus int16

const (
	// VerificationStatusUnspecified is the zero value and is never persisted.
	VerificationStatusUnspecified VerificationStatus = 0
	// VerificationStatusActive means the most recent 現況確認 found the certificate
	// valid (not revoked, 基本4情報 unchanged, not expired).
	VerificationStatusActive VerificationStatus = 1
	// VerificationStatusNeedsReverification means a 現況確認 reported the
	// certificate revoked, 基本4情報 changed, or expired. The account is flagged to
	// re-verify; it is not silently kept verified and not hard-locked.
	VerificationStatusNeedsReverification VerificationStatus = 2
)

// IdentityVerification is the result of a fan's 本人確認 (identity check): it binds one account
// to a verified real person, established via Pocket Sign (PocketSign Verify).
// It retains ONLY the result of a verification — never the raw certificate/response, never the 個人番号, never
// the certificate 発行番号/serial, and no 基本4情報 by default.
//
// The person key is ProviderUserID, on which the platform enforces "at most
// one verified account per person".
type IdentityVerification struct {
	// ID is the unique identifier for this verification record (UUIDv7).
	ID string
	// UserID is the account this verification is bound to.
	UserID string
	// Method is how the person proved their identity (JPKI or driver's licence).
	Method VerificationMethod
	// ProviderUserID is the Pocket Sign tenant-scoped person key. The only
	// person-identifying datum stored; never the serial, never the 個人番号.
	ProviderUserID string
	// DedupeStrength is derived from Method (STRONG for JPKI, WEAK for licence).
	DedupeStrength DedupeStrength
	// VerifyTime is when the verification was established.
	VerifyTime time.Time
	// Status is the freshness lifecycle, maintained by 現況確認 re-checks.
	Status VerificationStatus
}

// NewIdentityVerification creates a new IdentityVerification with a generated UUIDv7
// id. The status is set to Active; DedupeStrength is derived from Method.
//
// Raw certificate data MUST NOT be passed here — callers are responsible for
// discarding it immediately after the Pocket Sign Verify API call.
func NewIdentityVerification(userID, pocketSignUserID string, method VerificationMethod) *IdentityVerification {
	strength := DedupeStrengthWeak
	if method == VerificationMethodJPKI {
		strength = DedupeStrengthStrong
	}
	return &IdentityVerification{
		ID:             NewID(),
		UserID:         userID,
		Method:         method,
		ProviderUserID: pocketSignUserID,
		DedupeStrength: strength,
		VerifyTime:     time.Now().UTC(),
		Status:         VerificationStatusActive,
	}
}

// IdentityVerificationRepository defines the persistence contract for [IdentityVerification] records.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type IdentityVerificationRepository interface {
	// Create inserts a new IdentityVerification. The partial unique index on
	// (provider_user_id WHERE status=ACTIVE) enforces the 1-person-1-account
	// invariant at the database layer.
	//
	// # Possible errors
	//
	//  - AlreadyExists: the provider_user_id already has an active IdentityVerification for a
	//    different account (duplicate-person attempt).
	//  - FailedPrecondition: the user_id does not exist (FK violation).
	Create(ctx context.Context, identityVerification *IdentityVerification) (*IdentityVerification, error)

	// GetByUserID returns the most recent IdentityVerification for the given user.
	// Returns NotFound when the user has no verification record.
	//
	// # Possible errors
	//
	//  - NotFound: no record exists for the user.
	GetByUserID(ctx context.Context, userID string) (*IdentityVerification, error)

	// GetByPocketSignUserID returns the IdentityVerification keyed on the Pocket Sign
	// person key. Used to detect a duplicate-person attempt (another account with
	// the same provider_user_id in ACTIVE status).
	//
	// # Possible errors
	//
	//  - NotFound: no active record for the given provider_user_id.
	GetByProviderUserID(ctx context.Context, pocketSignUserID string) (*IdentityVerification, error)

	// UpdateStatus changes the status of the record identified by id. Used by
	// the 現況確認 re-check to flag NEEDS_REVERIFICATION and by the renewal path
	// to reset to ACTIVE.
	//
	// # Possible errors
	//
	//  - NotFound: no record with the id exists.
	UpdateStatus(ctx context.Context, id string, status VerificationStatus) error

	// Delete removes the IdentityVerification record. Used by the privacy deletion
	// path (purpose-end / valid request).
	//
	// # Possible errors
	//
	//  - NotFound: no record with the id exists.
	Delete(ctx context.Context, id string) error
}
