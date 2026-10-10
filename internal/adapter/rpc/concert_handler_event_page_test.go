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
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	eventPageOrganizerID = "019a0000-0000-7000-8000-0000000000f1"
	eventPageSeriesID    = "019a0000-0000-7000-8000-0000000000a1"
	eventPageMediaID     = "019a0000-0000-7000-8000-0000000000c1"
)

// firstPartyConcert builds a Concert whose Series is a PUBLISHED PUBLIC
// first-party Series with a cover, as ConcertUseCase.Get returns it.
func firstPartyConcert(id string, date time.Time) *entity.Concert {
	organizerID := eventPageOrganizerID
	description := "Two nights at Shibuya WWW."
	visibility := entity.SeriesVisibilityPublic
	state := entity.SeriesPublishStatePublished
	token := "secret-share-token"
	return &entity.Concert{
		ID: id, SeriesID: eventPageSeriesID, LocalDate: date,
		Venue: &entity.Venue{ID: "019a0000-0000-7000-8000-0000000000d1", Name: "Shibuya WWW"},
		Series: &entity.Series{
			ID:            eventPageSeriesID,
			Title:         "ONE MAN LIVE",
			Type:          entity.SeriesTypeSingle,
			Description:   &description,
			CoverMedia:    &entity.Media{ID: eventPageMediaID, OrganizerID: eventPageOrganizerID, Kind: entity.MediaKindImage},
			OrganizerID:   &organizerID,
			Visibility:    &visibility,
			PublishState:  &state,
			UnlistedToken: &token,
		},
		Artists: []*entity.Artist{{ID: "019a0000-0000-7000-8000-0000000000b1", Name: "The Band"}},
	}
}

func newEventPageHandler(t *testing.T) (*rpc.ConcertHandler, *mocks.MockConcertUseCase, *entitymocks.MockUserRepository) {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	concertUC := mocks.NewMockConcertUseCase(t)
	userRepo := entitymocks.NewMockUserRepository(t)
	h := rpc.NewConcertHandler(concertUC, userRepo, mapper.NewMediaURLBuilder("https://cdn.example.com"), logger)
	return h, concertUC, userRepo
}

