package rpc_test

import (
	"context"
	"errors"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	adminconcertv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/concert/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	usecasemocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// adminCtx returns a context carrying admin role claims.
func adminCtx() context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{
		Sub:   "admin-sub-123",
		Roles: []string{"admin"},
	})
}

// Admin-role enforcement lives in the admin server's boundary
// RequireRoleInterceptor (see internal/infrastructure/auth/authz_test.go), not in
// these handler methods, so these tests exercise only proto<->entity mapping.

func newAdminConcertHandler(
	t *testing.T,
	adminUC *usecasemocks.MockAdminConcertUseCase,
) *handler.AdminConcertHandler {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return handler.NewAdminConcertHandler(adminUC, mapper.NewSeriesImageURLBuilder(""), logger)
}

// ---------- List ----------

func TestAdminConcertHandler_List(t *testing.T) {
	t.Parallel()

	localDate := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	concertA := &entity.Concert{
		ID: "event-1", VenueID: "venue-1", LocalDate: localDate,
		Series:  &entity.Series{ID: "series-1", Title: "Tour Alpha", Type: entity.SeriesTypeTour},
		Artists: []*entity.Artist{{ID: "artist-1", Name: "Artist Alpha", MBID: "mbid-a"}},
	}
	concertB := &entity.Concert{
		ID: "event-2", VenueID: "venue-2", LocalDate: localDate,
		Series:  &entity.Series{ID: "series-2", Title: "Tour Beta", Type: entity.SeriesTypeTour},
		Artists: []*entity.Artist{{ID: "artist-2", Name: "Artist Beta", MBID: "mbid-b"}},
	}
	alpha := &entity.Artist{ID: "artist-1", Name: "Artist Alpha", MBID: "mbid-a"}
	tourDay := func(id string) *entity.Concert {
		return &entity.Concert{
			ID: id, SeriesID: "series-1", VenueID: "venue-1", LocalDate: localDate,
			Series:  &entity.Series{ID: "series-1", Title: "Tour Alpha", Type: entity.SeriesTypeTour},
			Artists: []*entity.Artist{alpha},
		}
	}

	type args struct {
		ctx context.Context
	}
	type dep struct {
		adminUC func(*usecasemocks.MockAdminConcertUseCase)
	}

	tests := []struct {
		name     string
		args     args
		dep      dep
		wantErr  bool
		wantCode connect.Code
		check    func(t *testing.T, resp *connect.Response[adminconcertv1.ListResponse])
	}{
		{
			// @spec components/adapter/admin/api/rpc/concert "Admin lists concerts"
			name: "map use-case concerts to ListResponse.Concerts",
			args: args{ctx: adminCtx()},
			dep: dep{
				adminUC: func(m *usecasemocks.MockAdminConcertUseCase) {
					m.EXPECT().List(mock.Anything).Return([]*entity.Concert{concertA, concertB}, nil).Once()
				},
			},
			check: func(t *testing.T, resp *connect.Response[adminconcertv1.ListResponse]) {
				t.Helper()
				require.Len(t, resp.Msg.Concerts, 2)
				assert.Equal(t, "event-1", resp.Msg.Concerts[0].GetEvent().GetId().GetValue())
				assert.Equal(t, "event-2", resp.Msg.Concerts[1].GetEvent().GetId().GetValue())
				require.Len(t, resp.Msg.Series, 2)
				assert.Equal(t, "Tour Alpha", resp.Msg.Series[0].GetTitle().GetValue())
				require.Len(t, resp.Msg.Artists, 2)
				assert.Equal(t, "Artist Alpha", resp.Msg.Artists[0].GetName().GetValue())
			},
		},
		{
			// @spec components/adapter/admin/api/rpc/concert "Two approved concerts of one series"
			name: "return the shared series and artist once for two concerts of one series",
			args: args{ctx: adminCtx()},
			dep: dep{
				adminUC: func(m *usecasemocks.MockAdminConcertUseCase) {
					m.EXPECT().List(mock.Anything).Return([]*entity.Concert{tourDay("event-1"), tourDay("event-2")}, nil).Once()
				},
			},
			check: func(t *testing.T, resp *connect.Response[adminconcertv1.ListResponse]) {
				t.Helper()
				require.Len(t, resp.Msg.Series, 1)
				assert.Equal(t, "series-1", resp.Msg.Series[0].GetId().GetValue())
				require.Len(t, resp.Msg.Artists, 1)
				assert.Equal(t, "artist-1", resp.Msg.Artists[0].GetId().GetValue())
				require.Len(t, resp.Msg.Concerts, 2)
				for _, c := range resp.Msg.Concerts {
					assert.Equal(t, "series-1", c.GetEvent().GetSeriesId().GetValue())
					require.Len(t, c.GetArtistIds(), 1)
					assert.Equal(t, "artist-1", c.GetArtistIds()[0].GetValue())
				}
			},
		},
		{
			name: "return empty Concerts slice when repo returns nothing",
			args: args{ctx: adminCtx()},
			dep: dep{
				adminUC: func(m *usecasemocks.MockAdminConcertUseCase) {
					m.EXPECT().List(mock.Anything).Return(nil, nil).Once()
				},
			},
			check: func(t *testing.T, resp *connect.Response[adminconcertv1.ListResponse]) {
				t.Helper()
				assert.Empty(t, resp.Msg.Concerts)
				assert.Empty(t, resp.Msg.Series)
				assert.Empty(t, resp.Msg.Artists)
			},
		},
		{
			name:    "propagate use-case error",
			args:    args{ctx: adminCtx()},
			wantErr: true,
			dep: dep{
				adminUC: func(m *usecasemocks.MockAdminConcertUseCase) {
					m.EXPECT().List(mock.Anything).Return(nil, errors.New("db failure")).Once()
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			adminUC := usecasemocks.NewMockAdminConcertUseCase(t)
			tt.dep.adminUC(adminUC)

			h := newAdminConcertHandler(t, adminUC)
			resp, err := h.List(tt.args.ctx, connect.NewRequest(&adminconcertv1.ListRequest{}))

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, resp)
				return
			}
			require.NoError(t, err)
			tt.check(t, resp)
		})
	}
}

