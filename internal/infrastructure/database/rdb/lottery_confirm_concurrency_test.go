package rdb_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"uuid"
)

// TestLotteryEntryConfirm_FanAndNoticeAtTheSameTime runs the fan's Confirm
// and the payment provider's notice's Confirm for one Authorized card
// authorization at the same time, a few seconds before the sale's end time by
// the database clock, against Postgres: one entry is made, both return it,
// and the card authorization is never released (the mock fails the test on a
// Release call).
func TestLotteryEntryConfirm_FanAndNoticeAtTheSameTime(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	// @spec components/usecase/lottery-entry/confirm "Fan and notice at the same time"
	cleanDatabase(t)
	ctx := context.Background()
	logger, err := logging.New()
	require.NoError(t, err)

	f := seedStock(t, entity.TicketSaleMethodLottery, 100, 4)
	setSaleEndByDatabaseClock(t, f.SaleID, 5*time.Second)
	fan := entity.UserID(seedUser(t, "fan", uuid.NewV7().String()+"@example.test", uuid.NewV7().String()))
	const cardAuthorizationID = "pi_fan_and_notice"
	cardAuthorizations := entitymocks.NewMockCardAuthorizationRepository(t)
	cardAuthorizations.EXPECT().Get(mock.Anything, cardAuthorizationID).Return(&entity.CardAuthorization{
		ID: cardAuthorizationID, Amount: f.SeatType.AmountFor(2), CardBrand: "visa", Status: entity.CardAuthorizationStatusAuthorized,
		Purchase: entity.CardAuthorizationPurchase{
			Purpose: entity.CardAuthorizationPurposeLotteryEntry, UserID: fan, TicketSaleID: f.SaleID,
			TicketStockID: f.ID, EventID: f.EventID, TicketCount: 2,
		},
	}, nil)

	now := time.Now()
	entries := rdb.NewLotteryEntryRepository(testDB)
	uc := usecase.NewLotteryEntryUseCase(lotteryEntryDeps(t, cardAuthorizations, &recordingPublisher{},
		func() time.Time { return now }, logger))

	callers := []*entity.UserID{&fan, nil} // the fan, and the notice without a fan
	got := make([]*entity.LotteryEntry, len(callers))
	errs := make([]error, len(callers))
	var wg sync.WaitGroup
	for i, caller := range callers {
		wg.Go(func() {
			got[i], errs[i] = uc.Confirm(ctx, cardAuthorizationID, caller)
		})
	}
	wg.Wait()

	require.NoError(t, errs[0])
	require.NoError(t, errs[1])
	assert.Equal(t, got[0].ID, got[1].ID, "both return the one entry")
	stored, err := entries.ListByTicketTypeAndUser(ctx, f.SaleID, fan)
	require.NoError(t, err)
	require.Len(t, stored, 1, "one entry is made")
	assert.Equal(t, cardAuthorizationID, stored[0].Order.CardAuthorizationID)
	assert.Equal(t, entity.LotteryEntryStateEntered, stored[0].State)
	assert.Equal(t, entity.OrderStatusPending, stored[0].Order.Status, "with its Pending Order")
}
