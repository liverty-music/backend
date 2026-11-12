package entity

import (
	"context"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// VerificationMethod records how a person proved their identity.
// Values mirror the proto enum VerificationMethod.
type VerificationMethod int16

const (
	// VerificationMethodUnspecified is the zero value and is never persisted.
	VerificationMethodUnspecified VerificationMethod = 0
	// VerificationMethodJPKI indicates マイナンバーカード 公的個人認証 via Pocket Sign.
	// Yields a stable per-person key, scoped to Liverty Music's account at the
	// verification provider (strong dedupe).
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
// to a verified real person, established by the verification provider (Pocket Sign Verify).
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
	// ProviderUserID is the Pocket Sign person key, scoped to Liverty Music's
	// account at the verification provider. The only
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
// discarding it immediately after the Pocket Sign Verify API call. verifyTime
// is when the identity was verified; it is stored in UTC.
func NewIdentityVerification(userID, providerUserID string, method VerificationMethod, verifyTime time.Time) *IdentityVerification {
	strength := DedupeStrengthWeak
	if method == VerificationMethodJPKI {
		strength = DedupeStrengthStrong
	}
	return &IdentityVerification{
		ID:             NewID(),
		UserID:         userID,
		Method:         method,
		ProviderUserID: providerUserID,
		DedupeStrength: strength,
		VerifyTime:     verifyTime.UTC(),
		Status:         VerificationStatusActive,
	}
}

// CertificateState is what a 現況確認 (liveness re-check) reports about the
// certificate a verification was made from. It is the verification provider's
// answer, mapped; what it means for the account is the entity's to decide
// ([IdentityVerification.NeedsReverification]).
type CertificateState int8

const (
	// CertificateStateUnspecified is an unset state, a possible provider hiccup.
	CertificateStateUnspecified CertificateState = iota
	// CertificateStateGood means the certificate is valid.
	CertificateStateGood
	// CertificateStateRevoked means the certificate was revoked.
	CertificateStateRevoked
	// CertificateStateExpired means the certificate expired.
	CertificateStateExpired
	// CertificateStateUnknown means the certificate is not revoked but its
	// validity window could not be confirmed, as with a failed CRL lookup.
	CertificateStateUnknown
)

// IsIndeterminate reports whether the state does not tell whether the
// certificate is good: Unknown and Unspecified.
func (s CertificateState) IsIndeterminate() bool {
	return s == CertificateStateUnknown || s == CertificateStateUnspecified
}

// NeedsReverification reports whether a re-check that found the certificate in
// state means the account must verify again: only a confirmed bad state, revoked
// or expired, does. An indeterminate state does not: flagging it would force a
// spurious re-verification of an otherwise valid person for a transient lookup
// failure.
func (v *IdentityVerification) NeedsReverification(state CertificateState) bool {
	return state == CertificateStateRevoked || state == CertificateStateExpired
}

// CurrentIdentityVerification returns the verification that is current among a
// user's records: the most recently verified Active one, or, when none is
// Active, the most recently verified of the others. It returns nil when there
// are none.
func CurrentIdentityVerification(records []*IdentityVerification) *IdentityVerification {
	var current *IdentityVerification
	for _, r := range records {
		switch {
		case current == nil:
			current = r
		case (r.Status == VerificationStatusActive) != (current.Status == VerificationStatusActive):
			if r.Status == VerificationStatusActive {
				current = r
			}
		case r.VerifyTime.After(current.VerifyTime):
			current = r
		}
	}
	return current
}

// CheckDedupe decides what a new verification of userID does with existing, the
// ACTIVE verification already bound to the same person (nil when there is
// none): at most one verified account per person. A person verified on
// another account fails with AlreadyExists. The same account's own earlier
// record (renewal) is moved to NeedsReverification before the new one is
// created, so the partial unique index on the person key is not violated; the
// returned transition is zero when there is nothing to move.
func CheckDedupe(userID string, existing *IdentityVerification) (Transition[VerificationStatus], error) {
	if existing == nil {
		return Transition[VerificationStatus]{}, nil
	}
	if existing.UserID != userID {
		return Transition[VerificationStatus]{}, apperr.New(codes.AlreadyExists,
			"this person is already verified on another account; "+
				"if you believe this is an error, please contact support to resolve the conflict")
	}
	return Transition[VerificationStatus]{From: []VerificationStatus{VerificationStatusActive}, To: VerificationStatusNeedsReverification}, nil
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

	// GetByProviderUserID returns the IdentityVerification keyed on the Pocket Sign
	// person key. Used to detect a duplicate-person attempt (another account with
	// the same provider_user_id in ACTIVE status).
	//
	// # Possible errors
	//
	//  - NotFound: no active record for the given provider_user_id.
	GetByProviderUserID(ctx context.Context, providerUserID string) (*IdentityVerification, error)

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