func TestConcertHandler_Get(t *testing.T) {
	t.Parallel()

	const eventID = "019a0000-0000-7000-8000-0000000000e1"
	date := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)

	t.Run("return the concert with its first-party series to a guest", func(t *testing.T) {
		// @spec components/adapter/fan/api/rpc/concert "Guest opens an event page"
		t.Parallel()
		h, concertUC, _ := newEventPageHandler(t)
		concertUC.EXPECT().Get(mock.Anything, eventID).Return(firstPartyConcert(eventID, date), nil).Once()

		// No auth claims: the caller is a guest.
		resp, err := h.Get(context.Background(), connect.NewRequest(&concertv1.GetRequest{
			EventId: &entityv1.EventId{Value: eventID},
		}))

		require.NoError(t, err)
		got := resp.Msg.Concert
		require.NotNil(t, got)
		assert.Equal(t, eventID, got.GetEvent().GetId().GetValue())
		assert.Equal(t, eventPageSeriesID, got.GetEvent().GetSeriesId().GetValue())
		require.Len(t, resp.Msg.Series, 1)
		require.Len(t, resp.Msg.Artists, 1)
		s := resp.Msg.Series[0]
		assert.Equal(t, eventPageOrganizerID, s.GetOrganizerId().GetValue())
		assert.Equal(t, "Two nights at Shibuya WWW.", s.GetDescription().GetValue())
		assert.Equal(t, entityv1.Visibility_VISIBILITY_PUBLIC, s.GetVisibility())
		assert.Equal(t, entityv1.PublishState_PUBLISH_STATE_PUBLISHED, s.GetPublishState())
		assert.Equal(t, eventPageMediaID, s.GetMedia().GetId().GetValue())
		assert.Contains(t, s.GetMedia().GetAttributes().GetLarge().GetValue(), "https://cdn.example.com/")
		assert.NotContains(t, s.String(), "secret-share-token", "the share token is never returned")
	})

	t.Run("show the seller of a first-party concert", func(t *testing.T) {
		// @spec components/adapter/fan/api/rpc/concert "Seller shown for a first-party concert"
		t.Parallel()
		h, concertUC, _ := newEventPageHandler(t)
		c := firstPartyConcert(eventID, date)
		c.Series.Organizer = &entity.Organizer{
			ID: eventPageOrganizerID, Name: "Liverty Records",
			SellerDetails: &entity.SellerDetails{
				LegalName: "株式会社リバティ", RepresentativeName: "山田 太郎", Address: "東京都渋谷区1-2-3",
				PhoneNumber: "+81312345678", ContactEmail: "info@example.com",
			},
			PlatformFeeRateBps: 500,
		}
		concertUC.EXPECT().Get(mock.Anything, eventID).Return(c, nil).Once()

		resp, err := h.Get(context.Background(), connect.NewRequest(&concertv1.GetRequest{
			EventId: &entityv1.EventId{Value: eventID},
		}))

		require.NoError(t, err)
		require.Len(t, resp.Msg.Series, 1)
		o := resp.Msg.Series[0].GetOrganizer()
		require.NotNil(t, o)
		assert.Equal(t, eventPageOrganizerID, o.GetId().GetValue())
		assert.Equal(t, "Liverty Records", o.GetName().GetValue())
		assert.Equal(t, "株式会社リバティ", o.GetSellerDetails().GetLegalName())
		assert.Zero(t, o.GetPlatformFeeRateBps(), "the platform fee rate is never returned")
	})

	t.Run("return the usecase error unchanged", func(t *testing.T) {
		t.Parallel()
		h, concertUC, _ := newEventPageHandler(t)
		concertUC.EXPECT().Get(mock.Anything, eventID).Return(nil, apperr.New(codes.NotFound, "event page not found")).Once()

		resp, err := h.Get(context.Background(), connect.NewRequest(&concertv1.GetRequest{
			EventId: &entityv1.EventId{Value: eventID},
		}))

		assert.Nil(t, resp)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestConcertHandler_ListBySeries(t *testing.T) {
	t.Parallel()

	t.Run("return the series' concerts in usecase order to a guest", func(t *testing.T) {
		// @spec components/adapter/fan/api/rpc/concert "Guest lists the dates of a series"
		t.Parallel()
		h, concertUC, _ := newEventPageHandler(t)
		const (
			day1 = "019a0000-0000-7000-8000-0000000000e2"
			day2 = "019a0000-0000-7000-8000-0000000000e3"
		)
		concertUC.EXPECT().ListBySeries(mock.Anything, eventPageSeriesID).Return([]*entity.Concert{
			firstPartyConcert(day1, time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)),
			firstPartyConcert(day2, time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)),
		}, nil).Once()

		resp, err := h.ListBySeries(context.Background(), connect.NewRequest(&concertv1.ListBySeriesRequest{
			SeriesId: &entityv1.SeriesId{Value: eventPageSeriesID},
		}))

		require.NoError(t, err)
		require.Len(t, resp.Msg.Concerts, 2)
		assert.Equal(t, day1, resp.Msg.Concerts[0].GetEvent().GetId().GetValue())
		assert.Equal(t, day2, resp.Msg.Concerts[1].GetEvent().GetId().GetValue())
		require.Len(t, resp.Msg.Series, 1, "the series is returned once for both dates")
		assert.Equal(t, eventPageOrganizerID, resp.Msg.Series[0].GetOrganizerId().GetValue())
	})
}

func TestConcertHandler_ListByFollower_SeriesOrganizer(t *testing.T) {
	t.Parallel()

	const internalUserID = "internal-user-uuid-1"
	date := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)

	discovered := &entity.Concert{
		ID: "019a0000-0000-7000-8000-0000000000e9", SeriesID: "019a0000-0000-7000-8000-0000000000a9", LocalDate: date,
		Venue:   &entity.Venue{ID: "019a0000-0000-7000-8000-0000000000d9", Name: "Zepp"},
		Series:  &entity.Series{ID: "019a0000-0000-7000-8000-0000000000a9", Title: "Discovered Tour", Type: entity.SeriesTypeTour},
		Artists: []*entity.Artist{{ID: "019a0000-0000-7000-8000-0000000000b9", Name: "Other Band"}},
	}

	tests := []struct {
		name          string
		concert       *entity.Concert
		wantOrganizer string
	}{
		{
			// @spec components/adapter/fan/api/rpc/concert "First-party concert in a list"
			name:          "carry the organizer id of a first-party series",
			concert:       firstPartyConcert("019a0000-0000-7000-8000-0000000000e1", date),
			wantOrganizer: eventPageOrganizerID,
		},
		{
			// @spec components/adapter/fan/api/rpc/concert "Discovered concert in a list"
			name:          "carry no organizer id for a discovered series",
			concert:       discovered,
			wantOrganizer: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h, concertUC, userRepo := newEventPageHandler(t)
			ctx := auth.WithClaims(context.Background(), &auth.Claims{Sub: "ext-user-1"})
			user := &entity.User{ID: internalUserID}
			userRepo.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(user, nil).Once()
			concertUC.EXPECT().ListByFollowerGrouped(mock.Anything, internalUserID, user.Home, mock.Anything).
				Return([]*entity.ProximityGroup{{Date: date, Away: []*entity.Concert{tt.concert}}}, nil).Once()

			resp, err := h.ListByFollower(ctx, connect.NewRequest(&concertv1.ListByFollowerRequest{}))

			require.NoError(t, err)
			require.Len(t, resp.Msg.Groups, 1)
			require.Len(t, resp.Msg.Groups[0].Away, 1)
			require.Len(t, resp.Msg.Series, 1)
			series := resp.Msg.Series[0]
			assert.Equal(t, resp.Msg.Groups[0].Away[0].GetEvent().GetSeriesId().GetValue(), series.GetId().GetValue())
			assert.Equal(t, tt.wantOrganizer, series.GetOrganizerId().GetValue())
			assert.Equal(t, tt.wantOrganizer != "", series.GetOrganizerId() != nil)
		})
	}
}

