package rpc_test

import (
	"context"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	stagedconcertv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/staged_concert/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	usecasemocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newAdminStagedConcertHandler(
	t *testing.T,
	adminUC *usecasemocks.MockAdminConcertUseCase,
) *handler.AdminStagedConcertHandler {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return handler.NewAdminStagedConcertHandler(adminUC, logger)
}

// ---------- List ----------

func TestAdminStagedConcertHandler_List(t *testing.T) {
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
		check    func(t *testing.T, resp *connect.Response[stagedconcertv1.ListResponse])
	}{
		{
			// @spec components/adapter/admin/api/rpc/staged-concert "Admin lists pending concerts"
			name: "return pending concerts with resolved venue",
			args: args{ctx: adminCtx()},
			dep: dep{
				adminUC: func(m *usecasemocks.MockAdminConcertUseCase) {
					m.EXPECT().ListPending(mock.Anything).Return([]*usecase.PendingConcertReview{
						{Staged: stagedWithVenue, Performer: artistA},
					}, nil).Once()
				},
			},
			check: func(t *testing.T, resp *connect.Response[stagedconcertv1.ListResponse]) {
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
			check: func(t *testing.T, resp *connect.Response[stagedconcertv1.ListResponse]) {
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

			h := newAdminStagedConcertHandler(t, adminUC)
			resp, err := h.List(tt.args.ctx, connect.NewRequest(&stagedconcertv1.ListRequest{}))

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

func TestAdminStagedConcertHandler_Approve(t *testing.T) {
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

			h := newAdminStagedConcertHandler(t, adminUC)
			resp, err := h.Approve(tt.args.ctx, connect.NewRequest(&stagedconcertv1.ApproveRequest{
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

func TestAdminStagedConcertHandler_Reject(t *testing.T) {
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
			// @spec components/adapter/admin/api/rpc/staged-concert "Reject records the reviewer"
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

			h := newAdminStagedConcertHandler(t, adminUC)
			resp, err := h.Reject(tt.args.ctx, connect.NewRequest(&stagedconcertv1.RejectRequest{
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

func TestAdminStagedConcertHandler_Boundary(t *testing.T) {
	t.Parallel()

	const stagedID = "019a0000-0000-7000-8000-0000000000a1"

	t.Run("non-admin approves", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/staged-concert "Non-admin approves"
		// The mock fails the test if the usecase runs.
		h := newAdminStagedConcertHandler(t, usecasemocks.NewMockAdminConcertUseCase(t))

		_, err := callThroughAdminPipeline(t, nonAdminCtx(), &stagedconcertv1.ApproveRequest{
			StagedId: &entityv1.StagedConcertId{Value: stagedID},
		}, h.Approve)

		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	})

	t.Run("approve without a resolution", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/staged-concert "Approve without a resolution"
		adminUC := usecasemocks.NewMockAdminConcertUseCase(t)
		startTime := time.Date(2026, 9, 1, 18, 0, 0, 0, time.UTC)
		adminUC.EXPECT().Approve(mock.Anything, stagedID, usecase.ApproveResolutionUnspecified, "admin-sub-123").
			Return(&usecase.ApproveResult{Conflict: &usecase.DuplicateConflict{
				Existing: &usecase.ExistingEventDisplay{
					EventID: "event-1", Title: "Tour 2026", ListedVenueName: "Budokan",
					LocalDate: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), StartTime: &startTime,
				},
				Staged:          &entity.StagedConcert{ID: stagedID, Title: "Tour 2026", ListedVenueName: "Budokan"},
				StagedPerformer: &entity.Artist{ID: "artist-1", Name: "Artist Alpha"},
			}}, nil).Once()
		h := newAdminStagedConcertHandler(t, adminUC)

		resp, err := callThroughAdminPipeline(t, adminCtx(), &stagedconcertv1.ApproveRequest{
			StagedId: &entityv1.StagedConcertId{Value: stagedID},
		}, h.Approve)

		require.NoError(t, err)
		require.NotNil(t, resp.Msg.GetConflict())
		assert.Equal(t, "event-1", resp.Msg.GetConflict().GetExisting().GetEventId().GetValue())
		assert.Equal(t, stagedID, resp.Msg.GetConflict().GetStaged().GetStagedId().GetValue())
	})

	t.Run("reject without a reason", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/admin/api/rpc/staged-concert "Reject without a reason"
		h := newAdminStagedConcertHandler(t, usecasemocks.NewMockAdminConcertUseCase(t))

		_, err := callThroughAdminPipeline(t, adminCtx(), &stagedconcertv1.RejectRequest{
			StagedId: &entityv1.StagedConcertId{Value: stagedID},
		}, h.Reject)

		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
	})
}
