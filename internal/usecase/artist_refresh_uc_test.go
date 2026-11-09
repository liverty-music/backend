package usecase_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// newRefreshUseCase returns an ArtistUseCase whose Refresh and RefreshDue run
// against the given mocks and clock.
func newRefreshUseCase(t *testing.T, repo *mocks.MockArtistRepository, idManager *mocks.MockArtistIdentityManager, clock usecase.Clock) usecase.ArtistUseCase {
	t.Helper()
	return usecase.NewArtistUseCase(repo, nil, nil, idManager, nil, clock, newTestLogger(t))
}

func TestArtistUseCase_Refresh(t *testing.T) {
	t.Parallel()

	const (
		artistID = "artist-1"
		mbid     = "5b11f4ce-a62d-471e-81fc-a69a8278c7da"
	)

	type catalog struct {
		artist *entity.Artist
		err    error
	}
	tests := []struct {
		name        string
		currentName string
		catalog     catalog
		// wantName is the name passed to UpdateName; empty when it must not be called.
		wantName string
		// wantEnded is the value passed to UpdateActivityEnded; nil when it must not be called.
		wantEnded      *bool
		updateNameErr  error
		updateEndedErr error
		wantErr        error
	}{
		{
			// @spec components/usecase/artist/refresh "Catalog name differs"
			name:        "Catalog name differs",
			currentName: "the band",
			catalog:     catalog{artist: &entity.Artist{Name: "The Band", MBID: mbid}},
			wantName:    "The Band",
			wantEnded:   new(false),
		},
		{
			// @spec components/usecase/artist/refresh "Catalog name equal"
			name:        "Catalog name equal",
			currentName: "The Band",
			catalog:     catalog{artist: &entity.Artist{Name: "The Band", MBID: mbid}},
			wantEnded:   new(false),
		},
		{
			// @spec components/usecase/artist/refresh "Catalog name empty"
			name:        "Catalog name empty",
			currentName: "The Band",
			catalog:     catalog{artist: &entity.Artist{Name: "", MBID: mbid}},
			wantEnded:   new(false),
		},
		{
			// @spec components/usecase/artist/refresh "Catalog marks the activity as ended"
			name:        "Catalog marks the activity as ended",
			currentName: "SMAP",
			catalog:     catalog{artist: &entity.Artist{Name: "SMAP", MBID: mbid, ActivityEnded: true}},
			wantEnded:   new(true),
		},
		{
			// @spec components/usecase/artist/refresh "Catalog no longer marks the activity as ended"
			name:        "Catalog no longer marks the activity as ended",
			currentName: "ELLEGARDEN",
			catalog:     catalog{artist: &entity.Artist{Name: "ELLEGARDEN", MBID: mbid, ActivityEnded: false}},
			wantEnded:   new(false),
		},
		{
			// @spec components/usecase/artist/refresh "MBID unknown to the catalog"
			name:        "MBID unknown to the catalog",
			currentName: "The Band",
			catalog:     catalog{err: apperr.ErrNotFound},
		},
		{
			// @spec components/usecase/artist/refresh "Catalog unavailable"
			name:        "Catalog unavailable",
			currentName: "The Band",
			catalog:     catalog{err: apperr.ErrUnavailable},
			wantErr:     apperr.ErrUnavailable,
		},
		{
			// @spec components/usecase/artist/refresh "Artist gone"
			name:           "Artist gone",
			currentName:    "The Band",
			catalog:        catalog{artist: &entity.Artist{Name: "The Band", MBID: mbid}},
			wantEnded:      new(false),
			updateEndedErr: apperr.ErrNotFound,
			wantErr:        apperr.ErrNotFound,
		},
		{
			// @spec components/usecase/artist/refresh "Name update fails"
			name:          "Name update fails",
			currentName:   "the band",
			catalog:       catalog{artist: &entity.Artist{Name: "The Band", MBID: mbid}},
			wantName:      "The Band",
			updateNameErr: apperr.ErrNotFound,
			wantErr:       apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo := mocks.NewMockArtistRepository(t)
			idManager := mocks.NewMockArtistIdentityManager(t)
			uc := newRefreshUseCase(t, repo, idManager, time.Now)

			idManager.EXPECT().GetArtist(anyCtx, mbid).Return(tt.catalog.artist, tt.catalog.err).Once()
			if tt.wantName != "" {
				repo.EXPECT().UpdateName(anyCtx, artistID, tt.wantName).Return(tt.updateNameErr).Once()
			}
			if tt.wantEnded != nil {
				repo.EXPECT().UpdateActivityEnded(anyCtx, artistID, *tt.wantEnded).Return(tt.updateEndedErr).Once()
			}

			err := uc.Refresh(t.Context(), artistID, mbid, tt.currentName)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}

func TestArtistUseCase_RefreshDue(t *testing.T) {
	t.Parallel()

	japan := time.FixedZone("Asia/Tokyo", 9*60*60)

	// @spec components/usecase/artist/refresh "Run picks today's slot"
	t.Run("Run picks today's slot", func(t *testing.T) {
		t.Parallel()
		repo := mocks.NewMockArtistRepository(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		// The CronJob fires at 05:00 Japan time, which is still the previous
		// day in UTC.
		runTime := time.Date(2026, 11, 10, 5, 0, 0, 0, japan)
		uc := newRefreshUseCase(t, repo, idManager, func() time.Time { return runTime })
		artist := &entity.Artist{ID: "artist-1", Name: "The Band", MBID: "mbid-1"}

		repo.EXPECT().ListRefreshDue(anyCtx, 30, 7).Return([]*entity.Artist{artist}, nil).Once()
		idManager.EXPECT().GetArtist(anyCtx, "mbid-1").Return(&entity.Artist{Name: "The Band", MBID: "mbid-1", ActivityEnded: true}, nil).Once()
		repo.EXPECT().UpdateActivityEnded(anyCtx, "artist-1", true).Return(nil).Once()

		summary, err := uc.RefreshDue(t.Context())

		require.NoError(t, err)
		assert.Equal(t, usecase.ArtistRefreshSummary{Slot: 7, Listed: 1, Attempted: 1, Ended: 1}, summary)
	})

	// @spec components/usecase/artist/refresh "Every artist within 30 days"
	t.Run("Every artist within 30 days", func(t *testing.T) {
		t.Parallel()
		repo := mocks.NewMockArtistRepository(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		day := time.Date(2026, 11, 10, 5, 0, 0, 0, japan)
		uc := newRefreshUseCase(t, repo, idManager, func() time.Time { return day })

		// Every artist sits in exactly one slot (ListRefreshDue's promise); the
		// run must visit each of the 30 slots once over 30 consecutive days.
		visited := map[int]int{}
		repo.EXPECT().ListRefreshDue(anyCtx, 30, mock.AnythingOfType("int")).
			RunAndReturn(func(_ context.Context, _, slot int) ([]*entity.Artist, error) {
				visited[slot]++
				return []*entity.Artist{}, nil
			}).Times(30)

		for range 30 {
			_, err := uc.RefreshDue(t.Context())
			require.NoError(t, err)
			day = day.AddDate(0, 0, 1)
		}

		require.Len(t, visited, 30)
		for slot := range 30 {
			assert.Equal(t, 1, visited[slot], "slot %d", slot)
		}
	})

	// @spec components/usecase/artist/refresh "Consecutive failures"
	t.Run("Consecutive failures", func(t *testing.T) {
		t.Parallel()
		repo := mocks.NewMockArtistRepository(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		uc := newRefreshUseCase(t, repo, idManager, time.Now)

		artists := make([]*entity.Artist, 5)
		for i := range artists {
			artists[i] = &entity.Artist{ID: fmt.Sprintf("artist-%d", i), Name: fmt.Sprintf("Band %d", i), MBID: fmt.Sprintf("mbid-%d", i)}
		}
		repo.EXPECT().ListRefreshDue(anyCtx, 30, mock.AnythingOfType("int")).Return(artists, nil).Once()
		// The first three lookups fail; no name or status is written for them,
		// and the last two artists are never looked up.
		for _, a := range artists[:3] {
			idManager.EXPECT().GetArtist(anyCtx, a.MBID).Return(nil, apperr.ErrUnavailable).Once()
		}

		summary, err := uc.RefreshDue(t.Context())

		require.NoError(t, err)
		assert.True(t, summary.CircuitBroken)
		assert.Equal(t, 5, summary.Listed)
		assert.Equal(t, 3, summary.Attempted)
		assert.Equal(t, 3, summary.Failed)
	})

	t.Run("a single failure does not stop the run", func(t *testing.T) {
		t.Parallel()
		repo := mocks.NewMockArtistRepository(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		uc := newRefreshUseCase(t, repo, idManager, time.Now)

		failing := &entity.Artist{ID: "artist-0", Name: "Band 0", MBID: "mbid-0"}
		renamed := &entity.Artist{ID: "artist-1", Name: "band 1", MBID: "mbid-1"}
		repo.EXPECT().ListRefreshDue(anyCtx, 30, mock.AnythingOfType("int")).Return([]*entity.Artist{failing, renamed}, nil).Once()
		idManager.EXPECT().GetArtist(anyCtx, "mbid-0").Return(nil, apperr.ErrUnavailable).Once()
		idManager.EXPECT().GetArtist(anyCtx, "mbid-1").Return(&entity.Artist{Name: "Band 1", MBID: "mbid-1"}, nil).Once()
		repo.EXPECT().UpdateName(anyCtx, "artist-1", "Band 1").Return(nil).Once()
		repo.EXPECT().UpdateActivityEnded(anyCtx, "artist-1", false).Return(nil).Once()

		summary, err := uc.RefreshDue(t.Context())

		require.NoError(t, err)
		assert.False(t, summary.CircuitBroken)
		assert.Equal(t, 2, summary.Attempted)
		assert.Equal(t, 1, summary.Failed)
		assert.Equal(t, 1, summary.Renamed)
	})

	t.Run("listing the slot fails", func(t *testing.T) {
		t.Parallel()
		repo := mocks.NewMockArtistRepository(t)
		uc := newRefreshUseCase(t, repo, mocks.NewMockArtistIdentityManager(t), time.Now)

		repo.EXPECT().ListRefreshDue(anyCtx, 30, mock.AnythingOfType("int")).Return(nil, apperr.ErrInternal).Once()

		_, err := uc.RefreshDue(t.Context())

		assert.ErrorIs(t, err, apperr.ErrInternal)
	})
}
