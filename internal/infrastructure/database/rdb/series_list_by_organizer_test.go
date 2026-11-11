package rdb_test

import (
	"testing"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
)

func TestSeriesRepository_ListByOrganizer(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)

	// @spec components/entity/series/list-by-organizer "Mixed states"
	t.Run("returns the Organizer's draft, published and cancelled series, newest first", func(t *testing.T) {
		f := newAuthoredFixture(t, db)
		// Each series at a venue of its own: published events on the same
		// slot would collide.
		seriesAt := func(f *authoredFixture, state entity.PublishState) string {
			f.venueID = seedVenue(t, db, "Hall "+entity.NewID())
			id, _ := f.series(state)
			return id
		}
		draft := seriesAt(f, entity.PublishStateDraft)
		published := seriesAt(f, entity.PublishStatePublished)
		cancelled := seriesAt(f, entity.PublishStateCancelled)
		other := *f
		other.orgID = seedOrganizer(t, db)
		seriesAt(&other, entity.PublishStatePublished)

		got, err := f.repo.ListByOrganizer(f.ctx, f.orgID)

		require.NoError(t, err)
		ids := make([]string, len(got))
		states := make([]entity.PublishState, len(got))
		for i, s := range got {
			ids[i] = s.ID
			require.NotNil(t, s.PublishState)
			states[i] = *s.PublishState
		}
		assert.Equal(t, []string{cancelled, published, draft}, ids, "only the Organizer's series, the most recently created first")
		assert.Equal(t, []entity.PublishState{entity.PublishStateCancelled, entity.PublishStatePublished, entity.PublishStateDraft}, states)
	})

	// @spec components/entity/series/list-by-organizer "Missing organizer"
	t.Run("fails with InvalidArgument when no Organizer id is given", func(t *testing.T) {
		f := newAuthoredFixture(t, db)

		got, err := f.repo.ListByOrganizer(f.ctx, "")

		require.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Nil(t, got)
	})
}
