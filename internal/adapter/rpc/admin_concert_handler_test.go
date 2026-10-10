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
	"github.com/liverty-music/backend/internal/usecase"
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
	return handler.NewAdminConcertHandler(adminUC, mapper.NewMediaURLBuilder(""), logger)
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

// ---------- ListPending ----------

func TestAdminConcertHandler_ListPending(t *testing.T) {
	t.Parallel()

	lat, lon := 35.6762, 139.6503
	placeID := "ChIJM..."
	venueName := "武道館"
	adminArea := "JP-13"
	sourceURL := "https://example.com/concert"

	stagedWithVenue := &entity.StagedConcert{
		ID:                "staged-1",
		ArtistID:          "artist-1",
		Title:             "Tour 2026",
		LocalDate:         time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
		ListedVenueName:   "Budokan",
		ResolvedPlaceID:   &placeID,
		ResolvedVenueName: &venueName,
		ResolvedAdminArea: &adminArea,
		ResolvedLatitude:  &lat,
		ResolvedLongitude: &lon,
		SourceURL:         &sourceURL,
		DiscoveredTime:    time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC),
	}

	stagedNoVenue := &entity.StagedConcert{
		ID:              "staged-2",
		ArtistID:        "artist-2",
		Title:           "Single Show",
		LocalDate:       time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC),
		ListedVenueName: "Mystery Venue",
		DiscoveredTime:  time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC),
	}

	artistA := &entity.Artist{ID: "artist-1", Name: "Artist Alpha", MBID: "mbid-a"}
	artistB := &entity.Artist{ID: "artist-2", Name: "Artist Beta", MBID: "mbid-b"}

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
		check    func(t *testing.T, resp *connect.Response[adminconcertv1.ListPendingResponse])
	}{
		{
			name: "return pending concerts with resolved venue",
			args: args{ctx: adminCtx()},
			dep: dep{
				adminUC: func(m *usecasemocks.MockAdminConcertUseCase) {
					m.EXPECT().ListPending(mock.Anything).Return([]*usecase.PendingConcertReview{
						{Staged: stagedWithVenue, Performer: artistA},
					}, nil).Once()
				},
			},
			check: func(t *testing.T, resp *connect.Response[adminconcertv1.ListPendingResponse]) {
				t.Helper()
				require.Len(t, resp.Msg.PendingConcerts, 1)
				pc := resp.Msg.PendingConcerts[0]
				assert.Equal(t, "staged-1", pc.GetStagedId().GetValue())
				assert.Equal(t, "Artist Alpha", pc.GetPerformer().GetName().GetValue())
				assert.Equal(t, "Tour 2026", pc.GetTitle().GetValue())
				assert.Equal(t, "Budokan", pc.GetListedVenueName().GetValue())
				assert.NotNil(t, pc.GetResolvedVenue())
				assert.Equal(t, "武道館", pc.GetResolvedVenue().GetName().GetValue())
				assert.Equal(t, "ChIJM...", pc.GetResolvedVenue().GetPlaceId().GetValue())
				assert.Equal(t, "JP-13", pc.GetResolvedVenue().GetAdminArea().GetValue())
				assert.InDelta(t, 35.6762, pc.GetResolvedVenue().GetCoordinates().GetLatitude(), 1e-6)
				assert.InDelta(t, 139.6503, pc.GetResolvedVenue().GetCoordinates().GetLongitude(), 1e-6)
				assert.Equal(t, "https://example.com/concert", pc.GetSourceUrl().GetValue())
				assert.NotNil(t, pc.GetDiscoveredTime())
			},
		},
		{
			name: "return pending concerts without resolved venue",
			args: args{ctx: adminCtx()},
			dep: dep{
				adminUC: func(m *usecasemocks.MockAdminConcertUseCase) {
					m.EXPECT().ListPending(mock.Anything).Return([]*usecase.PendingConcertReview{
						{Staged: stagedNoVenue, Performer: artistB},
					}, nil).Once()
				},
			},
			check: func(t *testing.T, resp *connect.Response[adminconcertv1.ListPendingResponse]) {
				t.Helper()
				require.Len(t, resp.Msg.PendingConcerts, 1)
				pc := resp.Msg.PendingConcerts[0]
				assert.Equal(t, "staged-2", pc.GetStagedId().GetValue())
				assert.Nil(t, pc.GetResolvedVenue())
				assert.Nil(t, pc.GetSourceUrl())
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			adminUC := usecasemocks.NewMockAdminConcertUseCase(t)
			tt.dep.adminUC(adminUC)

			h := newAdminConcertHandler(t, adminUC)
			resp, err := h.ListPending(tt.args.ctx, connect.NewRequest(&adminconcertv1.ListPendingRequest{}))

			if tt.wantErr {
				require.Error(t, err)
				var connErr *connect.Error
				require.ErrorAs(t, err, &connErr)
				assert.Equal(t, tt.wantCode, connErr.Code())
				assert.Nil(t, resp)
				return
			}
			require.NoError(t, err)
			tt.check(t, resp)
		})
	}
}

// ---------- Approve ----------

func TestAdminConcertHandler_Approve(t *testing.T) {
	t.Parallel()

	type args struct {
		ctx      context.Context
		stagedID string
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
	}{
		{
			name: "approve calls use case with staged id",
			args: args{ctx: adminCtx(), stagedID: "staged-abc"},
			dep: dep{
				adminUC: func(m *usecasemocks.MockAdminConcertUseCase) {
					m.EXPECT().Approve(mock.Anything, "staged-abc", usecase.ApproveResolutionUnspecified, mock.Anything).
						Return(&usecase.ApproveResult{}, nil).Once()
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
			resp, err := h.Approve(tt.args.ctx, connect.NewRequest(&adminconcertv1.ApproveRequest{
				StagedId: &entityv1.StagedConcertId{Value: tt.args.stagedID},
			}))

			if tt.wantErr {
				require.Error(t, err)
				var connErr *connect.Error
				require.ErrorAs(t, err, &connErr)
				assert.Equal(t, tt.wantCode, connErr.Code())
				assert.Nil(t, resp)
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, resp)
		})
	}
}

// ---------- Reject ----------

func TestAdminConcertHandler_Reject(t *testing.T) {
	t.Parallel()

	type args struct {
		ctx      context.Context
		stagedID string
		reason   string
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
	}{
		{
			name: "reject passes reason and reviewer sub to use case",
			args: args{ctx: adminCtx(), stagedID: "staged-xyz", reason: "wrong date"},
			dep: dep{
				adminUC: func(m *usecasemocks.MockAdminConcertUseCase) {
					// reviewer sub must match the sub in adminCtx.
					m.EXPECT().Reject(mock.Anything, "staged-xyz", "wrong date", "admin-sub-123").Return(nil).Once()
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
			resp, err := h.Reject(tt.args.ctx, connect.NewRequest(&adminconcertv1.RejectRequest{
				StagedId: &entityv1.StagedConcertId{Value: tt.args.stagedID},
				Reason:   tt.args.reason,
			}))

			if tt.wantErr {
				require.Error(t, err)
				var connErr *connect.Error
				require.ErrorAs(t, err, &connErr)
				assert.Equal(t, tt.wantCode, connErr.Code())
				assert.Nil(t, resp)
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, resp)
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
