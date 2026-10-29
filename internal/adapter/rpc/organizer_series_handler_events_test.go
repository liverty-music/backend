package rpc_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	organizerseriesv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/series/v1"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/type/date"
)

// TestOrganizerSeriesHandler_PerEventFields checks the fields
// restructure-event-publishing added to the organizer SeriesService: the event
// id of an edited date, the event ids of Publish and Cancel, and each date's
// own publish state on the Concerts returned.
func TestOrganizerSeriesHandler_PerEventFields(t *testing.T) {
	t.Parallel()

	expectCaller := func(d *organizerSeriesHandlerDeps) {
		d.organizerUC.EXPECT().ResolveCaller(mock.Anything, testProviderOrganizerID).Return(activeOrganizer(), nil).Once()
	}
	inState := func(e *entity.Event, state entity.PublishState) *entity.Event {
		e.PublishState = &state
		return e
	}

	t.Run("pass the event id of an edited date and none for a new date to Update", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerSeriesHandlerDeps(t)
		expectCaller(d)
		var inputs []*usecase.DraftEventInput
		d.seriesUC.EXPECT().Update(mock.Anything, testOrganizerID, "series-1", mock.Anything, mock.Anything, []string{"artist-1"}).
			Run(func(_ context.Context, _, _ string, _ *entity.Series, in []*usecase.DraftEventInput, _ []string) {
				inputs = in
			}).
			Return(&entity.Series{ID: "series-1", Type: entity.SeriesTypeTour}, nil, nil).Once()

		_, err := d.handler.Update(orgCtx(testProviderOrganizerID), &organizerseriesv1.UpdateRequest{
			SeriesId: &entityv1.SeriesId{Value: "series-1"},
			Draft: &organizerseriesv1.SeriesDraft{
				Title:      &entityv1.Title{Value: "TOUR"},
				Type:       entityv1.SeriesType_SERIES_TYPE_TOUR,
				Visibility: entityv1.Visibility_VISIBILITY_PUBLIC,
				ArtistIds:  []*entityv1.ArtistId{{Value: "artist-1"}},
				Events: []*organizerseriesv1.EventDraft{
					{EventId: &entityv1.EventId{Value: "event-1"}, VenueName: &entityv1.VenueName{Value: "WWW"}, PlaceId: &entityv1.PlaceId{Value: "ChIJwww"}, LocalDate: &entityv1.LocalDate{Value: &date.Date{Year: 2026, Month: 12, Day: 1}}},
					{VenueName: &entityv1.VenueName{Value: "WWW"}, PlaceId: &entityv1.PlaceId{Value: "ChIJwww"}, LocalDate: &entityv1.LocalDate{Value: &date.Date{Year: 2026, Month: 12, Day: 8}}},
				},
			},
		})

		require.NoError(t, err)
		require.Len(t, inputs, 2)
		require.NotNil(t, inputs[0].EventID)
		assert.Equal(t, "event-1", *inputs[0].EventID)
		assert.Nil(t, inputs[1].EventID)
	})

	t.Run("pass each date's per-account limit to Update and return it", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerSeriesHandlerDeps(t)
		expectCaller(d)
		var inputs []*usecase.DraftEventInput
		six := 6
		returned := authoredEvent("event-1", "series-1", 1)
		returned.PerAccountLimit = &six
		d.seriesUC.EXPECT().Update(mock.Anything, testOrganizerID, "series-1", mock.Anything, mock.Anything, []string{"artist-1"}).
			Run(func(_ context.Context, _, _ string, _ *entity.Series, in []*usecase.DraftEventInput, _ []string) {
				inputs = in
			}).
			Return(&entity.Series{ID: "series-1", Type: entity.SeriesTypeTour}, concertsOf([]*entity.Event{returned}, []*entity.Artist{{ID: "artist-1"}}), nil).Once()
		limit := int32(6)

		resp, err := d.handler.Update(orgCtx(testProviderOrganizerID), &organizerseriesv1.UpdateRequest{
			SeriesId: &entityv1.SeriesId{Value: "series-1"},
			Draft: &organizerseriesv1.SeriesDraft{
				Title:      &entityv1.Title{Value: "TOUR"},
				Type:       entityv1.SeriesType_SERIES_TYPE_TOUR,
				Visibility: entityv1.Visibility_VISIBILITY_PUBLIC,
				ArtistIds:  []*entityv1.ArtistId{{Value: "artist-1"}},
				Events: []*organizerseriesv1.EventDraft{
					{EventId: &entityv1.EventId{Value: "event-1"}, VenueName: &entityv1.VenueName{Value: "WWW"}, PlaceId: &entityv1.PlaceId{Value: "ChIJwww"}, LocalDate: &entityv1.LocalDate{Value: &date.Date{Year: 2026, Month: 12, Day: 1}}, PerAccountLimit: &limit},
					{VenueName: &entityv1.VenueName{Value: "WWW"}, PlaceId: &entityv1.PlaceId{Value: "ChIJwww"}, LocalDate: &entityv1.LocalDate{Value: &date.Date{Year: 2026, Month: 12, Day: 8}}},
				},
			},
		})

		require.NoError(t, err)
		require.Len(t, inputs, 2)
		require.NotNil(t, inputs[0].PerAccountLimit)
		assert.Equal(t, 6, *inputs[0].PerAccountLimit)
		assert.Nil(t, inputs[1].PerAccountLimit, "a date without a limit takes the default")
		require.Len(t, resp.GetConcerts(), 1)
		assert.Equal(t, int32(6), resp.GetConcerts()[0].GetEvent().GetPerAccountLimit())
	})

	t.Run("pass the named event ids to Publish and return each date's own state", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerSeriesHandlerDeps(t)
		expectCaller(d)
		published := entity.PublishStatePublished
		series := &entity.Series{ID: "series-1", Title: "TOUR", Type: entity.SeriesTypeTour, PublishState: &published}
		events := []*entity.Event{
			inState(authoredEvent("event-1", "series-1", 1), entity.PublishStatePublished),
			inState(authoredEvent("event-2", "series-1", 2), entity.PublishStateDraft),
		}
		d.seriesUC.EXPECT().Publish(mock.Anything, testOrganizerID, "series-1", []string{"event-1"}).
			Return(series, concertsOf(events, []*entity.Artist{{ID: "artist-1"}}), nil).Once()

		resp, err := d.handler.Publish(orgCtx(testProviderOrganizerID), &organizerseriesv1.PublishRequest{
			SeriesId: &entityv1.SeriesId{Value: "series-1"},
			EventIds: []*entityv1.EventId{{Value: "event-1"}},
		})

		require.NoError(t, err)
		assert.Equal(t, entityv1.PublishState_PUBLISH_STATE_PUBLISHED, resp.GetSeries().GetPublishState())
		require.Len(t, resp.GetConcerts(), 2)
		assert.Equal(t, entityv1.PublishState_PUBLISH_STATE_PUBLISHED, resp.GetConcerts()[0].GetEvent().GetPublishState())
		assert.Equal(t, entityv1.PublishState_PUBLISH_STATE_DRAFT, resp.GetConcerts()[1].GetEvent().GetPublishState())
	})

	t.Run("pass no event id to Publish when none is named", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerSeriesHandlerDeps(t)
		expectCaller(d)
		d.seriesUC.EXPECT().Publish(mock.Anything, testOrganizerID, "series-1", []string(nil)).
			Return(&entity.Series{ID: "series-1", Type: entity.SeriesTypeTour}, nil, nil).Once()

		_, err := d.handler.Publish(orgCtx(testProviderOrganizerID), &organizerseriesv1.PublishRequest{
			SeriesId: &entityv1.SeriesId{Value: "series-1"},
		})

		require.NoError(t, err)
	})

	t.Run("pass the named event ids to Cancel", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerSeriesHandlerDeps(t)
		expectCaller(d)
		d.seriesUC.EXPECT().Cancel(mock.Anything, testOrganizerID, "series-1", []string{"event-2", "event-3"}).Return(nil).Once()

		_, err := d.handler.Cancel(orgCtx(testProviderOrganizerID), &organizerseriesv1.CancelRequest{
			SeriesId: &entityv1.SeriesId{Value: "series-1"},
			EventIds: []*entityv1.EventId{{Value: "event-2"}, {Value: "event-3"}},
		})

		require.NoError(t, err)
	})

	t.Run("return each date with its own performers", func(t *testing.T) {
		t.Parallel()
		d := newOrganizerSeriesHandlerDeps(t)
		expectCaller(d)
		concerts := []*entity.Concert{
			{Event: *inState(authoredEvent("event-1", "series-1", 1), entity.PublishStatePublished), Artists: []*entity.Artist{{ID: "artist-1"}, {ID: "guest-1"}}},
			{Event: *inState(authoredEvent("event-2", "series-1", 2), entity.PublishStateCancelled), Artists: []*entity.Artist{{ID: "artist-1"}}},
		}
		d.seriesUC.EXPECT().Publish(mock.Anything, testOrganizerID, "series-1", []string(nil)).
			Return(&entity.Series{ID: "series-1", Type: entity.SeriesTypeTour}, concerts, nil).Once()

		resp, err := d.handler.Publish(orgCtx(testProviderOrganizerID), &organizerseriesv1.PublishRequest{
			SeriesId: &entityv1.SeriesId{Value: "series-1"},
		})

		require.NoError(t, err)
		require.Len(t, resp.GetConcerts(), 2)
		assert.Len(t, resp.GetConcerts()[0].GetArtistIds(), 2)
		assert.Len(t, resp.GetConcerts()[1].GetArtistIds(), 1)
		assert.Len(t, resp.GetArtists(), 2, "each artist once")
	})

	t.Run("set a fan concert's publish state on its event", func(t *testing.T) {
		t.Parallel()
		cancelled := entity.PublishStateCancelled
		c := &entity.Concert{ID: "event-1", SeriesID: "series-1", PublishState: &cancelled, Series: &entity.Series{ID: "series-1"}}

		got := mapper.ConcertToProto(c)

		assert.Equal(t, entityv1.PublishState_PUBLISH_STATE_CANCELLED, got.GetEvent().GetPublishState())
	})
}
