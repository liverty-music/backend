package usecase_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestConcertUseCase_AdminList(t *testing.T) {
	t.Parallel()
	orgID := "org-1"
	unlisted := entity.SeriesVisibilityUnlisted
	published := entity.PublishStatePublished

	t.Run("return only the concerts while concert candidates are pending", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/list "Catalog and queue both populated"
		// List reads the catalog alone; the candidate queue is not one of its ports.
		want := []*entity.Concert{{ID: "event-1"}}
		d := newConcertTestDeps(t)
		d.concertRepo.EXPECT().List(mock.Anything).Return(want, nil).Once()

		got, err := d.uc.List(context.Background())

		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("return a concert of a published unlisted series", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/list "Unlisted Organizer concert"
		concert := &entity.Concert{
			ID: "event-unlisted", PublishState: &published,
			Series: &entity.Series{ID: "series-1", OrganizerID: &orgID, Visibility: &unlisted},
		}
		d := newConcertTestDeps(t)
		d.concertRepo.EXPECT().List(mock.Anything).Return([]*entity.Concert{concert}, nil).Once()

		got, err := d.uc.List(context.Background())

		require.NoError(t, err)
		assert.Equal(t, []*entity.Concert{concert}, got)
	})
}

func TestConcertUseCase_ListCatalogByArtist(t *testing.T) {
	t.Parallel()

	t.Run("return the artist's cancelled organizer concert", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/list-catalog-by-artist "One artist's concerts"
		cancelled := entity.PublishStateCancelled
		want := []*entity.Concert{{ID: "event-cancelled", PublishState: &cancelled}}
		d := newConcertTestDeps(t)
		d.concertRepo.EXPECT().ListCatalogByArtist(mock.Anything, "artist-1").Return(want, nil).Once()

		got, err := d.uc.ListCatalogByArtist(context.Background(), "artist-1")

		require.NoError(t, err)
		assert.Equal(t, want, got)
	})

	t.Run("fail with InvalidArgument and read nothing when no artist is given", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/concert/list-catalog-by-artist "Missing artist"
		d := newConcertTestDeps(t)

		_, err := d.uc.ListCatalogByArtist(context.Background(), "")

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestConcertUseCase_AdminDelete(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		eventID string
		// repoErr is what DeleteAndSuppress returns for a non-empty eventID.
		repoErr error
		wantErr error
	}{
		{
			// @spec components/usecase/concert/delete "Auto-published concert deleted"
			name: "delete and suppress a discovered concert", eventID: "event-discovered",
		},
		{
			// @spec components/usecase/concert/delete "Cancelled concert after refunds"
			name: "delete and suppress a cancelled organizer concert after refunds", eventID: "event-cancelled",
		},
		{
			// @spec components/usecase/concert/delete "Unknown id"
			name: "succeed for an id that no longer exists", eventID: "event-gone",
		},
		{
			// @spec components/usecase/concert/delete "Concert with issued tickets"
			name:    "return the repository's FailedPrecondition unchanged",
			eventID: "event-sold",
			repoErr: apperr.New(codes.FailedPrecondition, "an event has a paid order"),
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			// @spec components/usecase/concert/delete "Missing id"
			name:    "fail with InvalidArgument and remove nothing when no id is given",
			wantErr: apperr.ErrInvalidArgument,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			d := newConcertTestDeps(t)
			if tt.eventID != "" {
				d.concertRepo.EXPECT().DeleteAndSuppress(mock.Anything, tt.eventID).Return(tt.repoErr).Once()
			}

			err := d.uc.Delete(context.Background(), tt.eventID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}
