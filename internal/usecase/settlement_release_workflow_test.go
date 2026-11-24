package usecase_test

import (
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

func TestReleaseDueSettlementsWorkflow(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

	t.Run("a tick sweeps once", func(t *testing.T) {
		t.Parallel()
		uc := ucmocks.NewMockSettlementUseCase(t)
		uc.EXPECT().ReleaseDueSettlements(mock.Anything).Return(nil).Once()

		_, err := usecase.ReleaseDueSettlementsWorkflow(uc)(t.Context(), &inlineFlow{}, at)

		require.NoError(t, err)
	})

	t.Run("a failed work list is returned as it is", func(t *testing.T) {
		t.Parallel()
		uc := ucmocks.NewMockSettlementUseCase(t)
		uc.EXPECT().ReleaseDueSettlements(mock.Anything).Return(apperr.New(codes.Internal, "list held settlements")).Once()

		_, err := usecase.ReleaseDueSettlementsWorkflow(uc)(t.Context(), &inlineFlow{}, at)

		assert.ErrorIs(t, err, apperr.ErrInternal, "the code must reach the runtime unchanged")
	})

	t.Run("the schedule is a cron expression with a seconds field every five minutes", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "0 */5 * * * *", usecase.ReleaseDueSettlementsSchedule)
		assert.Equal(t, "release-due-settlements", usecase.WorkflowReleaseDueSettlements)
	})
}
