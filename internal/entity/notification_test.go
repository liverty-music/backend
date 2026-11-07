package entity_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
)

func TestNotificationSubject_Validate(t *testing.T) {
	t.Parallel()

	sale := "sale-1"
	event := "event-1"
	order := entity.NotificationSubject{OrderID: "order-1"}

	t.Run("reminder without a stage", func(t *testing.T) {
		// @spec components/entity/notification "Reminder without a stage"
		t.Parallel()

		err := entity.NotificationSubject{TicketSaleID: sale, EventID: event}.Validate(entity.NotificationTypeSalesReminder)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("new concerts subject", func(t *testing.T) {
		// @spec components/entity/notification "New concerts subject"
		t.Parallel()

		err := entity.NotificationSubject{ArtistID: "artist-1", EventIDs: []string{"e-1", "e-2"}}.Validate(entity.NotificationTypeNewConcerts)

		assert.NoError(t, err)
	})

	t.Run("each type accepts the records it is about", func(t *testing.T) {
		t.Parallel()

		for typ, subject := range map[entity.NotificationType]entity.NotificationSubject{
			entity.NotificationTypeTicketSaleAnnouncement: {TicketSaleID: sale, EventID: event},
			entity.NotificationTypeSalesReminder:          {TicketSaleID: sale, EventID: event, Stage: entity.ReminderStageApplyOpen},
			entity.NotificationTypeLotteryWin:             order,
			entity.NotificationTypeResaleWin:              order,
			entity.NotificationTypeLotteryLoss:            {LotteryEntryID: "entry-1"},
			entity.NotificationTypeResaleEntryLapsed:      {ResaleEntryID: "entry-1"},
			entity.NotificationTypeResaleEntryLost:        {ResaleEntryID: "entry-1"},
			entity.NotificationTypeConcertCancelled:       order,
			entity.NotificationTypeOrderRefunded:          order,
		} {
			assert.NoError(t, subject.Validate(typ), string(typ))
		}
	})

	t.Run("a record the type is not about is invalid", func(t *testing.T) {
		t.Parallel()

		err := entity.NotificationSubject{OrderID: "order-1", ArtistID: "artist-1"}.Validate(entity.NotificationTypeLotteryWin)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("an undefined stage is invalid", func(t *testing.T) {
		t.Parallel()

		err := entity.NotificationSubject{TicketSaleID: sale, EventID: event, Stage: 4}.Validate(entity.NotificationTypeSalesReminder)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("an unknown type is invalid", func(t *testing.T) {
		t.Parallel()

		assert.ErrorIs(t, order.Validate("order_confirmation"), apperr.ErrInvalidArgument)
	})
}
