package rdb_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeriesRepository_ListMediaByOrganizer(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	ctx := context.Background()
	repo := rdb.NewSeriesRepository(testDB)

	t.Run("returns the cover and a media no series uses", func(t *testing.T) {
		// @spec components/entity/media/list-by-organizer "Cover and replaced upload"
		o := seedDeletableOrganizer(t, entity.OrganizerStatusActive)
		other := seedDeletableOrganizer(t, entity.OrganizerStatusActive)

		got, err := repo.ListMediaByOrganizer(ctx, o.organizerID)

		require.NoError(t, err)
		ids := make([]string, 0, len(got))
		for _, m := range got {
			assert.Equal(t, o.organizerID, m.OrganizerID)
			assert.Equal(t, entity.MediaKindImage, m.Kind)
			ids = append(ids, m.ID)
		}
		assert.ElementsMatch(t, []string{o.coverID, o.unusedID}, ids)
		assert.NotContains(t, ids, other.coverID)
	})

	t.Run("returns an empty list when the organizer owns no media", func(t *testing.T) {
		// @spec components/entity/media/list-by-organizer "No media"
		got, err := repo.ListMediaByOrganizer(ctx, entity.NewID())

		require.NoError(t, err)
		assert.Empty(t, got)
		assert.NotNil(t, got)
	})
}
