package rpc_test

import (
	"context"
	"errors"
	"testing"

	entitypb "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	organizerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// connectCodeOf returns the Connect code that err would surface to a client.
// A handler-constructed error is already a *connect.Error; an error returned
// directly by a mocked usecase (as in these tests, which call the handler
// without the production interceptor chain) is a bare *apperr.AppErr, which
// the real apperr_connect.ErrorHandlingInterceptor converts to *connect.Error
// at runtime. This helper performs the same mapping so these unit tests can
// assert the client-visible code without standing up that interceptor.
func connectCodeOf(err error) connect.Code {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce.Code()
	}
	var ae *apperr.AppErr
	if errors.As(err, &ae) {
		return connect.Code(ae.Code)
	}
	return connect.CodeUnknown
}

// ── test constants ────────────────────────────────────────────────────────────

const (
	testOrganizerID        = "org-entity-111"
	testOrganizerName      = "Test Label"
	testZitadelOrgID       = "zitadel-org-abc"
	testForeignOrganizerID = "org-entity-999"
)

// ── context helpers ──────────────────────────────────────────────────────────

// orgCtx returns a context that simulates passing through both
// ClaimsBridgeInterceptor and OrgScopedInterceptor: claims are set and the
// caller's Zitadel org id is stored by WithCallerOrgID.
func orgCtx(zitadelOrgID string) context.Context {
	ctx := auth.WithClaims(context.Background(), &auth.Claims{Sub: "op-sub"})
	return auth.WithCallerOrgID(ctx, zitadelOrgID)
}

// noOrgCtx returns a context with claims but without the caller-org id —
// simulates a misconfigured or missing OrgScopedInterceptor.
func noOrgCtx() context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{Sub: "op-sub"})
}

// ── entity helpers ────────────────────────────────────────────────────────────

func activeOrganizer() *entity.Organizer {
	return &entity.Organizer{
		ID:           testOrganizerID,
		Name:         testOrganizerName,
		ZitadelOrgID: testZitadelOrgID,
		Status:       entity.OrganizerStatusActive,
	}
}

// ── OrganizerHandler.Get ─────────────────────────────────────────────────────

func TestOrganizerHandler_Get(t *testing.T) {
	t.Parallel()

	type args struct {
		ctx context.Context
	}
	type dep struct {
		setupUC func(m *ucmocks.MockOrganizerUseCase)
	}
	tests := []struct {
		name     string
		args     args
		dep      dep
		wantCode connect.Code
		wantOrg  bool
		wantErr  bool
	}{
		{
			name: "return own organizer when status is active",
			args: args{ctx: orgCtx(testZitadelOrgID)},
			dep: dep{setupUC: func(m *ucmocks.MockOrganizerUseCase) {
				m.On("ResolveCaller", mock.Anything, testZitadelOrgID).
					Return(activeOrganizer(), nil).Once()
			}},
			wantOrg: true,
		},
		// The Organizer-status→code mapping now lives in
		// OrganizerUseCase.ResolveCaller (see organizer_uc_test.go); these
		// cases only verify the handler forwards whatever it returns.
		{
			name: "return FAILED_PRECONDITION when own organizer is deactivated",
			args: args{ctx: orgCtx(testZitadelOrgID)},
			dep: dep{setupUC: func(m *ucmocks.MockOrganizerUseCase) {
				m.On("ResolveCaller", mock.Anything, testZitadelOrgID).
					Return((*entity.Organizer)(nil), apperr.New(apperr.ErrFailedPrecondition.Code, "organizer is deactivated")).Once()
			}},
			wantErr:  true,
			wantCode: connect.CodeFailedPrecondition,
		},
		{
			name: "return PERMISSION_DENIED when no organizer linked to zitadel org",
			args: args{ctx: orgCtx(testZitadelOrgID)},
			dep: dep{setupUC: func(m *ucmocks.MockOrganizerUseCase) {
				m.On("ResolveCaller", mock.Anything, testZitadelOrgID).
					Return((*entity.Organizer)(nil), apperr.New(apperr.ErrPermissionDenied.Code, "permission denied")).Once()
			}},
			wantErr:  true,
			wantCode: connect.CodePermissionDenied,
		},
		{
			name: "return PERMISSION_DENIED when organizer is in provisioning status",
			args: args{ctx: orgCtx(testZitadelOrgID)},
			dep: dep{setupUC: func(m *ucmocks.MockOrganizerUseCase) {
				m.On("ResolveCaller", mock.Anything, testZitadelOrgID).
					Return((*entity.Organizer)(nil), apperr.New(apperr.ErrPermissionDenied.Code, "permission denied")).Once()
			}},
			wantErr:  true,
			wantCode: connect.CodePermissionDenied,
		},
		{
			// Defence-in-depth: interceptor must have set the caller org id.
			name:     "return PERMISSION_DENIED when caller org id is absent from context",
			args:     args{ctx: noOrgCtx()},
			dep:      dep{setupUC: func(m *ucmocks.MockOrganizerUseCase) {}}, // usecase must NOT be called
			wantErr:  true,
			wantCode: connect.CodePermissionDenied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ucMock := ucmocks.NewMockOrganizerUseCase(t)
			tt.dep.setupUC(ucMock)

			logger, err := logging.New()
			require.NoError(t, err)
			h := rpc.NewOrganizerHandler(ucMock, logger)
			resp, err := h.Get(tt.args.ctx, connect.NewRequest(&organizerv1.GetRequest{}))

			if tt.wantErr {
				require.Error(t, err)
				assert.Equal(t, tt.wantCode, connectCodeOf(err))
				assert.Nil(t, resp)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, resp)
			if tt.wantOrg {
				assert.Equal(t, testOrganizerID, resp.Msg.Organizer.Id.Value)
				assert.Equal(t, testOrganizerName, resp.Msg.Organizer.Name.Value)
			}
		})
	}
}

