package rpc_test

import (
	"context"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	adminv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
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

func newModerationHandler(
	t *testing.T,
	approvalUC *usecasemocks.MockConcertApprovalUseCase,
) *handler.ConcertModerationHandler {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	return handler.NewConcertModerationHandler(approvalUC, logger)
}

// ---------- ListPendingConcerts ----------

func TestConcertModerationHandler_ListPendingConcerts(t *testing.T) {
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
		approvalUC func(*usecasemocks.MockConcertApprovalUseCase)
	}

	tests := []struct {
		name     string
		args     args
		dep      dep
		wantErr  bool
		wantCode connect.Code
		check    func(t *testing.T, resp *connect.Response[adminv1.ListPendingConcertsResponse])
	}{
		{
			name: "return pending concerts with resolved venue",
			args: args{ctx: adminCtx()},
			dep: dep{
				approvalUC: func(m *usecasemocks.MockConcertApprovalUseCase) {
					m.EXPECT().ListPending(mock.Anything).Return([]*usecase.PendingConcertReview{
						{Staged: stagedWithVenue, Performer: artistA},
					}, nil).Once()
				},
			},
			check: func(t *testing.T, resp *connect.Response[adminv1.ListPendingConcertsResponse]) {
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
				approvalUC: func(m *usecasemocks.MockConcertApprovalUseCase) {
					m.EXPECT().ListPending(mock.Anything).Return([]*usecase.PendingConcertReview{
						{Staged: stagedNoVenue, Performer: artistB},
					}, nil).Once()
				},
			},
			check: func(t *testing.T, resp *connect.Response[adminv1.ListPendingConcertsResponse]) {
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

			approvalUC := usecasemocks.NewMockConcertApprovalUseCase(t)
			tt.dep.approvalUC(approvalUC)

			h := newModerationHandler(t, approvalUC)
			resp, err := h.ListPendingConcerts(tt.args.ctx, connect.NewRequest(&adminv1.ListPendingConcertsRequest{}))

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

// ---------- ApproveConcert ----------

func TestConcertModerationHandler_ApproveConcert(t *testing.T) {
	t.Parallel()

	type args struct {
		ctx      context.Context
		stagedID string
	}
	type dep struct {
		approvalUC func(*usecasemocks.MockConcertApprovalUseCase)
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
				approvalUC: func(m *usecasemocks.MockConcertApprovalUseCase) {
					m.EXPECT().Approve(mock.Anything, "staged-abc").Return(nil).Once()
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			approvalUC := usecasemocks.NewMockConcertApprovalUseCase(t)
			tt.dep.approvalUC(approvalUC)

			h := newModerationHandler(t, approvalUC)
			resp, err := h.ApproveConcert(tt.args.ctx, connect.NewRequest(&adminv1.ApproveConcertRequest{
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

// ---------- RejectConcert ----------

func TestConcertModerationHandler_RejectConcert(t *testing.T) {
	t.Parallel()

	type args struct {
		ctx      context.Context
		stagedID string
		reason   string
	}
	type dep struct {
		approvalUC func(*usecasemocks.MockConcertApprovalUseCase)
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
				approvalUC: func(m *usecasemocks.MockConcertApprovalUseCase) {
					// reviewer sub must match the sub in adminCtx.
					m.EXPECT().Reject(mock.Anything, "staged-xyz", "wrong date", "admin-sub-123").Return(nil).Once()
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			approvalUC := usecasemocks.NewMockConcertApprovalUseCase(t)
			tt.dep.approvalUC(approvalUC)

			h := newModerationHandler(t, approvalUC)
			resp, err := h.RejectConcert(tt.args.ctx, connect.NewRequest(&adminv1.RejectConcertRequest{
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