func TestConcertHandler_ListByFollower_TourInOneList(t *testing.T) {
	// @spec components/adapter/fan/api/rpc/concert "Tour in one list"
	t.Parallel()
	h, concertUC, userRepo := newEventPageHandler(t)
	ctx := auth.WithClaims(context.Background(), &auth.Claims{Sub: "ext-user-1"})
	user := &entity.User{ID: "internal-user-uuid-1"}
	userRepo.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(user, nil).Once()
	day := func(d int) time.Time { return time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC) }
	c1 := firstPartyConcert("019a0000-0000-7000-8000-0000000000e1", day(20))
	c2 := firstPartyConcert("019a0000-0000-7000-8000-0000000000e2", day(21))
	c3 := firstPartyConcert("019a0000-0000-7000-8000-0000000000e3", day(22))
	concertUC.EXPECT().ListByFollowerGrouped(mock.Anything, user.ID, user.Home, mock.Anything).
		Return([]*entity.ProximityGroup{
			{Date: day(20), Home: []*entity.Concert{c1}},
			{Date: day(21), Nearby: []*entity.Concert{c2}},
			{Date: day(22), Away: []*entity.Concert{c3}},
		}, nil).Once()

	resp, err := h.ListByFollower(ctx, connect.NewRequest(&concertv1.ListByFollowerRequest{}))

	require.NoError(t, err)
	require.Len(t, resp.Msg.Groups, 3)
	require.Len(t, resp.Msg.Series, 1, "the tour's series is returned once")
	assert.Equal(t, eventPageSeriesID, resp.Msg.Series[0].GetId().GetValue())
	require.Len(t, resp.Msg.Artists, 1, "the tour's artist is returned once")
	assert.Equal(t, "019a0000-0000-7000-8000-0000000000b1", resp.Msg.Artists[0].GetId().GetValue())
}

func TestConcertHandler_ListByArtists_CoverImage(t *testing.T) {
	// @spec components/adapter/fan/api/rpc/concert "Cover image in a list"
	t.Parallel()
	h, concertUC, _ := newEventPageHandler(t)
	date := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	concertUC.EXPECT().ListByArtists(mock.Anything, []string{"019a0000-0000-7000-8000-0000000000b1"}, mock.Anything).
		Return([]*entity.ProximityGroup{
			{Date: date, Home: []*entity.Concert{firstPartyConcert("019a0000-0000-7000-8000-0000000000e1", date)}},
		}, nil).Once()

	resp, err := h.ListByArtists(context.Background(), connect.NewRequest(&concertv1.ListByArtistsRequest{
		ArtistIds: []*entityv1.ArtistId{{Value: "019a0000-0000-7000-8000-0000000000b1"}},
		Home:      &entityv1.Home{CountryCode: "JP", Level_1: "JP-13"},
	}))

	require.NoError(t, err)
	require.Len(t, resp.Msg.Series, 1)
	media := resp.Msg.Series[0].GetMedia()
	assert.Equal(t, eventPageMediaID, media.GetId().GetValue())
	assert.Contains(t, media.GetAttributes().GetLarge().GetValue(), "https://cdn.example.com/")
	assert.NotContains(t, resp.Msg.Series[0].String(), "secret-share-token", "the share token is never returned")
}
