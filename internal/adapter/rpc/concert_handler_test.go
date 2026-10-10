package rpc_test

import (
	"context"
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	concertv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/concert/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/date"
)

func TestConcertHandler_List(t *testing.T) {
	t.Parallel()

	t.Run("returns concerts for a specific artist", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		concertUC := mocks.NewMockConcertUseCase(t)
		userRepo := entitymocks.NewMockUserRepository(t)
		h := rpc.NewConcertHandler(concertUC, userRepo, mapper.NewMediaURLBuilder(""), logger)

		artistID := "artist-123"
		supportID := "artist-456"
		localDate := time.Date(2025, 6, 15, 0, 0, 0, 0, time.UTC)
		concertUC.EXPECT().ListByArtist(mock.Anything, artistID).Return([]*entity.Concert{
			{
				ID: "concert-1", SeriesID: "series-1", VenueID: "venue-1", LocalDate: localDate,
				Series: &entity.Series{ID: "series-1", Title: "Summer Tour", Type: entity.SeriesTypeTour, SourceURL: "https://example.com/tour"},
				Artists: []*entity.Artist{
					{ID: artistID, Name: "Headliner", MBID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"},
					{ID: supportID, Name: "Support", MBID: "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"},
				},
			},
		}, nil).Once()

		req := connect.NewRequest(&concertv1.ListRequest{
			ArtistId: &entityv1.ArtistId{Value: artistID},
		})

		resp, err := h.List(context.Background(), req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Len(t, resp.Msg.Concerts, 1)
		concert := resp.Msg.Concerts[0]
		assert.Equal(t, "concert-1", concert.GetEvent().GetId().GetValue())
		assert.Equal(t, "series-1", concert.GetEvent().GetSeriesId().GetValue())
		assert.Equal(t, int32(2025), concert.GetEvent().GetLocalDate().GetValue().GetYear())
		assert.Equal(t, int32(6), concert.GetEvent().GetLocalDate().GetValue().GetMonth())
		assert.Equal(t, int32(15), concert.GetEvent().GetLocalDate().GetValue().GetDay())
		// Title / source URL come with the Series in the side list, the
		// performers as ids on the Concert and Artists in the side list.
		require.Len(t, resp.Msg.Series, 1)
		assert.Equal(t, "Summer Tour", resp.Msg.Series[0].GetTitle().GetValue())
		assert.Equal(t, "https://example.com/tour", resp.Msg.Series[0].GetSourceUrl().GetValue())
		require.Len(t, concert.GetArtistIds(), 2, "multi-performer concert must round-trip both performers")
		assert.Equal(t, artistID, concert.GetArtistIds()[0].GetValue())
		assert.Equal(t, supportID, concert.GetArtistIds()[1].GetValue())
		require.Len(t, resp.Msg.Artists, 2)
	})

	t.Run("returns all concerts when artist_id is not specified", func(t *testing.T) {
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		concertUC := mocks.NewMockConcertUseCase(t)
		userRepo := entitymocks.NewMockUserRepository(t)
		h := rpc.NewConcertHandler(concertUC, userRepo, mapper.NewMediaURLBuilder(""), logger)

		localDate := time.Date(2025, 7, 20, 0, 0, 0, 0, time.UTC)
		concertUC.EXPECT().ListByArtist(mock.Anything, "").Return([]*entity.Concert{
			{
				ID:        "concert-2",
				VenueID:   "venue-2",
				LocalDate: localDate,
				Series:    &entity.Series{Title: "World Tour"},
				Artists:   []*entity.Artist{{ID: "artist-456"}},
			},
		}, nil).Once()

		req := connect.NewRequest(&concertv1.ListRequest{})

		resp, err := h.List(context.Background(), req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
		assert.Len(t, resp.Msg.Concerts, 1)
		assert.Equal(t, "concert-2", resp.Msg.Concerts[0].GetEvent().GetId().GetValue())
	})

	t.Run("returns empty slice when no concerts exist", func(t *testing.T) {
		// @spec components/adapter/fan/api/rpc/concert "Artist without concerts"
		t.Parallel()
		logger, err := logging.New()
		require.NoError(t, err)
		concertUC := mocks.NewMockConcertUseCase(t)
		userRepo := entitymocks.NewMockUserRepository(t)
		h := rpc.NewConcertHandler(concertUC, userRepo, mapper.NewMediaURLBuilder(""), logger)

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
		h := rpc.NewConcertHandler(concertUC, userRepo, mapper.NewMediaURLBuilder(""), logger)

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

func TestConcertHandler_ListByFollower(t *testing.T) {
	t.Parallel()

	internalUserID := "internal-user-uuid-1"

	t.Run("unauthenticated", func(t *testing.T) {
		t.Parallel()

		logger, err := logging.New()
		require.NoError(t, err)

		concertUC := mocks.NewMockConcertUseCase(t)
		userRepo := entitymocks.NewMockUserRepository(t)
		h := rpc.NewConcertHandler(concertUC, userRepo, mapper.NewMediaURLBuilder(""), logger)

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
		h := rpc.NewConcertHandler(concertUC, userRepo, mapper.NewMediaURLBuilder(""), logger)

		ctx := auth.WithClaims(context.Background(), &auth.Claims{Sub: "ext-user-1"})
		user := &entity.User{ID: internalUserID}
		userRepo.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(user, nil).Once()
		concertUC.EXPECT().ListByFollowerGrouped(mock.Anything, internalUserID, user.Home, mock.Anything).Return([]*entity.ProximityGroup{}, nil).Once()

		req := connect.NewRequest(&concertv1.ListByFollowerRequest{})

		resp, err := h.ListByFollower(ctx, req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})

	t.Run("threads the request from date to the use case", func(t *testing.T) {
		t.Parallel()

		logger, err := logging.New()
		require.NoError(t, err)

		concertUC := mocks.NewMockConcertUseCase(t)
		userRepo := entitymocks.NewMockUserRepository(t)
		h := rpc.NewConcertHandler(concertUC, userRepo, mapper.NewMediaURLBuilder(""), logger)

		ctx := auth.WithClaims(context.Background(), &auth.Claims{Sub: "ext-user-1"})
		user := &entity.User{ID: internalUserID}
		userRepo.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(user, nil).Once()

		// A client-supplied from must reach the use case mapped to a UTC-midnight
		// *time.Time; the getter chain is nil-safe when the field is omitted.
		wantFrom := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		concertUC.EXPECT().
			ListByFollowerGrouped(mock.Anything, internalUserID, user.Home,
				mock.MatchedBy(func(from *time.Time) bool {
					return from != nil && from.Equal(wantFrom)
				})).
			Return([]*entity.ProximityGroup{}, nil).Once()

		req := connect.NewRequest(&concertv1.ListByFollowerRequest{
			From: &entityv1.LocalDate{Value: &date.Date{Year: 2020, Month: 1, Day: 1}},
		})

		resp, err := h.ListByFollower(ctx, req)

		assert.NoError(t, err)
		assert.NotNil(t, resp)
	})
}