// ── OrganizerHandler.ListArtists ─────────────────────────────────────────────

func TestOrganizerHandler_ListArtists(t *testing.T) {
	t.Parallel()

	type args struct {
		ctx context.Context
		req *connect.Request[organizerv1.ListArtistsRequest]
	}
	type dep struct {
		setupUC func(m *ucmocks.MockOrganizerUseCase)
	}

	listReq := func(orgID string) *connect.Request[organizerv1.ListArtistsRequest] {
		return connect.NewRequest(&organizerv1.ListArtistsRequest{
			OrganizerId: &entitypb.OrganizerId{Value: orgID},
		})
	}

	sampleArtists := []*entity.Artist{
		{ID: "artist-1", Name: "Artist One"},
		{ID: "artist-2", Name: "Artist Two"},
	}

	tests := []struct {
		name        string
		args        args
		dep         dep
		wantCode    connect.Code
		wantArtists int
		wantErr     bool
	}{
		{
			name: "return own roster ascending by artist id",
			args: args{
				ctx: orgCtx(testZitadelOrgID),
				req: listReq(testOrganizerID),
			},
			dep: dep{setupUC: func(m *ucmocks.MockOrganizerUseCase) {
				m.On("ResolveCaller", mock.Anything, testZitadelOrgID).
					Return(activeOrganizer(), nil).Once()
				m.On("ListOwnArtists", mock.Anything, testOrganizerID, testOrganizerID).
					Return(sampleArtists, nil).Once()
			}},
			wantArtists: 2,
		},
		{
			name: "return empty roster when organizer represents no artists",
			args: args{
				ctx: orgCtx(testZitadelOrgID),
				req: listReq(testOrganizerID),
			},
			dep: dep{setupUC: func(m *ucmocks.MockOrganizerUseCase) {
				m.On("ResolveCaller", mock.Anything, testZitadelOrgID).
					Return(activeOrganizer(), nil).Once()
				m.On("ListOwnArtists", mock.Anything, testOrganizerID, testOrganizerID).
					Return([]*entity.Artist{}, nil).Once()
			}},
			wantArtists: 0,
		},
		{
			// Cross-organizer request: organizer_id resolves to a different org.
			// The ownership check now lives in OrganizerUseCase.ListOwnArtists
			// (see organizer_uc_test.go); this case only verifies the handler
			// forwards whatever it returns.
			name: "return PERMISSION_DENIED when organizer_id does not match caller's organizer",
			args: args{
				ctx: orgCtx(testZitadelOrgID),
				req: listReq(testForeignOrganizerID),
			},
			dep: dep{setupUC: func(m *ucmocks.MockOrganizerUseCase) {
				m.On("ResolveCaller", mock.Anything, testZitadelOrgID).
					Return(activeOrganizer(), nil).Once()
				m.On("ListOwnArtists", mock.Anything, testOrganizerID, testForeignOrganizerID).
					Return(nil, apperr.New(apperr.ErrPermissionDenied.Code, "permission denied")).Once()
			}},
			wantErr:  true,
			wantCode: connect.CodePermissionDenied,
		},
		{
			name: "return FAILED_PRECONDITION when own organizer is deactivated",
			args: args{
				ctx: orgCtx(testZitadelOrgID),
				req: listReq(testOrganizerID),
			},
			dep: dep{setupUC: func(m *ucmocks.MockOrganizerUseCase) {
				m.On("ResolveCaller", mock.Anything, testZitadelOrgID).
					Return((*entity.Organizer)(nil), apperr.New(apperr.ErrFailedPrecondition.Code, "organizer is deactivated")).Once()
			}},
			wantErr:  true,
			wantCode: connect.CodeFailedPrecondition,
		},
		{
			name: "return PERMISSION_DENIED when no organizer linked to caller's zitadel org",
			args: args{
				ctx: orgCtx(testZitadelOrgID),
				req: listReq(testOrganizerID),
			},
			dep: dep{setupUC: func(m *ucmocks.MockOrganizerUseCase) {
				m.On("ResolveCaller", mock.Anything, testZitadelOrgID).
					Return((*entity.Organizer)(nil), apperr.New(apperr.ErrPermissionDenied.Code, "permission denied")).Once()
			}},
			wantErr:  true,
			wantCode: connect.CodePermissionDenied,
		},
		{
			name: "return PERMISSION_DENIED when caller org id absent from context",
			args: args{
				ctx: noOrgCtx(),
				req: listReq(testOrganizerID),
			},
			dep:      dep{setupUC: func(m *ucmocks.MockOrganizerUseCase) {}},
			wantErr:  true,
			wantCode: connect.CodePermissionDenied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ucMock := ucmocks.NewMockOrganizerUseCase(t)
			tt.dep.setupUC(ucMock)

			logger, err := logging.New()
			require.NoError(t, err)
			h := rpc.NewOrganizerHandler(ucMock, logger)
			resp, err := h.ListArtists(tt.args.ctx, tt.args.req)

			if tt.wantErr {
				require.Error(t, err)
				assert.Equal(t, tt.wantCode, connectCodeOf(err))
				assert.Nil(t, resp)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, resp)
			assert.Len(t, resp.Msg.Artists, tt.wantArtists)
		})
	}
}
