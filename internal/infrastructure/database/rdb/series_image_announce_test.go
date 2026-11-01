package rdb_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

func TestSeriesImageRepository_CreateRequestsProcessing(t *testing.T) {
	ctx := context.Background()
	repo := rdb.NewSeriesImageRepository(testDB, testOutbox)
	newImage := func(t *testing.T) *entity.SeriesImage {
		t.Helper()
		return &entity.SeriesImage{ID: uuid.NewV7().String(), OrganizerID: seedOrganizer(t), Attributes: map[string]string{}}
	}
	requestsFor := func(t *testing.T, mark int64, seriesID, imageID string) []*entityv1.SeriesImage {
		t.Helper()
		var out []*entityv1.SeriesImage
		for _, img := range announcedAbout(t, mark, entity.SubjectSeriesImageUploaded, seriesID, func() *entityv1.SeriesImage { return &entityv1.SeriesImage{} }) {
			if img.GetId().GetValue() == imageID {
				out = append(out, img)
			}
		}
		return out
	}
	stored := func(t *testing.T, id string) int {
		t.Helper()
		var n int
		require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT count(*) FROM series_images WHERE id = $1`, id).Scan(&n))
		return n
	}

	t.Run("first attach", func(t *testing.T) {
		// @spec components/entity/series-image/create "First attach"
		// @spec components/infrastructure/backend/process/event-delivery "Request for a dedicated process"
		img, seriesID := newImage(t), uuid.NewV7().String()
		mark := outboxMark(t)

		require.NoError(t, repo.Create(ctx, img, seriesID))

		assert.Equal(t, 1, stored(t, img.ID))
		got := requestsFor(t, mark, seriesID, img.ID)
		require.Len(t, got, 1)
		assert.Equal(t, img.OrganizerID, got[0].GetOrganizerId().GetValue())
	})

	t.Run("repeated attach", func(t *testing.T) {
		// @spec components/entity/series-image/create "Repeated attach"
		img, seriesID := newImage(t), uuid.NewV7().String()
		mark := outboxMark(t)

		require.NoError(t, repo.Create(ctx, img, seriesID))
		require.NoError(t, repo.Create(ctx, img, seriesID))

		assert.Equal(t, 1, stored(t, img.ID))
		assert.Len(t, requestsFor(t, mark, seriesID, img.ID), 1)
	})

	t.Run("request cannot be recorded", func(t *testing.T) {
		// @spec components/entity/series-image/create "Request cannot be recorded"
		img, seriesID := newImage(t), uuid.NewV7().String()

		err := rdb.NewSeriesImageRepository(testDB, failingOutbox{}).Create(ctx, img, seriesID)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Zero(t, stored(t, img.ID))
	})
}
