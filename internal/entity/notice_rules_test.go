package entity_test

import (
	"testing"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
)

func TestOrder_GetsCancellationNotice(t *testing.T) {
	t.Parallel()

	for status, want := range map[entity.OrderStatus]bool{
		entity.OrderStatusPending:  false,
		entity.OrderStatusPaid:     true,
		entity.OrderStatusVoid:     false,
		entity.OrderStatusRefunded: false,
	} {
		assert.Equal(t, want, (&entity.Order{Status: status}).GetsCancellationNotice(), status.String())
	}
}

func TestOrder_GetsRefundNotice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		refund *entity.OrderRefund
		want   bool
	}{
		{"refunded for the cancellation", &entity.OrderRefund{Reason: entity.RefundReasonCancellation, Amount: 8000}, true},
		{"not refunded", nil, false},
		{"refunded for a dispute", &entity.OrderRefund{Reason: entity.RefundReasonDispute, Amount: 8000}, false},
		{"nothing left to refund after a resale", &entity.OrderRefund{Reason: entity.RefundReasonCancellation, Amount: 0}, false},
	}
	for _, tt := range tests {
		assert.Equal(t, tt.want, (&entity.Order{Refund: tt.refund}).GetsRefundNotice(), tt.name)
	}
}

func TestNotification_Stamp(t *testing.T) {
	t.Parallel()

	t.Run("a sales reminder names its sale and milestone", func(t *testing.T) {
		t.Parallel()
		n := &entity.Notification{ID: "n-1", Type: entity.NotificationTypeSalesReminder,
			Subject: entity.NotificationSubject{TicketSaleID: "sale-1", Stage: entity.ReminderStageApplyClose24H}}
		p := &entity.NotificationPayload{Data: map[string]string{"url": "/tickets"}}

		n.Stamp(p)

		assert.Equal(t, map[string]string{
			"url":                                    "/tickets",
			entity.NotificationDataKeyNotificationID: "n-1",
			entity.NotificationDataKeyType:           "sales_reminder",
			entity.NotificationDataKeyTicketSaleID:   "sale-1",
			entity.NotificationDataKeyMilestone:      "apply_close_24h",
		}, p.Data)
	})

	t.Run("another type carries only its id and type", func(t *testing.T) {
		t.Parallel()
		n := &entity.Notification{ID: "n-2", Type: entity.NotificationTypeLotteryLoss,
			Subject: entity.NotificationSubject{LotteryEntryID: "entry-1"}}
		p := &entity.NotificationPayload{}

		n.Stamp(p)

		assert.Equal(t, map[string]string{
			entity.NotificationDataKeyNotificationID: "n-2",
			entity.NotificationDataKeyType:           "lottery_loss",
		}, p.Data)
	})
}

func TestNewConcertsOfArtist(t *testing.T) {
	t.Parallel()

	artist := &entity.Artist{ID: "artist-1"}
	other := &entity.Artist{ID: "artist-2"}
	concertOf := func(id string, performers ...*entity.Artist) *entity.Concert {
		c := &entity.Concert{Artists: performers}
		c.ID = id
		return c
	}
	headlined := concertOf("c-1", artist)
	festival := concertOf("c-2", other, artist)
	orphan := concertOf("c-3")
	foreign := concertOf("c-4", other)

	t.Run("keeps the concerts the artist performs at and leaves out one without performers", func(t *testing.T) {
		t.Parallel()
		got, orphans, err := entity.NewConcertsOfArtist("artist-1", []string{"c-1", "c-2", "c-3"}, []*entity.Concert{headlined, festival, orphan})

		require.NoError(t, err)
		assert.Equal(t, []*entity.Concert{headlined, festival}, got)
		assert.Equal(t, []string{"c-3"}, orphans)
	})

	t.Run("an id that names no concert is refused", func(t *testing.T) {
		t.Parallel()
		_, _, err := entity.NewConcertsOfArtist("artist-1", []string{"c-1", "c-9"}, []*entity.Concert{headlined})

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("a concert the artist does not perform at is refused", func(t *testing.T) {
		t.Parallel()
		_, _, err := entity.NewConcertsOfArtist("artist-1", []string{"c-4"}, []*entity.Concert{foreign})

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestBelongsTo(t *testing.T) {
	t.Parallel()

	const fan, other entity.UserID = "fan-1", "fan-2"
	order := &entity.Order{UserID: fan}

	assert.True(t, order.BelongsTo(fan))
	assert.False(t, order.BelongsTo(other))
	assert.True(t, (&entity.ResaleListing{UserID: fan}).BelongsTo(fan))
	assert.False(t, (&entity.ResaleListing{UserID: fan}).BelongsTo(other))
	assert.True(t, entity.CardAuthorizationPurchase{UserID: fan}.BelongsTo(fan))
	assert.False(t, entity.CardAuthorizationPurchase{UserID: fan}.BelongsTo(other))
	assert.True(t, (&entity.Notification{UserID: "fan-1"}).BelongsTo("fan-1"))
	assert.False(t, (&entity.Notification{UserID: "fan-1"}).BelongsTo("fan-2"))
	assert.True(t, (&entity.ResaleEntry{Order: order}).BelongsTo(fan))
	assert.False(t, (&entity.ResaleEntry{Order: order}).BelongsTo(other))
	assert.False(t, (&entity.ResaleEntry{}).BelongsTo(fan), "an entry read without its Order belongs to nobody")
}
