package usecase_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// kycTestDeps wires all dependencies for KycUseCase tests.
type kycTestDeps struct {
	kycRepo  *entitymocks.MockKycRepository
	userRepo *entitymocks.MockUserRepository
	verifier *ucmocks.MockPocketSignVerifier
	uc       usecase.KycUseCase
}

func newKycTestDeps(t *testing.T) *kycTestDeps {
	t.Helper()
	d := &kycTestDeps{
		kycRepo:  entitymocks.NewMockKycRepository(t),
		userRepo: entitymocks.NewMockUserRepository(t),
		verifier: ucmocks.NewMockPocketSignVerifier(t),
	}
	d.uc = usecase.NewKycUseCase(d.kycRepo, d.userRepo, d.verifier, newTestLogger(t))
	return d
}

// sampleUser returns a minimal User for test setup.
func sampleUser(id string) *entity.User {
	return &entity.User{ID: id, ExternalID: "ext-" + id, Email: id + "@example.com", Name: "Test User"}
}

// sampleKyc returns a new Kyc in ACTIVE status.
func sampleKyc(id, userID, pocketSignUserID string) *entity.Kyc {
	return &entity.Kyc{
		ID:               id,
		UserID:           userID,
		Method:           entity.VerificationMethodJPKI,
		PocketSignUserID: pocketSignUserID,
		DedupeStrength:   entity.DedupeStrengthStrong,
		VerifiedTime:     time.Now().UTC(),
		Status:           entity.VerificationStatusActive,
	}
}

// ────────────────────────────────────────────────────────────────────
// Start
// ────────────────────────────────────────────────────────────────────

func TestKycUseCase_Start(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	type args struct {
		userID string
		method entity.VerificationMethod
	}
	type dep struct {
		userFound   bool
		stubCalled  bool
		sessionID   string
		redirectURL string
		stubErr     error
	}

	tests := []struct {
		name        string
		args        args
		dep         dep
		wantSession string
		wantURL     string
		wantErr     error
	}{
		{
			name: "return session id and redirect url when user exists and method is JPKI",
			args: args{userID: "user-1", method: entity.VerificationMethodJPKI},
			dep: dep{
				userFound:   true,
				stubCalled:  true,
				sessionID:   "sess-abc",
				redirectURL: "https://stamp.p8n.app/redirect/sess-abc",
			},
			wantSession: "sess-abc",
			wantURL:     "https://stamp.p8n.app/redirect/sess-abc",
		},
		{
			name:    "return error when user does not exist",
			args:    args{userID: "missing", method: entity.VerificationMethodJPKI},
			dep:     dep{userFound: false},
			wantErr: apperr.ErrNotFound,
		},
		{
			name:    "return error when method is unspecified",
			args:    args{userID: "user-1", method: entity.VerificationMethodUnspecified},
			dep:     dep{userFound: true},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "propagate verifier error when stub returns unavailable",
			args: args{userID: "user-1", method: entity.VerificationMethodJPKI},
			dep: dep{
				userFound:  true,
				stubCalled: true,
				stubErr:    apperr.New(codes.Unavailable, "not configured"),
			},
			wantErr: apperr.ErrUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newKycTestDeps(t)

			if tt.dep.userFound {
				d.userRepo.EXPECT().Get(ctx, tt.args.userID).Return(sampleUser(tt.args.userID), nil)
			} else {
				d.userRepo.EXPECT().Get(ctx, tt.args.userID).Return(nil, apperr.New(codes.NotFound, "not found"))
			}

			if tt.dep.stubCalled {
				d.verifier.EXPECT().StartVerify(ctx, tt.args.userID, tt.args.method).
					Return(tt.dep.sessionID, tt.dep.redirectURL, tt.dep.stubErr)
			}

			gotSession, gotURL, err := d.uc.Start(ctx, tt.args.userID, tt.args.method)

			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Empty(t, gotSession)
				assert.Empty(t, gotURL)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantSession, gotSession)
			assert.Equal(t, tt.wantURL, gotURL)
		})
	}
}

// ────────────────────────────────────────────────────────────────────
// Create
// ────────────────────────────────────────────────────────────────────

