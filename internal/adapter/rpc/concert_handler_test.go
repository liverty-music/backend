package rpc_test

import (
	"context"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	concertv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/concert/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// TODO(add-series-hierarchy task 9.2): assertions in this file target the legacy
// BSR Concert proto (Title / SourceUrl / ArtistId scalar fields). They are
// correct for the transitional mapper bridge in internal/adapter/rpc/mapper/
// concert.go, which sources values from the new entity locations
// (Series.Title, Performers[0].ID, ...) while still emitting the pre-Series
// proto shape. When the new generated types reach BSR and the bridge is
// retired, swap these assertions to:
//
//	resp.Msg.Concerts[i].Series.Title.Value              (instead of .Title)
//	resp.Msg.Concerts[i].Series.SourceUrl.Value          (instead of .SourceUrl)
//	resp.Msg.Concerts[i].Performers                      (instead of .ArtistId)
//
// Add at least one multi-performer assertion (len(Performers) > 1) once the
// new repeated field is available.
func TestConcertHandler_List(t *testing.T) {
	t.Parallel()

	t.Run("returns concerts for a specific artist", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		concertUC := mocks.NewMockConcertUseCase(t)
		userRepo := entitymocks.NewMockUserRepository(t)
		h := rpc.NewConcertHandler(concertUC, userRepo, logger)

		artistID := "artist-123"
		localDate := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)
		concertUC.EXPECT().ListByArtist(mock.Anything, artistID).Return([]*entity.Concert{
			{
				Event: entity.Event{
					ID:        "concert-1",
					VenueID:   "venue-1",
					LocalDate: localDate,
				},
				Series:     &entity.Series{Title: "Summer Tour"},
				Performers: []*entity.Artist{{ID: artistID}},
			},
		}, nil).Once()

		req := connect.NewRequest(&concertv1.ListRequest{
			ArtistId: &entityv1.ArtistId{Value: artistID},
		})

		resp, err := h.List(context.Background(), req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Len(t, resp.Msg.Concerts, 1)
		assert.Equal(t, "concert-1", resp.Msg.Concerts[0].Id.Value)
		assert.Equal(t, artistID, resp.Msg.Concerts[0].ArtistId.Value)
		assert.Equal(t, "venue-1", resp.Msg.Concerts[0].VenueId.Value)
		assert.Equal(t, "Summer Tour", resp.Msg.Concerts[0].Title.Value)
		assert.Equal(t, int32(2025), resp.Msg.Concerts[0].LocalDate.Value.Year)
		assert.Equal(t, int32(6), resp.Msg.Concerts[0].LocalDate.Value.Month)
		assert.Equal(t, int32(15), resp.Msg.Concerts[0].LocalDate.Value.Day)
	})

	t.Run("returns all concerts when artist_id is not specified", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		concertUC := mocks.NewMockConcertUseCase(t)
		userRepo := entitymocks.NewMockUserRepository(t)
		h := rpc.NewConcertHandler(concertUC, userRepo, logger)

		localDate := time.Date(2025, 7, 20, 0, 0, 0, 0, time.UTC)
		concertUC.EXPECT().ListByArtist(mock.Anything, "").Return([]*entity.Concert{
			{
				Event: entity.Event{
					ID:        "concert-2",
					VenueID:   "venue-2",
					LocalDate: localDate,
				},
				Series:     &entity.Series{Title: "World Tour"},
				Performers: []*entity.Artist{{ID: "artist-456"}},
			},
		}, nil).Once()

		req := connect.NewRequest(&concertv1.ListRequest{})

		resp, err := h.List(context.Background(), req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Len(t, resp.Msg.Concerts, 1)
		assert.Equal(t, "concert-2", resp.Msg.Concerts[0].Id.Value)
	})

	t.Run("returns empty slice when no concerts exist", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		concertUC := mocks.NewMockConcertUseCase(t)
		userRepo := entitymocks.NewMockUserRepository(t)
		h := rpc.NewConcertHandler(concertUC, userRepo, logger)

		concertUC.EXPECT().ListByArtist(mock.Anything, "artist-999").Return([]*entity.Concert{}, nil).Once()

		req := connect.NewRequest(&concertv1.ListRequest{
			ArtistId: &entityv1.ArtistId{Value: "artist-999"},
		})

		resp, err := h.List(context.Background(), req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Empty(t, resp.Msg.Concerts)
	})

	t.Run("propagates use case error", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		concertUC := mocks.NewMockConcertUseCase(t)
		userRepo := entitymocks.NewMockUserRepository(t)
		h := rpc.NewConcertHandler(concertUC, userRepo, logger)

		concertUC.EXPECT().ListByArtist(mock.Anything, "artist-123").Return(nil, assert.AnError).Once()

		req := connect.NewRequest(&concertv1.ListRequest{
			ArtistId: &entityv1.ArtistId{Value: "artist-123"},
		})

		resp, err := h.List(context.Background(), req)

		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.ErrorIs(t, err, assert.AnError)
	})
}

