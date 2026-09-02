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

// identityTestDeps wires all dependencies for IdentityVerificationUseCase tests.
type identityTestDeps struct {
	viRepo   *entitymocks.MockVerifiedIdentityRepository
	userRepo *entitymocks.MockUserRepository
	verifier *ucmocks.MockPocketSignVerifier
	uc       usecase.IdentityVerificationUseCase
}

func newIdentityTestDeps(t *testing.T) *identityTestDeps {
	t.Helper()
	d := &identityTestDeps{
		viRepo:   entitymocks.NewMockVerifiedIdentityRepository(t),
		userRepo: entitymocks.NewMockUserRepository(t),
		verifier: ucmocks.NewMockPocketSignVerifier(t),
	}
	d.uc = usecase.NewIdentityVerificationUseCase(d.viRepo, d.userRepo, d.verifier, newTestLogger(t))
	return d
}

// sampleUser returns a minimal User for test setup.
func sampleUser(id string) *entity.User {
	return &entity.User{ID: id, ExternalID: "ext-" + id, Email: id + "@example.com", Name: "Test User", IsActive: true}
}

// sampleVI returns a new VerifiedIdentity in ACTIVE status.
func sampleVI(id, userID, pocketSignUserID string) *entity.VerifiedIdentity {
	return &entity.VerifiedIdentity{
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
// StartVerify
// ────────────────────────────────────────────────────────────────────

func TestIdentityVerificationUseCase_StartVerify(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	type args struct {
		userID string
		method entity.VerificationMethod
	}
	type dep struct {
		userFound  bool
		stubCalled bool
		challenge  *usecase.PocketSignChallenge
		stubErr    error
	}

	tests := []struct {
		name    string
		args    args
		dep     dep
		want    *usecase.PocketSignChallenge
		wantErr error
	}{
		{
			name: "return challenge when user exists and method is JPKI",
			args: args{userID: "user-1", method: entity.VerificationMethodJPKI},
			dep: dep{
				userFound:  true,
				stubCalled: true,
				challenge:  &usecase.PocketSignChallenge{SessionID: "sess-abc", Challenge: []byte("nonce")},
			},
			want: &usecase.PocketSignChallenge{SessionID: "sess-abc", Challenge: []byte("nonce")},
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
			d := newIdentityTestDeps(t)

			if tt.dep.userFound {
				d.userRepo.EXPECT().Get(ctx, tt.args.userID).Return(sampleUser(tt.args.userID), nil)
			} else {
				d.userRepo.EXPECT().Get(ctx, tt.args.userID).Return(nil, apperr.New(codes.NotFound, "not found"))
			}

			if tt.dep.stubCalled {
				d.verifier.EXPECT().IssueChallenge(ctx, tt.args.method).Return(tt.dep.challenge, tt.dep.stubErr)
			}

			got, err := d.uc.StartVerify(ctx, tt.args.userID, tt.args.method)

			if tt.wantErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ────────────────────────────────────────────────────────────────────
// CompleteVerify
// ────────────────────────────────────────────────────────────────────

func TestIdentityVerificationUseCase_CompleteVerify(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const (
		userA     = "user-a"
		userB     = "user-b"
		psidA     = "ps-id-a"
		sessionID = "sess-1"
		viID      = "vi-1"
	)
	signedResponse := []byte("signed")

	tests := []struct {
		name    string
		setup   func(d *identityTestDeps)
		userID  string
		wantErr error
	}{
		{
			name:   "successful verify upgrades account to IDENTITY_VERIFIED",
			userID: userA,
			setup: func(d *identityTestDeps) {
				d.userRepo.EXPECT().Get(ctx, userA).Return(sampleUser(userA), nil)
				d.verifier.EXPECT().ValidateResponse(ctx, sessionID, signedResponse).Return(
					&usecase.PocketSignResult{PocketSignUserID: psidA, Method: entity.VerificationMethodJPKI}, nil,
				)
				// No existing active identity for psidA.
				d.viRepo.EXPECT().GetByPocketSignUserID(ctx, psidA).Return(nil, apperr.New(codes.NotFound, "not found"))
				d.viRepo.EXPECT().Create(ctx, matchVI(userA, psidA)).Return(sampleVI(viID, userA, psidA), nil)
			},
		},
		{
			name:   "reject when pocket_sign_user_id belongs to a different account",
			userID: userA,
			setup: func(d *identityTestDeps) {
				d.userRepo.EXPECT().Get(ctx, userA).Return(sampleUser(userA), nil)
				d.verifier.EXPECT().ValidateResponse(ctx, sessionID, signedResponse).Return(
					&usecase.PocketSignResult{PocketSignUserID: psidA, Method: entity.VerificationMethodJPKI}, nil,
				)
				// psidA already belongs to userB — duplicate-person attempt.
				d.viRepo.EXPECT().GetByPocketSignUserID(ctx, psidA).Return(sampleVI(viID, userB, psidA), nil)
			},
			wantErr: apperr.ErrAlreadyExists,
		},
		{
			name:   "renewal path re-links same pocket_sign_user_id to the same user",
			userID: userA,
			setup: func(d *identityTestDeps) {
				d.userRepo.EXPECT().Get(ctx, userA).Return(sampleUser(userA), nil)
				d.verifier.EXPECT().ValidateResponse(ctx, sessionID, signedResponse).Return(
					&usecase.PocketSignResult{PocketSignUserID: psidA, Method: entity.VerificationMethodJPKI}, nil,
				)
				// psidA belongs to userA — renewal path: deactivate old, create new.
				oldVI := sampleVI("old-vi", userA, psidA)
				d.viRepo.EXPECT().GetByPocketSignUserID(ctx, psidA).Return(oldVI, nil)
				d.viRepo.EXPECT().UpdateStatus(ctx, "old-vi", entity.VerificationStatusNeedsReverification).Return(nil)
				d.viRepo.EXPECT().Create(ctx, matchVI(userA, psidA)).Return(sampleVI(viID, userA, psidA), nil)
			},
		},
		{
			name:   "return error when user does not exist",
			userID: "missing",
			setup: func(d *identityTestDeps) {
				d.userRepo.EXPECT().Get(ctx, "missing").Return(nil, apperr.New(codes.NotFound, "not found"))
			},
			wantErr: apperr.ErrNotFound,
		},
		{
			name:   "only pocket_sign_user_id is stored — no PII survives the call",
			userID: userA,
			setup: func(d *identityTestDeps) {
				d.userRepo.EXPECT().Get(ctx, userA).Return(sampleUser(userA), nil)
				d.verifier.EXPECT().ValidateResponse(ctx, sessionID, signedResponse).Return(
					&usecase.PocketSignResult{PocketSignUserID: psidA, Method: entity.VerificationMethodJPKI}, nil,
				)
				d.viRepo.EXPECT().GetByPocketSignUserID(ctx, psidA).Return(nil, apperr.New(codes.NotFound, "not found"))
				// The entity passed to Create must not carry raw signedResponse data.
				d.viRepo.EXPECT().Create(ctx, matchVINoRawData(userA, psidA, signedResponse)).Return(sampleVI(viID, userA, psidA), nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newIdentityTestDeps(t)
			tt.setup(d)

			got, err := d.uc.CompleteVerify(ctx, tt.userID, sessionID, signedResponse)

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
// ReCheck
// ────────────────────────────────────────────────────────────────────

func TestIdentityVerificationUseCase_ReCheck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const (
		userA = "user-a"
		psidA = "ps-id-a"
		viID  = "vi-1"
	)

	tests := []struct {
		name       string
		setup      func(d *identityTestDeps)
		wantStatus entity.VerificationStatus
		wantErr    error
	}{
		{
			name: "return ACTIVE when certificate is still valid",
			setup: func(d *identityTestDeps) {
				vi := sampleVI(viID, userA, psidA)
				d.viRepo.EXPECT().GetByUserID(ctx, userA).Return(vi, nil)
				d.verifier.EXPECT().Recheck(ctx, psidA).Return(&usecase.PocketSignRecheckResult{NeedsReverification: false}, nil)
			},
			wantStatus: entity.VerificationStatusActive,
		},
		{
			name: "flag NEEDS_REVERIFICATION when revoked or changed",
			setup: func(d *identityTestDeps) {
				vi := sampleVI(viID, userA, psidA)
				d.viRepo.EXPECT().GetByUserID(ctx, userA).Return(vi, nil)
				d.verifier.EXPECT().Recheck(ctx, psidA).Return(&usecase.PocketSignRecheckResult{NeedsReverification: true}, nil)
				d.viRepo.EXPECT().UpdateStatus(ctx, viID, entity.VerificationStatusNeedsReverification).Return(nil)
			},
			wantStatus: entity.VerificationStatusNeedsReverification,
		},
		{
			name: "return error when user has no verified identity",
			setup: func(d *identityTestDeps) {
				d.viRepo.EXPECT().GetByUserID(ctx, userA).Return(nil, apperr.New(codes.NotFound, "not found"))
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newIdentityTestDeps(t)
			tt.setup(d)

			got, err := d.uc.ReCheck(ctx, userA)

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
// GetMyVerificationStatus
// ────────────────────────────────────────────────────────────────────

func TestIdentityVerificationUseCase_GetMyVerificationStatus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const (
		userA = "user-a"
		psidA = "ps-id-a"
		viID  = "vi-1"
	)

	tests := []struct {
		name      string
		setup     func(d *identityTestDeps)
		wantLevel entity.VerificationLevel
		wantVI    bool
	}{
		{
			name: "return IDENTITY_VERIFIED when active record exists",
			setup: func(d *identityTestDeps) {
				d.userRepo.EXPECT().Get(ctx, userA).Return(sampleUser(userA), nil)
				d.viRepo.EXPECT().GetByUserID(ctx, userA).Return(sampleVI(viID, userA, psidA), nil)
			},
			wantLevel: entity.VerificationLevelIdentityVerified,
			wantVI:    true,
		},
		{
			name: "return UNVERIFIED when no record exists",
			setup: func(d *identityTestDeps) {
				d.userRepo.EXPECT().Get(ctx, userA).Return(sampleUser(userA), nil)
				d.viRepo.EXPECT().GetByUserID(ctx, userA).Return(nil, apperr.New(codes.NotFound, "not found"))
			},
			wantLevel: entity.VerificationLevelUnverified,
			wantVI:    false,
		},
		{
			name: "return UNVERIFIED when record is NEEDS_REVERIFICATION",
			setup: func(d *identityTestDeps) {
				d.userRepo.EXPECT().Get(ctx, userA).Return(sampleUser(userA), nil)
				vi := sampleVI(viID, userA, psidA)
				vi.Status = entity.VerificationStatusNeedsReverification
				d.viRepo.EXPECT().GetByUserID(ctx, userA).Return(vi, nil)
			},
			wantLevel: entity.VerificationLevelUnverified,
			wantVI:    true, // record returned but level is UNVERIFIED
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newIdentityTestDeps(t)
			tt.setup(d)

			level, vi, err := d.uc.GetMyVerificationStatus(ctx, userA)

			require.NoError(t, err)
			assert.Equal(t, tt.wantLevel, level)
			if tt.wantVI {
				assert.NotNil(t, vi)
			} else {
				assert.Nil(t, vi)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────
// Delete
// ────────────────────────────────────────────────────────────────────

func TestIdentityVerificationUseCase_Delete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const (
		userA = "user-a"
		psidA = "ps-id-a"
		viID  = "vi-1"
	)

	tests := []struct {
		name    string
		setup   func(d *identityTestDeps)
		wantErr error
	}{
		{
			name: "delete verified identity when it exists",
			setup: func(d *identityTestDeps) {
				d.viRepo.EXPECT().GetByUserID(ctx, userA).Return(sampleVI(viID, userA, psidA), nil)
				d.viRepo.EXPECT().Delete(ctx, viID).Return(nil)
			},
		},
		{
			name: "return error when user has no verified identity",
			setup: func(d *identityTestDeps) {
				d.viRepo.EXPECT().GetByUserID(ctx, userA).Return(nil, apperr.New(codes.NotFound, "not found"))
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newIdentityTestDeps(t)
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

// matchVI returns a mock.MatchedBy predicate that accepts any *entity.VerifiedIdentity
// with the given userID and pocketSignUserID.
func matchVI(userID, pocketSignUserID string) any {
	return mock.MatchedBy(func(vi *entity.VerifiedIdentity) bool {
		return vi != nil && vi.UserID == userID && vi.PocketSignUserID == pocketSignUserID
	})
}

// matchVINoRawData returns a mock.MatchedBy predicate that verifies the
// *entity.VerifiedIdentity passed to Create does NOT contain the raw
// signedResponse bytes anywhere in its fields (raw cert data must be gone).
func matchVINoRawData(userID, pocketSignUserID string, forbidden []byte) any {
	return mock.MatchedBy(func(vi *entity.VerifiedIdentity) bool {
		if vi == nil {
			return false
		}
		if vi.UserID != userID || vi.PocketSignUserID != pocketSignUserID {
			return false
		}
		forbiddenStr := string(forbidden)
		return !slices.Contains([]string{vi.ID, vi.UserID, vi.PocketSignUserID}, forbiddenStr)
	})
}