func TestKycUseCase_Create(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const (
		userA     = "user-a"
		userB     = "user-b"
		psidA     = "ps-id-a"
		sessionID = "sess-1"
		kycID     = "kyc-1"
	)

	tests := []struct {
		name    string
		setup   func(d *kycTestDeps)
		userID  string
		wantErr error
	}{
		{
			name:   "successful verify upgrades account to KYC_VERIFIED",
			userID: userA,
			setup: func(d *kycTestDeps) {
				d.userRepo.EXPECT().Get(ctx, userA).Return(sampleUser(userA), nil)
				d.verifier.EXPECT().CompleteVerify(ctx, userA, sessionID).Return(
					&usecase.PocketSignResult{PocketSignUserID: psidA, Method: entity.VerificationMethodJPKI}, nil,
				)
				// No existing active Kyc for psidA.
				d.kycRepo.EXPECT().GetByPocketSignUserID(ctx, psidA).Return(nil, apperr.New(codes.NotFound, "not found"))
				d.kycRepo.EXPECT().Create(ctx, matchKyc(userA, psidA)).Return(sampleKyc(kycID, userA, psidA), nil)
			},
		},
		{
			name:   "reject when pocket_sign_user_id belongs to a different account",
			userID: userA,
			setup: func(d *kycTestDeps) {
				d.userRepo.EXPECT().Get(ctx, userA).Return(sampleUser(userA), nil)
				d.verifier.EXPECT().CompleteVerify(ctx, userA, sessionID).Return(
					&usecase.PocketSignResult{PocketSignUserID: psidA, Method: entity.VerificationMethodJPKI}, nil,
				)
				// psidA already belongs to userB — duplicate-person attempt.
				d.kycRepo.EXPECT().GetByPocketSignUserID(ctx, psidA).Return(sampleKyc(kycID, userB, psidA), nil)
			},
			wantErr: apperr.ErrAlreadyExists,
		},
		{
			name:   "renewal path re-links same pocket_sign_user_id to the same user",
			userID: userA,
			setup: func(d *kycTestDeps) {
				d.userRepo.EXPECT().Get(ctx, userA).Return(sampleUser(userA), nil)
				d.verifier.EXPECT().CompleteVerify(ctx, userA, sessionID).Return(
					&usecase.PocketSignResult{PocketSignUserID: psidA, Method: entity.VerificationMethodJPKI}, nil,
				)
				// psidA belongs to userA — renewal path: deactivate old, create new.
				oldVI := sampleKyc("old-kyc", userA, psidA)
				d.kycRepo.EXPECT().GetByPocketSignUserID(ctx, psidA).Return(oldVI, nil)
				d.kycRepo.EXPECT().UpdateStatus(ctx, "old-kyc", entity.VerificationStatusNeedsReverification).Return(nil)
				d.kycRepo.EXPECT().Create(ctx, matchKyc(userA, psidA)).Return(sampleKyc(kycID, userA, psidA), nil)
			},
		},
		{
			name:   "return error when user does not exist",
			userID: "missing",
			setup: func(d *kycTestDeps) {
				d.userRepo.EXPECT().Get(ctx, "missing").Return(nil, apperr.New(codes.NotFound, "not found"))
			},
			wantErr: apperr.ErrNotFound,
		},
		{
			name:   "only pocket_sign_user_id is stored — no PII survives the call",
			userID: userA,
			setup: func(d *kycTestDeps) {
				d.userRepo.EXPECT().Get(ctx, userA).Return(sampleUser(userA), nil)
				d.verifier.EXPECT().CompleteVerify(ctx, userA, sessionID).Return(
					&usecase.PocketSignResult{PocketSignUserID: psidA, Method: entity.VerificationMethodJPKI}, nil,
				)
				d.kycRepo.EXPECT().GetByPocketSignUserID(ctx, psidA).Return(nil, apperr.New(codes.NotFound, "not found"))
				// The entity passed to Create must contain only the pocket_sign_user_id
				// — no raw cert data or any other PII.
				d.kycRepo.EXPECT().Create(ctx, matchKycPrivacy(userA, psidA)).Return(sampleKyc(kycID, userA, psidA), nil)
			},
		},
		{
			name:   "propagate FailedPrecondition when verifier returns session not yet completed",
			userID: userA,
			setup: func(d *kycTestDeps) {
				d.userRepo.EXPECT().Get(ctx, userA).Return(sampleUser(userA), nil)
				d.verifier.EXPECT().CompleteVerify(ctx, userA, sessionID).Return(
					nil, apperr.New(codes.FailedPrecondition, "session not yet completed"),
				)
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name:   "propagate PermissionDenied when verifier returns nonce mismatch",
			userID: userA,
			setup: func(d *kycTestDeps) {
				d.userRepo.EXPECT().Get(ctx, userA).Return(sampleUser(userA), nil)
				d.verifier.EXPECT().CompleteVerify(ctx, userA, sessionID).Return(
					nil, apperr.New(codes.PermissionDenied, "nonce mismatch"),
				)
			},
			wantErr: apperr.ErrPermissionDenied,
		},
		{
			// GetByPocketSignUserID returns a non-NotFound error (e.g. Unavailable due
			// to a DB outage). The usecase must propagate the error — it must NOT
			// swallow it or fall through to Create.
			name:   "propagate non-NotFound error from GetByPocketSignUserID without calling Create",
			userID: userA,
			setup: func(d *kycTestDeps) {
				d.userRepo.EXPECT().Get(ctx, userA).Return(sampleUser(userA), nil)
				d.verifier.EXPECT().CompleteVerify(ctx, userA, sessionID).Return(
					&usecase.PocketSignResult{PocketSignUserID: psidA, Method: entity.VerificationMethodJPKI}, nil,
				)
				// GetByPocketSignUserID returns Unavailable — NOT NotFound.
				// Create must NOT be called in this case.
				d.kycRepo.EXPECT().GetByPocketSignUserID(ctx, psidA).
					Return(nil, apperr.New(codes.Unavailable, "DB temporarily unavailable"))
				// No Create call expected.
			},
			wantErr: apperr.ErrUnavailable,
		},
		{
			// The renewal path: psidA already belongs to userA, so the usecase
			// calls UpdateStatus to deactivate the old record. If UpdateStatus
			// returns an error, it must be propagated — Create must NOT be called.
			name:   "propagate UpdateStatus error in renewal path without calling Create",
			userID: userA,
			setup: func(d *kycTestDeps) {
				d.userRepo.EXPECT().Get(ctx, userA).Return(sampleUser(userA), nil)
				d.verifier.EXPECT().CompleteVerify(ctx, userA, sessionID).Return(
					&usecase.PocketSignResult{PocketSignUserID: psidA, Method: entity.VerificationMethodJPKI}, nil,
				)
				oldVI := sampleKyc("old-kyc-renewal", userA, psidA)
				d.kycRepo.EXPECT().GetByPocketSignUserID(ctx, psidA).Return(oldVI, nil)
				// UpdateStatus fails (e.g. DB connection lost).
				d.kycRepo.EXPECT().UpdateStatus(ctx, "old-kyc-renewal", entity.VerificationStatusNeedsReverification).
					Return(apperr.New(codes.Internal, "DB write failed"))
				// Create must NOT be called after UpdateStatus fails.
			},
			wantErr: apperr.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newKycTestDeps(t)
			tt.setup(d)

			got, err := d.uc.Create(ctx, tt.userID, sessionID)

			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, got)
			assert.Equal(t, tt.userID, got.UserID)
			assert.Equal(t, psidA, got.PocketSignUserID)
		})
	}
}

