package usecase_test

import (
	"context"
	"testing"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
)

// authoredSeries returns a Series of the Organizer in the state, as
// Series.ListByOrganizer returns it.
func authoredSeries(id string, state entity.PublishState) *entity.Series {
	return &entity.Series{ID: id, Title: "Tour " + id, Type: entity.SeriesTypeSingle, PublishState: &state}
}

func TestSeriesUseCase_List(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// @spec components/usecase/series/list "Drafts included"
	t.Run("a draft and a published series are returned, each with its performances", func(t *testing.T) {
		t.Parallel()
		d := newAuthoringDeps(t)
		draft := authoredSeries("series-draft", entity.PublishStateDraft)
		published := authoredSeries("series-published", entity.PublishStatePublished)
		draftConcerts := concertsOf([]*entity.Event{{ID: "event-draft", SeriesID: draft.ID}}, nil)
		publishedConcerts := concertsOf([]*entity.Event{
			{ID: "event-published-1", SeriesID: published.ID},
			{ID: "event-published-2", SeriesID: published.ID},
		}, []*entity.Artist{{ID: "artist-1"}})
		// Newest first, as Series.ListByOrganizer orders them.
		d.seriesRepo.EXPECT().ListByOrganizer(ctx, "org-1").Return([]*entity.Series{published, draft}, nil).Once()
		d.seriesRepo.EXPECT().GetAuthored(ctx, published.ID).Return(published, publishedConcerts, nil).Once()
		d.seriesRepo.EXPECT().GetAuthored(ctx, draft.ID).Return(draft, draftConcerts, nil).Once()

		series, concerts, err := d.uc.List(ctx, "org-1")

		require.NoError(t, err)
		assert.Equal(t, []*entity.Series{published, draft}, series)
		assert.Equal(t, append(append([]*entity.Concert{}, publishedConcerts...), draftConcerts...), concerts)
	})

	// @spec components/usecase/series/list "Cancelled included"
	t.Run("a cancelled series is returned", func(t *testing.T) {
		t.Parallel()
		d := newAuthoringDeps(t)
		cancelled := authoredSeries("series-cancelled", entity.PublishStateCancelled)
		cancelledConcerts := concertsOf([]*entity.Event{{ID: "event-cancelled", SeriesID: cancelled.ID}}, nil)
		d.seriesRepo.EXPECT().ListByOrganizer(ctx, "org-1").Return([]*entity.Series{cancelled}, nil).Once()
		d.seriesRepo.EXPECT().GetAuthored(ctx, cancelled.ID).Return(cancelled, cancelledConcerts, nil).Once()

		series, concerts, err := d.uc.List(ctx, "org-1")

		require.NoError(t, err)
		assert.Equal(t, []*entity.Series{cancelled}, series)
		assert.Equal(t, cancelledConcerts, concerts)
	})

	// @spec components/usecase/series/list "Other Organizers excluded"
	t.Run("only the caller's Organizer's series are read", func(t *testing.T) {
		t.Parallel()
		d := newAuthoringDeps(t)
		own := authoredSeries("series-own", entity.PublishStatePublished)
		// The mock fails the test on a read for any other Organizer or
		// Series: the only reads are the caller's Organizer's list and the
		// Series it returned.
		d.seriesRepo.EXPECT().ListByOrganizer(ctx, "org-1").Return([]*entity.Series{own}, nil).Once()
		d.seriesRepo.EXPECT().GetAuthored(ctx, own.ID).Return(own, nil, nil).Once()

		series, concerts, err := d.uc.List(ctx, "org-1")

		require.NoError(t, err)
		assert.Equal(t, []*entity.Series{own}, series)
		assert.Empty(t, concerts)
	})

	// @spec components/usecase/series/list "Missing Organizer"
	//
	// Series.ListByOrganizer refuses the missing id (its contract test,
	// TestSeriesRepository_ListByOrganizer); this checks that List reads
	// nothing else and returns that refusal as it is.
	t.Run("no Organizer fails with InvalidArgument", func(t *testing.T) {
		t.Parallel()
		d := newAuthoringDeps(t)
		d.seriesRepo.EXPECT().ListByOrganizer(ctx, "").Return(nil, apperr.New(codes.InvalidArgument, "organizer ID must not be empty")).Once()

		series, concerts, err := d.uc.List(ctx, "")

		require.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Nil(t, series)
		assert.Nil(t, concerts)
	})

	t.Run("a series that cannot be read fails the call", func(t *testing.T) {
		t.Parallel()
		d := newAuthoringDeps(t)
		first := authoredSeries("series-1", entity.PublishStatePublished)
		second := authoredSeries("series-2", entity.PublishStateDraft)
		d.seriesRepo.EXPECT().ListByOrganizer(ctx, "org-1").Return([]*entity.Series{first, second}, nil).Once()
		d.seriesRepo.EXPECT().GetAuthored(ctx, first.ID).Return(first, nil, nil).Once()
		d.seriesRepo.EXPECT().GetAuthored(ctx, second.ID).Return(nil, nil, apperr.New(codes.Unavailable, "db down")).Once()

		series, concerts, err := d.uc.List(ctx, "org-1")

		require.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Nil(t, series)
		assert.Nil(t, concerts)
	})
}
