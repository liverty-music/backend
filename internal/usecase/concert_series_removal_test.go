package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestConcertCreationUseCase_RemoveEmptyDiscoveredSeries(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 11, 28, 4, 0, 0, 0, time.UTC)
	dayBefore := now.Add(-24 * time.Hour)

	// Series.DeleteOrphaned decides which Series are empty; the run only
	// asks it for those created more than a day before the run.
	for _, scenario := range []string{"Series emptied by a rejection", "Series of a merged event"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			// @spec components/usecase/series/remove-empty-discovered-series "Series emptied by a rejection"
			// @spec components/usecase/series/remove-empty-discovered-series "Series of a merged event"
			h := newCreationHarness(t)
			h.series.EXPECT().DeleteOrphaned(mock.Anything, dayBefore).Return(1, nil).Once()

			require.NoError(t, h.uc.RemoveEmptyDiscoveredSeries(context.Background(), now))
		})
	}

	t.Run("Removal fails", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/series/remove-empty-discovered-series "Removal fails"
		h := newCreationHarness(t)
		h.series.EXPECT().DeleteOrphaned(mock.Anything, dayBefore).Return(0, apperr.New(codes.Unavailable, "database down")).Once()

		err := h.uc.RemoveEmptyDiscoveredSeries(context.Background(), now)

		assert.ErrorIs(t, err, apperr.ErrUnavailable, "the run fails; the next day's run removes them")
	})

	t.Run("the scheduled run removes as of its scheduled time", func(t *testing.T) {
		t.Parallel()
		h := newCreationHarness(t)
		h.series.EXPECT().DeleteOrphaned(mock.Anything, dayBefore).Return(0, nil).Once()

		_, err := h.uc.RunRemoveEmptyDiscoveredSeries(context.Background(), &inlineFlow{}, now)

		require.NoError(t, err)
	})
}