// ---------- Delete ----------

func TestAdminConcertHandler_Delete(t *testing.T) {
	t.Parallel()

	type args struct {
		ctx     context.Context
		eventID string
	}
	type dep struct {
		adminUC func(*usecasemocks.MockAdminConcertUseCase)
	}

	tests := []struct {
		name     string
		args     args
		dep      dep
		wantErr  bool
		wantCode connect.Code
		check    func(t *testing.T, resp *connect.Response[adminconcertv1.DeleteResponse])
	}{
		{
			name: "pass event id through and return DeleteResponse",
			args: args{ctx: adminCtx(), eventID: "event-abc"},
			dep: dep{
				adminUC: func(m *usecasemocks.MockAdminConcertUseCase) {
					m.EXPECT().Delete(mock.Anything, "event-abc").Return(nil).Once()
				},
			},
			check: func(t *testing.T, resp *connect.Response[adminconcertv1.DeleteResponse]) {
				t.Helper()
				assert.NotNil(t, resp.Msg)
			},
		},
		{
			name:    "propagate use-case error",
			args:    args{ctx: adminCtx(), eventID: "event-bad"},
			wantErr: true,
			dep: dep{
				adminUC: func(m *usecasemocks.MockAdminConcertUseCase) {
					m.EXPECT().Delete(mock.Anything, "event-bad").Return(errors.New("delete failed")).Once()
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			adminUC := usecasemocks.NewMockAdminConcertUseCase(t)
			tt.dep.adminUC(adminUC)

			h := newAdminConcertHandler(t, adminUC)
			resp, err := h.Delete(tt.args.ctx, connect.NewRequest(&adminconcertv1.DeleteRequest{
				EventId: &entityv1.EventId{Value: tt.args.eventID},
			}))

			if tt.wantErr {
				require.Error(t, err)
				assert.Nil(t, resp)
				return
			}
			require.NoError(t, err)
			tt.check(t, resp)
		})
	}
}

func TestAdminConcertHandler_Delete_Boundary(t *testing.T) {
	t.Parallel()

	t.Run("non-admin deletes", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/concert "Non-admin deletes"
		// The mock fails the test if the usecase runs.
		h := newAdminConcertHandler(t, usecasemocks.NewMockAdminConcertUseCase(t))

		_, err := callThroughAdminPipeline(t, nonAdminCtx(), &adminconcertv1.DeleteRequest{
			EventId: &entityv1.EventId{Value: "019a0000-0000-7000-8000-0000000000e1"},
		}, h.Delete)

		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})

	t.Run("delete without an event", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/concert "Delete without an event"
		h := newAdminConcertHandler(t, usecasemocks.NewMockAdminConcertUseCase(t))

		_, err := callThroughAdminPipeline(t, adminCtx(), &adminconcertv1.DeleteRequest{}, h.Delete)

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}