func TestConcertHandler_SearchNewConcerts(t *testing.T) {
	t.Parallel()

	t.Run("success_returns_concerts", func(t *testing.T) {
		t.Parallel()

		logger, err := logging.New()
		require.NoError(t, err)

		concertUC := mocks.NewMockConcertUseCase(t)
		userRepo := entitymocks.NewMockUserRepository(t)
		h := rpc.NewConcertHandler(concertUC, userRepo, logger)

		artistID := "artist-123"
		date := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
		venueName := "Tokyo Dome"
		concerts := []*entity.Concert{
			{
				Event: entity.Event{
					ID:              "c1",
					ListedVenueName: &venueName,
					LocalDate:       date,
				},
				Series:     &entity.Series{Title: "Summer Live"},
				Performers: []*entity.Artist{{ID: artistID}},
			},
		}

		concertUC.EXPECT().SearchNewConcerts(mock.Anything, artistID).Return(concerts, nil)

		req := connect.NewRequest(&concertv1.SearchNewConcertsRequest{
			ArtistId: &entityv1.ArtistId{Value: artistID},
		})

		resp, err := h.SearchNewConcerts(context.Background(), req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Len(t, resp.Msg.Concerts, 1)
		assert.Equal(t, "Summer Live", resp.Msg.Concerts[0].Title.GetValue())
	})

	t.Run("success_no_concerts", func(t *testing.T) {
		t.Parallel()

		logger, err := logging.New()
		require.NoError(t, err)

		concertUC := mocks.NewMockConcertUseCase(t)
		userRepo := entitymocks.NewMockUserRepository(t)
		h := rpc.NewConcertHandler(concertUC, userRepo, logger)

		artistID := "artist-123"

		concertUC.EXPECT().SearchNewConcerts(mock.Anything, artistID).Return(nil, nil)

		req := connect.NewRequest(&concertv1.SearchNewConcertsRequest{
			ArtistId: &entityv1.ArtistId{Value: artistID},
		})

		resp, err := h.SearchNewConcerts(context.Background(), req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Empty(t, resp.Msg.Concerts)
	})

	t.Run("failure", func(t *testing.T) {
		t.Parallel()

		logger, err := logging.New()
		require.NoError(t, err)

		concertUC := mocks.NewMockConcertUseCase(t)
		userRepo := entitymocks.NewMockUserRepository(t)
		h := rpc.NewConcertHandler(concertUC, userRepo, logger)

		artistID := "artist-123"

		concertUC.EXPECT().SearchNewConcerts(mock.Anything, artistID).Return(nil, assert.AnError)

		req := connect.NewRequest(&concertv1.SearchNewConcertsRequest{
			ArtistId: &entityv1.ArtistId{Value: artistID},
		})

		resp, err := h.SearchNewConcerts(context.Background(), req)

		assert.Error(t, err)
		assert.Nil(t, resp)
	})
}

func TestConcertHandler_ListByFollower(t *testing.T) {
	t.Parallel()

	internalUserID := "internal-user-uuid-1"

	t.Run("unauthenticated", func(t *testing.T) {
		t.Parallel()

		logger, err := logging.New()
		require.NoError(t, err)

		concertUC := mocks.NewMockConcertUseCase(t)
		userRepo := entitymocks.NewMockUserRepository(t)
		h := rpc.NewConcertHandler(concertUC, userRepo, logger)

		// No auth context → GetExternalUserID returns CodeUnauthenticated.
		req := connect.NewRequest(&concertv1.ListByFollowerRequest{})

		resp, err := h.ListByFollower(context.Background(), req)

		assert.Error(t, err)
		assert.Nil(t, resp)
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		logger, err := logging.New()
		require.NoError(t, err)

		concertUC := mocks.NewMockConcertUseCase(t)
		userRepo := entitymocks.NewMockUserRepository(t)
		h := rpc.NewConcertHandler(concertUC, userRepo, logger)

		ctx := auth.WithClaims(context.Background(), &auth.Claims{Sub: "ext-user-1"})
		user := &entity.User{ID: internalUserID}
		userRepo.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(user, nil).Once()
		concertUC.EXPECT().ListByFollowerGrouped(mock.Anything, internalUserID, user.Home).Return([]*entity.ProximityGroup{}, nil).Once()

		req := connect.NewRequest(&concertv1.ListByFollowerRequest{})

		resp, err := h.ListByFollower(ctx, req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})
}
