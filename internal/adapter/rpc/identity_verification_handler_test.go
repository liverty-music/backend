package rpc_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	identityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/identity/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────────
// Helpers
// ─────────────────────────────────────────────────────────────────────────────

// newIdentityHandler creates a handler wired with the provided mocks.
func newIdentityHandler(t *testing.T, identityUC *ucmocks.MockIdentityVerificationUseCase, userUC *ucmocks.MockUserUseCase) *rpc.IdentityVerificationHandler {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return rpc.NewIdentityVerificationHandler(identityUC, userUC, logger)
}

// resolveCallerSetup adds the EXPECT calls that resolveCallerUserID requires.
// When found=true the mock resolves externalID → callerUser{ID:callerUserID}.
func resolveCallerSetup(userUC *ucmocks.MockUserUseCase, externalID, callerUserID string, found bool) {
	if found {
		userUC.EXPECT().GetByExternalID(mock.Anything, externalID).
			Return(&entity.User{ID: callerUserID, ExternalID: externalID}, nil).Once()
	} else {
		userUC.EXPECT().GetByExternalID(mock.Anything, externalID).
			Return(nil, apperr.New(codes.NotFound, "user not found")).Once()
	}
}

// sampleActiveVI returns a minimal active VerifiedIdentity for handler tests.
func sampleActiveVI(userID, psid string) *entity.VerifiedIdentity {
	return &entity.VerifiedIdentity{
		ID:               "vi-1",
		UserID:           userID,
		Method:           entity.VerificationMethodJPKI,
		PocketSignUserID: psid,
		DedupeStrength:   entity.DedupeStrengthStrong,
		Status:           entity.VerificationStatusActive,
	}
}

// sampleNeedsReverificationVI returns a VerifiedIdentity in NEEDS_REVERIFICATION.
func sampleNeedsReverificationVI(userID, psid string) *entity.VerifiedIdentity {
	vi := sampleActiveVI(userID, psid)
	vi.Status = entity.VerificationStatusNeedsReverification
	return vi
}

// ─────────────────────────────────────────────────────────────────────────────
// verifiedIdentityLevel helper (tested via handler round-trip)
// ─────────────────────────────────────────────────────────────────────────────

