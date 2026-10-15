package rpc_test

import (
	"context"
	"testing"

	organizerartistv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/artist/v1"
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

func newOrganizerArtistHandler(t *testing.T, uc *ucmocks.MockOrganizerUseCase, artistUC *ucmocks.MockArtistUseCase) *rpc.OrganizerArtistHandler {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return rpc.NewOrganizerArtistHandler(artistUC, uc, logger)
}

func TestOrganizerArtistHandler_List(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		ctx         context.Context
		setupUC     func(m *ucmocks.MockOrganizerUseCase, a *ucmocks.MockArtistUseCase)
		wantArtists int
		wantCode    connect.Code
	}{
		{
			// @spec components/adapter/organizer/api/rpc/artist "Operator lists their own roster"
			name: "return the caller's own roster",
			ctx:  orgCtx(testProviderOrganizerID),
			setupUC: func(m *ucmocks.MockOrganizerUseCase, a *ucmocks.MockArtistUseCase) {
				m.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).Return(activeOrganizer(), nil).Once()
				a.EXPECT().ListByOrganizer(mock.Anything, testOrganizerID).Return([]*entity.Artist{
					{ID: "artist-1", Name: "Artist One"},
					{ID: "artist-2", Name: "Artist Two"},
				}, nil).Once()
			},
			wantArtists: 2,
		},
		{
			// @spec components/adapter/organizer/api/rpc/artist "Operator lists their own roster"
			name: "return an empty roster when the Organizer represents no artists",
			ctx:  orgCtx(testProviderOrganizerID),
			setupUC: func(m *ucmocks.MockOrganizerUseCase, a *ucmocks.MockArtistUseCase) {
				m.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).Return(activeOrganizer(), nil).Once()
				a.EXPECT().ListByOrganizer(mock.Anything, testOrganizerID).Return([]*entity.Artist{}, nil).Once()
			},
			wantArtists: 0,
		},
		{
			// @spec components/adapter/organizer/api/rpc/artist "Deactivated Organizer"
			name: "return FAILED_PRECONDITION when the caller's Organizer is deactivated",
			ctx:  orgCtx(testProviderOrganizerID),
			setupUC: func(m *ucmocks.MockOrganizerUseCase, a *ucmocks.MockArtistUseCase) {
				m.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).
					Return(nil, apperr.New(apperr.ErrFailedPrecondition.Code, "organizer is deactivated")).Once()
			},
			wantCode: connect.CodeFailedPrecondition,
		},
		{
			name: "return PERMISSION_DENIED when no Organizer is linked to the tenant",
			ctx:  orgCtx(testProviderOrganizerID),
			setupUC: func(m *ucmocks.MockOrganizerUseCase, a *ucmocks.MockArtistUseCase) {
				m.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).
					Return(nil, apperr.New(apperr.ErrPermissionDenied.Code, "permission denied")).Once()
			},
			wantCode: connect.CodePermissionDenied,
		},
		{
			name:     "return PERMISSION_DENIED when the caller's tenant is absent from the context",
			ctx:      noOrgCtx(),
			setupUC:  func(_ *ucmocks.MockOrganizerUseCase, _ *ucmocks.MockArtistUseCase) {},
			wantCode: connect.CodePermissionDenied,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			uc, artistUC := ucmocks.NewMockOrganizerUseCase(t), ucmocks.NewMockArtistUseCase(t)
			tt.setupUC(uc, artistUC)

			resp, err := newOrganizerArtistHandler(t, uc, artistUC).List(tt.ctx, connect.NewRequest(&organizerartistv1.ListRequest{}))

			if tt.wantCode != 0 {
				assert.Equal(t, tt.wantCode, connectCodeOf(err))
				assert.Nil(t, resp)
				return
			}
			require.NoError(t, err)
			assert.Len(t, resp.Msg.GetArtists(), tt.wantArtists)
		})
	}

	t.Run("sign-in for another application", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/organizer/api/rpc/artist "Sign-in for another application"
		// The organizer server's OrgScopedInterceptor rejects a token whose
		// audience is not the organizer console before the handler runs; the
		// mock fails the test if the usecase is reached.
		h := newOrganizerArtistHandler(t, ucmocks.NewMockOrganizerUseCase(t), ucmocks.NewMockArtistUseCase(t))
		next := connect.UnaryFunc(func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			return h.List(ctx, req.(*connect.Request[organizerartistv1.ListRequest]))
		})
		ctx := auth.WithClaims(context.Background(), &auth.Claims{
			Sub:             "op-sub",
			Audiences:       []string{"fan-app-project"},
			LoginScopeOrgID: testProviderOrganizerID,
			RoleOrgIDs:      map[string]struct{}{testProviderOrganizerID: {}},
		})

		_, err := auth.NewOrgScopedInterceptor("organizer-console-project", nil).WrapUnary(next)(ctx, connect.NewRequest(&organizerartistv1.ListRequest{}))

		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})
}