// ────────────────────────────────────────────────────────────────────
// Recheck
// ────────────────────────────────────────────────────────────────────

func TestKycUseCase_Recheck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const (
		userA = "user-a"
		psidA = "ps-id-a"
		kycID = "kyc-1"
	)

	tests := []struct {
		name       string
		setup      func(d *kycTestDeps)
		wantStatus entity.VerificationStatus
		wantErr    error
	}{
		{
			name: "return ACTIVE when certificate is still valid",
			setup: func(d *kycTestDeps) {
				kyc := sampleKyc(kycID, userA, psidA)
				d.kycRepo.EXPECT().GetByUserID(ctx, userA).Return(kyc, nil)
				d.verifier.EXPECT().Recheck(ctx, psidA).Return(&usecase.PocketSignRecheckResult{NeedsReverification: false}, nil)
			},
			wantStatus: entity.VerificationStatusActive,
		},
		{
			name: "flag NEEDS_REVERIFICATION when revoked or changed",
			setup: func(d *kycTestDeps) {
				kyc := sampleKyc(kycID, userA, psidA)
				d.kycRepo.EXPECT().GetByUserID(ctx, userA).Return(kyc, nil)
				d.verifier.EXPECT().Recheck(ctx, psidA).Return(&usecase.PocketSignRecheckResult{NeedsReverification: true}, nil)
				d.kycRepo.EXPECT().UpdateStatus(ctx, kycID, entity.VerificationStatusNeedsReverification).Return(nil)
			},
			wantStatus: entity.VerificationStatusNeedsReverification,
		},
		{
			name: "fail with FailedPrecondition when the user has no Kyc to re-check",
			setup: func(d *kycTestDeps) {
				d.kycRepo.EXPECT().GetByUserID(ctx, userA).Return(nil, apperr.New(codes.NotFound, "not found"))
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newKycTestDeps(t)
			tt.setup(d)

			got, err := d.uc.Recheck(ctx, userA)

			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantStatus, got.Status)
		})
	}
}

