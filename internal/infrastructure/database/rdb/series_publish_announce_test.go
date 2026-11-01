package rdb_test

import (
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

// concertsAnnouncedFor returns the CONCERT.created announcements recorded
// after mark for the Artist (its ce_subject), each as the event ids it
// carries.
func concertsAnnouncedFor(t *testing.T, mark int64, artistID string) [][]string {
	t.Helper()
	lists := announcedAbout(t, mark, entity.SubjectConcertCreated, artistID, func() *entityv1.ConcertList { return &entityv1.ConcertList{} })
	out := make([][]string, 0, len(lists))
	for _, l := range lists {
		var ids []string
		for _, c := range l.GetConcerts() {
			ids = append(ids, c.GetEvent().GetId().GetValue())
		}
		out = append(out, ids)
	}
	return out
}

func TestSeriesRepository_PublishEventsAnnounces(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}

	t.Run("two performers", func(t *testing.T) {
		// @spec components/entity/series/publish-events "Two performers"
		f := newAuthoredFixture(t)
		seriesID, ids := f.series(entity.PublishStateDraft)
		second := seedArtist(t, "Second Band", uuid.NewV4().String())
		f.exec(`INSERT INTO concert_artists (event_id, artist_id) VALUES ($1, $2)`, ids[0], second)
		mark := outboxMark(t)

		_, err := f.repo.PublishEvents(f.ctx, seriesID, ids)

		require.NoError(t, err)
		assert.Equal(t, [][]string{{ids[0]}}, concertsAnnouncedFor(t, mark, f.artistID))
		assert.Equal(t, [][]string{{ids[0]}}, concertsAnnouncedFor(t, mark, second))
	})

	t.Run("additional date announced alone", func(t *testing.T) {
		// @spec components/entity/series/publish-events "Additional date announced alone"
		f := newAuthoredFixture(t)
		seriesID, ids := f.series(entity.PublishStatePublished, entity.PublishStatePublished, entity.PublishStatePublished, entity.PublishStateDraft)
		mark := outboxMark(t)

		_, err := f.repo.PublishEvents(f.ctx, seriesID, ids[3:])

		require.NoError(t, err)
		assert.Equal(t, [][]string{{ids[3]}}, concertsAnnouncedFor(t, mark, f.artistID))
	})

	t.Run("only claimed events", func(t *testing.T) {
		// @spec components/entity/series/publish-events "Only claimed events"
		f := newAuthoredFixture(t)
		seriesID, ids := f.series(entity.PublishStateDraft)
		f.discovered(authoredFirstDate, nil)
		mark := outboxMark(t)

		_, err := f.repo.PublishEvents(f.ctx, seriesID, ids)

		require.NoError(t, err)
		assert.Empty(t, concertsAnnouncedFor(t, mark, f.artistID))
	})

	t.Run("unlisted series", func(t *testing.T) {
		// @spec components/entity/series/publish-events "Unlisted series"
		f := newAuthoredFixture(t)
		seriesID, ids := f.series(entity.PublishStateDraft)
		f.exec(`UPDATE series SET visibility = 'UNLISTED' WHERE id = $1`, seriesID)
		mark := outboxMark(t)

		_, err := f.repo.PublishEvents(f.ctx, seriesID, ids)

		require.NoError(t, err)
		assert.Empty(t, concertsAnnouncedFor(t, mark, f.artistID))
	})

	t.Run("announcement cannot be recorded", func(t *testing.T) {
		// @spec components/entity/series/publish-events "Announcement cannot be recorded"
		f := newAuthoredFixture(t)
		seriesID, ids := f.series(entity.PublishStateDraft)

		_, err := rdb.NewSeriesRepository(testDB, failingOutbox{}).PublishEvents(f.ctx, seriesID, ids)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		state, _ := f.state(ids[0])
		assert.Equal(t, "DRAFT", state)
	})
}
