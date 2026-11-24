package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/usecase"
)

// sweepStub is a SettlementUseCase that records how often it is asked to sweep.
type sweepStub struct {
	calls int
	err   error
}

func (s *sweepStub) ReleaseDueSettlements(context.Context) error {
	s.calls++
	return s.err
}

func TestReleaseDueSettlementsWorkflow(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	t.Run("a tick sweeps once", func(t *testing.T) {
		t.Parallel()
		uc := &sweepStub{}

		_, err := usecase.ReleaseDueSettlementsWorkflow(uc)(t.Context(), &inlineFlow{}, at)

		require.NoError(t, err)
		assert.Equal(t, 1, uc.calls)
	})

	t.Run("a failed work list is returned as it is", func(t *testing.T) {
		t.Parallel()
		uc := &sweepStub{err: apperr.New(codes.Internal, "list held settlements")}

		_, err := usecase.ReleaseDueSettlementsWorkflow(uc)(t.Context(), &inlineFlow{}, at)

		assert.ErrorIs(t, err, apperr.ErrInternal, "the code must reach the runtime unchanged")
		assert.Equal(t, 1, uc.calls)
	})

	t.Run("the schedule is a cron expression with a seconds field every five minutes", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "0 */5 * * * *", usecase.ReleaseDueSettlementsSchedule)
		assert.Equal(t, "release-due-settlements", usecase.WorkflowReleaseDueSettlements)
	})
}