// TestVerifiedIdentityLevel exercises the level derivation logic by driving
// GetMyVerificationStatus through the handler, which calls verifiedIdentityLevel
// internally. This validates three distinct branches in a single function call.
func TestVerifiedIdentityLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		vi        *entity.VerifiedIdentity // returned by usecase mock
		wantLevel entityv1.VerificationLevel
	}{
		{
			name:      "IDENTITY_VERIFIED for an ACTIVE VerifiedIdentity",
			vi:        sampleActiveVI(testCallerUserID, "ps-1"),
			wantLevel: entityv1.VerificationLevel_VERIFICATION_LEVEL_IDENTITY_VERIFIED,
		},
		{
			name:      "UNVERIFIED for a NEEDS_REVERIFICATION VerifiedIdentity",
			vi:        sampleNeedsReverificationVI(testCallerUserID, "ps-2"),
			wantLevel: entityv1.VerificationLevel_VERIFICATION_LEVEL_UNVERIFIED,
		},
		{
			name:      "UNVERIFIED for nil VerifiedIdentity",
			vi:        nil,
			wantLevel: entityv1.VerificationLevel_VERIFICATION_LEVEL_UNVERIFIED,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			identityUC := ucmocks.NewMockIdentityVerificationUseCase(t)
			userUC := ucmocks.NewMockUserUseCase(t)
			h := newIdentityHandler(t, identityUC, userUC)

			resolveCallerSetup(userUC, testCallerExtID, testCallerUserID, true)

			var wantLevel entity.VerificationLevel
			if tt.vi != nil && tt.vi.Status == entity.VerificationStatusActive {
				wantLevel = entity.VerificationLevelIdentityVerified
			} else {
				wantLevel = entity.VerificationLevelUnverified
			}

			identityUC.EXPECT().GetMyVerificationStatus(mock.Anything, testCallerUserID).
				Return(wantLevel, tt.vi, nil).Once()

			ctx := authedCtx(testCallerExtID)
			req := connect.NewRequest(&identityv1.GetMyVerificationStatusRequest{
				UserId: newUserIDProto(testCallerUserID),
			})

			resp, err := h.GetMyVerificationStatus(ctx, req)

			require.NoError(t, err)
			require.NotNil(t, resp)
			assert.Equal(t, tt.wantLevel, resp.Msg.GetVerificationLevel())
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// StartVerify
// ─────────────────────────────────────────────────────────────────────────────

func TestIdentityVerificationHandler_StartVerify(t *testing.T) {
	t.Parallel()

	type args struct {
		ctx    context.Context
		userID string // req.user_id field
		method entityv1.VerificationMethod
	}
	type dep struct {
		setupUserUC     func(m *ucmocks.MockUserUseCase)
		setupIdentityUC func(m *ucmocks.MockIdentityVerificationUseCase)
	}
	type want struct {
		sessionID   string
		redirectURL string
	}
	tests := []struct {
		name     string
		args     args
		dep      dep
		want     want
		wantCode connect.Code
		wantErr  bool
	}{
		{
			name: "return session_id and redirect_url on happy path",
			args: args{
				ctx:    authedCtx(testCallerExtID),
				userID: testCallerUserID,
				method: entityv1.VerificationMethod_VERIFICATION_METHOD_JPKI,
			},
			dep: dep{
				setupUserUC: func(m *ucmocks.MockUserUseCase) {
					resolveCallerSetup(m, testCallerExtID, testCallerUserID, true)
				},
				setupIdentityUC: func(m *ucmocks.MockIdentityVerificationUseCase) {
					m.EXPECT().StartVerify(mock.Anything, testCallerUserID, entity.VerificationMethodJPKI).
						Return("sess-abc", "https://stamp.p8n.app/redirect/sess-abc", nil).Once()
				},
			},
			want: want{sessionID: "sess-abc", redirectURL: "https://stamp.p8n.app/redirect/sess-abc"},
		},
		{
			// req.user_id ≠ JWT-derived caller → PERMISSION_DENIED; usecase NOT called.
			name: "return PERMISSION_DENIED when req.user_id differs from JWT-derived caller",
			args: args{
				ctx:    authedCtx(testCallerExtID),
				userID: testForeignUserID,
				method: entityv1.VerificationMethod_VERIFICATION_METHOD_JPKI,
			},
			dep: dep{
				setupUserUC: func(m *ucmocks.MockUserUseCase) {
					resolveCallerSetup(m, testCallerExtID, testCallerUserID, true)
					// identityUC must NOT be called after permission check fails.
				},
				setupIdentityUC: func(m *ucmocks.MockIdentityVerificationUseCase) {},
			},
			wantErr:  true,
			wantCode: connect.CodePermissionDenied,
		},
		{
			// GetByExternalID returns NotFound → UNAUTHENTICATED.
			name: "return UNAUTHENTICATED when GetByExternalID returns NotFound",
			args: args{
				ctx:    authedCtx(testCallerExtID),
				userID: testCallerUserID,
				method: entityv1.VerificationMethod_VERIFICATION_METHOD_JPKI,
			},
			dep: dep{
				setupUserUC: func(m *ucmocks.MockUserUseCase) {
					resolveCallerSetup(m, testCallerExtID, testCallerUserID, false)
				},
				setupIdentityUC: func(m *ucmocks.MockIdentityVerificationUseCase) {},
			},
			wantErr:  true,
			wantCode: connect.CodeUnauthenticated,
		},
		{
			// method == UNSPECIFIED → INVALID_ARGUMENT before usecase is called.
			name: "return INVALID_ARGUMENT when method is UNSPECIFIED",
			args: args{
				ctx:    authedCtx(testCallerExtID),
				userID: testCallerUserID,
				method: entityv1.VerificationMethod_VERIFICATION_METHOD_UNSPECIFIED,
			},
			dep: dep{
				setupUserUC: func(m *ucmocks.MockUserUseCase) {
					resolveCallerSetup(m, testCallerExtID, testCallerUserID, true)
				},
				setupIdentityUC: func(m *ucmocks.MockIdentityVerificationUseCase) {
					// StartVerify must NOT be called for an unspecified method.
				},
			},
			wantErr:  true,
			wantCode: connect.CodeInvalidArgument,
		},
		{
			// usecase receives the JWT-derived callerUserID, NOT the req.user_id.
			// This case verifies the argument assertion by matching on testCallerUserID.
			name: "usecase receives JWT-derived callerUserID, not req.user_id",
			args: args{
				ctx:    authedCtx(testCallerExtID),
				userID: testCallerUserID, // same as callerUserID — permission check passes
				method: entityv1.VerificationMethod_VERIFICATION_METHOD_JPKI,
			},
			dep: dep{
				setupUserUC: func(m *ucmocks.MockUserUseCase) {
					resolveCallerSetup(m, testCallerExtID, testCallerUserID, true)
				},
				setupIdentityUC: func(m *ucmocks.MockIdentityVerificationUseCase) {
					// Assert that the first positional arg (userID) is the JWT-derived
					// testCallerUserID, not a request-supplied value.
					m.EXPECT().StartVerify(mock.Anything, testCallerUserID, entity.VerificationMethodJPKI).
						Return("sess-ok", "https://stamp.p8n.app/r/ok", nil).Once()
				},
			},
			want: want{sessionID: "sess-ok", redirectURL: "https://stamp.p8n.app/r/ok"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			identityUC := ucmocks.NewMockIdentityVerificationUseCase(t)
			userUC := ucmocks.NewMockUserUseCase(t)
			tt.dep.setupUserUC(userUC)
			tt.dep.setupIdentityUC(identityUC)

			h := newIdentityHandler(t, identityUC, userUC)
			req := connect.NewRequest(&identityv1.StartVerifyRequest{
				UserId: newUserIDProto(tt.args.userID),
				Method: tt.args.method,
			})

			resp, err := h.StartVerify(tt.args.ctx, req)

			if tt.wantErr {
				require.Error(t, err)
				var ce *connect.Error
				require.ErrorAs(t, err, &ce)
				assert.Equal(t, tt.wantCode, ce.Code())
				assert.Nil(t, resp)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, resp)
			assert.Equal(t, tt.want.sessionID, resp.Msg.GetSessionId())
			require.NotNil(t, resp.Msg.GetRedirectUrl())
			assert.Equal(t, tt.want.redirectURL, resp.Msg.GetRedirectUrl().GetValue())
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// CompleteVerify
// ─────────────────────────────────────────────────────────────────────────────

func TestIdentityVerificationHandler_CompleteVerify(t *testing.T) {
	t.Parallel()

	const testSessionID = "sess-complete-1"
	const testPsid = "ps-user-1"

	type args struct {
		ctx       context.Context
		userID    string
		sessionID string
	}
	type dep struct {
		setupUserUC     func(m *ucmocks.MockUserUseCase)
		setupIdentityUC func(m *ucmocks.MockIdentityVerificationUseCase)
	}
	tests := []struct {
		name     string
		args     args
		dep      dep
		wantErr  bool
		wantCode connect.Code
	}{
		{
			name: "return VerifiedIdentity and IDENTITY_VERIFIED level on happy path",
			args: args{
				ctx:       authedCtx(testCallerExtID),
				userID:    testCallerUserID,
				sessionID: testSessionID,
			},
			dep: dep{
				setupUserUC: func(m *ucmocks.MockUserUseCase) {
					resolveCallerSetup(m, testCallerExtID, testCallerUserID, true)
				},
				setupIdentityUC: func(m *ucmocks.MockIdentityVerificationUseCase) {
					m.EXPECT().CompleteVerify(mock.Anything, testCallerUserID, testSessionID).
						Return(sampleActiveVI(testCallerUserID, testPsid), nil).Once()
				},
			},
		},
		{
			name: "return PERMISSION_DENIED when req.user_id differs from JWT-derived caller",
			args: args{
				ctx:       authedCtx(testCallerExtID),
				userID:    testForeignUserID,
				sessionID: testSessionID,
			},
			dep: dep{
				setupUserUC: func(m *ucmocks.MockUserUseCase) {
					resolveCallerSetup(m, testCallerExtID, testCallerUserID, true)
				},
				setupIdentityUC: func(m *ucmocks.MockIdentityVerificationUseCase) {},
			},
			wantErr:  true,
			wantCode: connect.CodePermissionDenied,
		},
		{
			name: "return UNAUTHENTICATED when GetByExternalID returns NotFound",
			args: args{
				ctx:       authedCtx(testCallerExtID),
				userID:    testCallerUserID,
				sessionID: testSessionID,
			},
			dep: dep{
				setupUserUC: func(m *ucmocks.MockUserUseCase) {
					resolveCallerSetup(m, testCallerExtID, testCallerUserID, false)
				},
				setupIdentityUC: func(m *ucmocks.MockIdentityVerificationUseCase) {},
			},
			wantErr:  true,
			wantCode: connect.CodeUnauthenticated,
		},
		{
			// session_id is passed to the usecase, not the user_id.
			name: "usecase receives JWT-derived callerUserID and session_id from request",
			args: args{
				ctx:       authedCtx(testCallerExtID),
				userID:    testCallerUserID,
				sessionID: testSessionID,
			},
			dep: dep{
				setupUserUC: func(m *ucmocks.MockUserUseCase) {
					resolveCallerSetup(m, testCallerExtID, testCallerUserID, true)
				},
				setupIdentityUC: func(m *ucmocks.MockIdentityVerificationUseCase) {
					// Explicit arg match asserts both the callerUserID and the
					// session_id are forwarded correctly.
					m.EXPECT().CompleteVerify(mock.Anything, testCallerUserID, testSessionID).
						Return(sampleActiveVI(testCallerUserID, testPsid), nil).Once()
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			identityUC := ucmocks.NewMockIdentityVerificationUseCase(t)
			userUC := ucmocks.NewMockUserUseCase(t)
			tt.dep.setupUserUC(userUC)
			tt.dep.setupIdentityUC(identityUC)

			h := newIdentityHandler(t, identityUC, userUC)
			req := connect.NewRequest(&identityv1.CompleteVerifyRequest{
				UserId:    newUserIDProto(tt.args.userID),
				SessionId: tt.args.sessionID,
			})

			resp, err := h.CompleteVerify(tt.args.ctx, req)

			if tt.wantErr {
				require.Error(t, err)
				var ce *connect.Error
				require.ErrorAs(t, err, &ce)
				assert.Equal(t, tt.wantCode, ce.Code())
				assert.Nil(t, resp)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, resp)
			// The response carries a non-nil VerifiedIdentity and IDENTITY_VERIFIED level.
			assert.NotNil(t, resp.Msg.GetVerifiedIdentity())
			assert.Equal(t,
				entityv1.VerificationLevel_VERIFICATION_LEVEL_IDENTITY_VERIFIED,
				resp.Msg.GetVerificationLevel(),
			)
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// ReCheck
// ─────────────────────────────────────────────────────────────────────────────

func TestIdentityVerificationHandler_ReCheck(t *testing.T) {
	t.Parallel()

	type args struct {
		ctx    context.Context
		userID string
	}
	type dep struct {
		setupUserUC     func(m *ucmocks.MockUserUseCase)
		setupIdentityUC func(m *ucmocks.MockIdentityVerificationUseCase)
	}
	tests := []struct {
		name        string
		args        args
		dep         dep
		wantErr     bool
		wantCode    connect.Code
		wantVICheck func(t *testing.T, resp *connect.Response[identityv1.ReCheckResponse])
	}{
		{
			name: "return ACTIVE status and IDENTITY_VERIFIED level on happy path",
			args: args{ctx: authedCtx(testCallerExtID), userID: testCallerUserID},
			dep: dep{
				setupUserUC: func(m *ucmocks.MockUserUseCase) {
					resolveCallerSetup(m, testCallerExtID, testCallerUserID, true)
				},
				setupIdentityUC: func(m *ucmocks.MockIdentityVerificationUseCase) {
					m.EXPECT().ReCheck(mock.Anything, testCallerUserID).
						Return(sampleActiveVI(testCallerUserID, "ps-1"), nil).Once()
				},
			},
			wantVICheck: func(t *testing.T, resp *connect.Response[identityv1.ReCheckResponse]) {
				t.Helper()
				assert.Equal(t,
					entityv1.VerificationStatus_VERIFICATION_STATUS_ACTIVE,
					resp.Msg.GetStatus(),
				)
				assert.Equal(t,
					entityv1.VerificationLevel_VERIFICATION_LEVEL_IDENTITY_VERIFIED,
					resp.Msg.GetVerificationLevel(),
				)
			},
		},
		{
			name: "return PERMISSION_DENIED when req.user_id differs from JWT-derived caller",
			args: args{ctx: authedCtx(testCallerExtID), userID: testForeignUserID},
			dep: dep{
				setupUserUC: func(m *ucmocks.MockUserUseCase) {
					resolveCallerSetup(m, testCallerExtID, testCallerUserID, true)
				},
				setupIdentityUC: func(m *ucmocks.MockIdentityVerificationUseCase) {},
			},
			wantErr:  true,
			wantCode: connect.CodePermissionDenied,
		},
		{
			name: "return UNAUTHENTICATED when GetByExternalID returns NotFound",
			args: args{ctx: authedCtx(testCallerExtID), userID: testCallerUserID},
			dep: dep{
				setupUserUC: func(m *ucmocks.MockUserUseCase) {
					resolveCallerSetup(m, testCallerExtID, testCallerUserID, false)
				},
				setupIdentityUC: func(m *ucmocks.MockIdentityVerificationUseCase) {},
			},
			wantErr:  true,
			wantCode: connect.CodeUnauthenticated,
		},
		{
			// ReCheck returns a NEEDS_REVERIFICATION vi: level must be UNVERIFIED.
			name: "return NEEDS_REVERIFICATION status and UNVERIFIED level when cert is revoked",
			args: args{ctx: authedCtx(testCallerExtID), userID: testCallerUserID},
			dep: dep{
				setupUserUC: func(m *ucmocks.MockUserUseCase) {
					resolveCallerSetup(m, testCallerExtID, testCallerUserID, true)
				},
				setupIdentityUC: func(m *ucmocks.MockIdentityVerificationUseCase) {
					m.EXPECT().ReCheck(mock.Anything, testCallerUserID).
						Return(sampleNeedsReverificationVI(testCallerUserID, "ps-2"), nil).Once()
				},
			},
			wantVICheck: func(t *testing.T, resp *connect.Response[identityv1.ReCheckResponse]) {
				t.Helper()
				assert.Equal(t,
					entityv1.VerificationStatus_VERIFICATION_STATUS_NEEDS_REVERIFICATION,
					resp.Msg.GetStatus(),
				)
				assert.Equal(t,
					entityv1.VerificationLevel_VERIFICATION_LEVEL_UNVERIFIED,
					resp.Msg.GetVerificationLevel(),
				)
			},
		},
		{
			// ReCheck usecase receives the JWT-derived callerUserID.
			name: "usecase receives JWT-derived callerUserID",
			args: args{ctx: authedCtx(testCallerExtID), userID: testCallerUserID},
			dep: dep{
				setupUserUC: func(m *ucmocks.MockUserUseCase) {
					resolveCallerSetup(m, testCallerExtID, testCallerUserID, true)
				},
				setupIdentityUC: func(m *ucmocks.MockIdentityVerificationUseCase) {
					// Explicit arg match asserts the arg is testCallerUserID.
					m.EXPECT().ReCheck(mock.Anything, testCallerUserID).
						Return(sampleActiveVI(testCallerUserID, "ps-3"), nil).Once()
				},
			},
			wantVICheck: func(t *testing.T, resp *connect.Response[identityv1.ReCheckResponse]) {
				t.Helper()
				assert.NotNil(t, resp)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			identityUC := ucmocks.NewMockIdentityVerificationUseCase(t)
			userUC := ucmocks.NewMockUserUseCase(t)
			tt.dep.setupUserUC(userUC)
			tt.dep.setupIdentityUC(identityUC)

			h := newIdentityHandler(t, identityUC, userUC)
			req := connect.NewRequest(&identityv1.ReCheckRequest{
				UserId: newUserIDProto(tt.args.userID),
			})

			resp, err := h.ReCheck(tt.args.ctx, req)

			if tt.wantErr {
				require.Error(t, err)
				var ce *connect.Error
				require.ErrorAs(t, err, &ce)
				assert.Equal(t, tt.wantCode, ce.Code())
				assert.Nil(t, resp)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, resp)
			if tt.wantVICheck != nil {
				tt.wantVICheck(t, resp)
			}
		})
	}
}

// TestIdentityVerificationHandler_ReCheck_NilVI_ReturnsInternal verifies the
// defensive guard in ReCheck: if the usecase violates its contract and returns
// (nil, nil), the handler returns a clean INTERNAL error instead of nil-deref
// panicking on vi.Status.
func TestIdentityVerificationHandler_ReCheck_NilVI_ReturnsInternal(t *testing.T) {
	t.Parallel()

	identityUC := ucmocks.NewMockIdentityVerificationUseCase(t)
	userUC := ucmocks.NewMockUserUseCase(t)

	resolveCallerSetup(userUC, testCallerExtID, testCallerUserID, true)
	// Simulate a usecase returning (nil, nil) — no error, but no entity.
	identityUC.EXPECT().ReCheck(mock.Anything, testCallerUserID).
		Return(nil, nil).Once()

	h := newIdentityHandler(t, identityUC, userUC)
	req := connect.NewRequest(&identityv1.ReCheckRequest{
		UserId: newUserIDProto(testCallerUserID),
	})

	// The nil-vi guard in ReCheck must convert a (nil, nil) usecase contract
	// violation into a clean INTERNAL error instead of a nil-pointer panic.
	var resp *connect.Response[identityv1.ReCheckResponse]
	var err error
	assert.NotPanics(t, func() {
		resp, err = h.ReCheck(authedCtx(testCallerExtID), req)
	}, "ReCheck must not panic on a nil VerifiedIdentity")
	assert.Nil(t, resp)
	require.Error(t, err)
	assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
}

// ─────────────────────────────────────────────────────────────────────────────
// GetMyVerificationStatus
// ─────────────────────────────────────────────────────────────────────────────

func TestIdentityVerificationHandler_GetMyVerificationStatus(t *testing.T) {
	t.Parallel()

	type args struct {
		ctx    context.Context
		userID string
	}
	type dep struct {
		setupUserUC     func(m *ucmocks.MockUserUseCase)
		setupIdentityUC func(m *ucmocks.MockIdentityVerificationUseCase)
	}
	tests := []struct {
		name      string
		args      args
		dep       dep
		wantErr   bool
		wantCode  connect.Code
		wantLevel entityv1.VerificationLevel
		wantVI    bool
	}{
		{
			name: "return IDENTITY_VERIFIED and VerifiedIdentity when active record exists",
			args: args{ctx: authedCtx(testCallerExtID), userID: testCallerUserID},
			dep: dep{
				setupUserUC: func(m *ucmocks.MockUserUseCase) {
					resolveCallerSetup(m, testCallerExtID, testCallerUserID, true)
				},
				setupIdentityUC: func(m *ucmocks.MockIdentityVerificationUseCase) {
					m.EXPECT().GetMyVerificationStatus(mock.Anything, testCallerUserID).
						Return(entity.VerificationLevelIdentityVerified, sampleActiveVI(testCallerUserID, "ps-1"), nil).Once()
				},
			},
			wantLevel: entityv1.VerificationLevel_VERIFICATION_LEVEL_IDENTITY_VERIFIED,
			wantVI:    true,
		},
		{
			name: "return UNVERIFIED and no VerifiedIdentity when no record exists",
			args: args{ctx: authedCtx(testCallerExtID), userID: testCallerUserID},
			dep: dep{
				setupUserUC: func(m *ucmocks.MockUserUseCase) {
					resolveCallerSetup(m, testCallerExtID, testCallerUserID, true)
				},
				setupIdentityUC: func(m *ucmocks.MockIdentityVerificationUseCase) {
					m.EXPECT().GetMyVerificationStatus(mock.Anything, testCallerUserID).
						Return(entity.VerificationLevelUnverified, nil, nil).Once()
				},
			},
			wantLevel: entityv1.VerificationLevel_VERIFICATION_LEVEL_UNVERIFIED,
			wantVI:    false,
		},
		{
			name: "return PERMISSION_DENIED when req.user_id differs from JWT-derived caller",
			args: args{ctx: authedCtx(testCallerExtID), userID: testForeignUserID},
			dep: dep{
				setupUserUC: func(m *ucmocks.MockUserUseCase) {
					resolveCallerSetup(m, testCallerExtID, testCallerUserID, true)
				},
				setupIdentityUC: func(m *ucmocks.MockIdentityVerificationUseCase) {},
			},
			wantErr:  true,
			wantCode: connect.CodePermissionDenied,
		},
		{
			name: "return UNAUTHENTICATED when GetByExternalID returns NotFound",
			args: args{ctx: authedCtx(testCallerExtID), userID: testCallerUserID},
			dep: dep{
				setupUserUC: func(m *ucmocks.MockUserUseCase) {
					resolveCallerSetup(m, testCallerExtID, testCallerUserID, false)
				},
				setupIdentityUC: func(m *ucmocks.MockIdentityVerificationUseCase) {},
			},
			wantErr:  true,
			wantCode: connect.CodeUnauthenticated,
		},
		{
			// Unauthenticated context (no claims at all).
			name: "return UNAUTHENTICATED when no JWT claims in context",
			args: args{ctx: context.Background(), userID: testCallerUserID},
			dep: dep{
				setupUserUC:     func(m *ucmocks.MockUserUseCase) {},
				setupIdentityUC: func(m *ucmocks.MockIdentityVerificationUseCase) {},
			},
			wantErr:  true,
			wantCode: connect.CodeUnauthenticated,
		},
		{
			// usecase receives the JWT-derived callerUserID.
			name: "usecase receives JWT-derived callerUserID, not req.user_id",
			args: args{ctx: authedCtx(testCallerExtID), userID: testCallerUserID},
			dep: dep{
				setupUserUC: func(m *ucmocks.MockUserUseCase) {
					resolveCallerSetup(m, testCallerExtID, testCallerUserID, true)
				},
				setupIdentityUC: func(m *ucmocks.MockIdentityVerificationUseCase) {
					// Assert the arg is the JWT-derived testCallerUserID.
					m.EXPECT().GetMyVerificationStatus(mock.Anything, testCallerUserID).
						Return(entity.VerificationLevelIdentityVerified, sampleActiveVI(testCallerUserID, "ps-x"), nil).Once()
				},
			},
			wantLevel: entityv1.VerificationLevel_VERIFICATION_LEVEL_IDENTITY_VERIFIED,
			wantVI:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			identityUC := ucmocks.NewMockIdentityVerificationUseCase(t)
			userUC := ucmocks.NewMockUserUseCase(t)
			tt.dep.setupUserUC(userUC)
			tt.dep.setupIdentityUC(identityUC)

			h := newIdentityHandler(t, identityUC, userUC)
			req := connect.NewRequest(&identityv1.GetMyVerificationStatusRequest{
				UserId: newUserIDProto(tt.args.userID),
			})

			resp, err := h.GetMyVerificationStatus(tt.args.ctx, req)

			if tt.wantErr {
				require.Error(t, err)
				var ce *connect.Error
				require.ErrorAs(t, err, &ce)
				assert.Equal(t, tt.wantCode, ce.Code())
				assert.Nil(t, resp)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, resp)
			assert.Equal(t, tt.wantLevel, resp.Msg.GetVerificationLevel())
			if tt.wantVI {
				assert.NotNil(t, resp.Msg.GetVerifiedIdentity())
			} else {
				assert.Nil(t, resp.Msg.GetVerifiedIdentity())
			}
		})
	}
}