// ────────────────────────────────────────────────────────────────────
// Get
// ────────────────────────────────────────────────────────────────────

func TestKycUseCase_Get(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const (
		userA = "user-a"
		psidA = "ps-id-a"
		kycID = "kyc-1"
	)

	tests := []struct {
		name      string
		setup     func(d *kycTestDeps)
		wantLevel entity.VerificationLevel
		wantErr   error
	}{
		{
			name: "return the Active Kyc, which makes the account KYC_VERIFIED",
			setup: func(d *kycTestDeps) {
				d.kycRepo.EXPECT().GetByUserID(ctx, userA).Return(sampleKyc(kycID, userA, psidA), nil)
			},
			wantLevel: entity.VerificationLevelKycVerified,
		},
		{
			name: "return a Kyc that needs re-verification, which leaves the account UNVERIFIED",
			setup: func(d *kycTestDeps) {
				kyc := sampleKyc(kycID, userA, psidA)
				kyc.Status = entity.VerificationStatusNeedsReverification
				d.kycRepo.EXPECT().GetByUserID(ctx, userA).Return(kyc, nil)
			},
			wantLevel: entity.VerificationLevelUnverified,
		},
		{
			name: "fail with NotFound for a fan who never passed KYC",
			setup: func(d *kycTestDeps) {
				d.kycRepo.EXPECT().GetByUserID(ctx, userA).Return(nil, apperr.New(codes.NotFound, "not found"))
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newKycTestDeps(t)
			tt.setup(d)

			kyc, err := d.uc.Get(ctx, userA)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Equal(t, entity.VerificationLevelUnverified, usecase.KycVerificationLevel(kyc))
				return
			}
			require.NoError(t, err)
			assert.Equal(t, kycID, kyc.ID)
			assert.Equal(t, tt.wantLevel, usecase.KycVerificationLevel(kyc))
		})
	}
}

// ────────────────────────────────────────────────────────────────────
// Delete
// ────────────────────────────────────────────────────────────────────

func TestKycUseCase_Delete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const (
		userA = "user-a"
		psidA = "ps-id-a"
		kycID = "kyc-1"
	)

	tests := []struct {
		name    string
		setup   func(d *kycTestDeps)
		wantErr error
	}{
		{
			name: "delete Kyc when it exists",
			setup: func(d *kycTestDeps) {
				d.kycRepo.EXPECT().GetByUserID(ctx, userA).Return(sampleKyc(kycID, userA, psidA), nil)
				d.kycRepo.EXPECT().Delete(ctx, kycID).Return(nil)
			},
		},
		{
			name: "return error when user has no Kyc",
			setup: func(d *kycTestDeps) {
				d.kycRepo.EXPECT().GetByUserID(ctx, userA).Return(nil, apperr.New(codes.NotFound, "not found"))
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newKycTestDeps(t)
			tt.setup(d)

			err := d.uc.Delete(ctx, userA)

			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

// ────────────────────────────────────────────────────────────────────
// Test matcher helpers
// ────────────────────────────────────────────────────────────────────

// matchVI returns a mock.MatchedBy predicate that accepts any *entity.Kyc
// with the given userID and pocketSignUserID.
func matchKyc(userID, pocketSignUserID string) any {
	return mock.MatchedBy(func(kyc *entity.Kyc) bool {
		return kyc != nil && kyc.UserID == userID && kyc.PocketSignUserID == pocketSignUserID
	})
}

// matchKycPrivacy returns a mock.MatchedBy predicate that verifies the
// *entity.Kyc passed to Create stores only the pocket_sign_user_id
// and contains no suspicious extra raw data. The Stamp flow stores no raw cert
// or signed-response bytes on the entity — this predicate enforces that invariant
// by confirming the fields contain only the expected typed values.
func matchKycPrivacy(userID, pocketSignUserID string) any {
	return mock.MatchedBy(func(kyc *entity.Kyc) bool {
		if kyc == nil {
			return false
		}
		if kyc.UserID != userID || kyc.PocketSignUserID != pocketSignUserID {
			return false
		}
		// Confirm none of the string fields carry unexpected large blobs.
		// The only stored identifiers should be the userID and pocketSignUserID;
		// other string fields (ID, etc.) are opaque IDs, not raw cert bytes.
		forbiddenStrings := []string{"signed", "jwe", "certificate"}
		allFields := []string{kyc.ID, kyc.UserID, kyc.PocketSignUserID}
		for _, field := range allFields {
			for _, forbidden := range forbiddenStrings {
				if slices.Contains([]string{field}, forbidden) {
					return false
				}
			}
		}
		return true
	})
}
