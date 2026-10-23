package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

const commerceFanID = "11111111-2222-3333-4444-555555555555"

// recordingMetrics returns a metrics mock that accepts the lag sample and the
// given status once per handled message.
func recordingMetrics(t *testing.T, status string, times int) *ucmocks.MockAnalyticsConsumerMetrics {
	t.Helper()
	m := ucmocks.NewMockAnalyticsConsumerMetrics(t)
	m.EXPECT().RecordMessage(mock.Anything, status).Times(times)
	m.EXPECT().RecordLag(mock.Anything, mock.Anything).Times(times)
	return m
}

// Stable ids of the commerce announcements (UUIDs, as the entities mint them).
const (
	drawnEntryID     = "019a0000-0000-7000-8000-00000000e001"
	expiredCheckout  = "019a0000-0000-7000-8000-00000000c001"
	admittedTicketID = "019a0000-0000-7000-8000-00000000t001"
)

// TestAnalyticsConsumer_FanSignals covers the routing of the hype, lottery
// result, checkout expiry and admission subjects to their catalogue events.
// A hype change has no stable id (each change is a new fact) and is enqueued
// plainly; the other three are enqueued once per id and the time in the data.
func TestAnalyticsConsumer_FanSignals(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 11, 20, 18, 32, 0, 123456789, time.UTC)
	atMillis := at.Truncate(time.Millisecond)

	tests := []struct {
		name      string
		data      any
		handle    func(c *event.AnalyticsConsumer, msg *message.Message) error
		wantEvent usecase.AnalyticsEventName
		wantProps usecase.AnalyticsProperties
		// wantOnce is the stable event id; empty for a plain Enqueue.
		wantOnce string
	}{
		{
			name:      "hype changed",
			data:      entity.ArtistHypeChangedData{UserID: commerceFanID, ArtistID: "artist-1", FromHype: "watch", ToHype: "away"},
			handle:    (*event.AnalyticsConsumer).HandleArtistHypeChanged,
			wantEvent: usecase.EventArtistHypeChanged,
			wantProps: usecase.AnalyticsProperties{"artist_id": "artist-1", "from_hype": "watch", "to_hype": "away"},
		},
		{
			name: "lottery result",
			data: entity.LotteryEntryDrawnData{
				LotteryEntryID: drawnEntryID, UserID: commerceFanID, TicketSaleID: "sale-1", TicketTypeID: "tt-1",
				Result: "lost", CaptureFailed: true, DrawTime: at,
			},
			handle:    (*event.AnalyticsConsumer).HandleLotteryEntryDrawn,
			wantEvent: usecase.EventTicketLotteryResult,
			wantProps: usecase.AnalyticsProperties{
				"lottery_entry_id": drawnEntryID, "ticket_sale_id": "sale-1", "ticket_type_id": "tt-1",
				"result": "lost", "capture_failed": true,
			},
			wantOnce: drawnEntryID,
		},
		{
			name:      "checkout expired",
			data:      entity.CheckoutExpiredData{CheckoutID: expiredCheckout, UserID: commerceFanID, TicketTypeID: "tt-1", TicketCount: 2, HoldExpireTime: at},
			handle:    (*event.AnalyticsConsumer).HandleCheckoutExpired,
			wantEvent: usecase.EventCheckoutExpired,
			wantProps: usecase.AnalyticsProperties{"checkout_id": expiredCheckout, "ticket_type_id": "tt-1", "ticket_count": 2},
			wantOnce:  expiredCheckout,
		},
		{
			name:      "ticket admitted",
			data:      entity.TicketAdmittedData{TicketID: admittedTicketID, UserID: commerceFanID, EventID: "event-1", AdmitTime: at},
			handle:    (*event.AnalyticsConsumer).HandleTicketAdmitted,
			wantEvent: usecase.EventTicketAdmitted,
			wantProps: usecase.AnalyticsProperties{"ticket_id": admittedTicketID, "event_id": "event-1"},
			wantOnce:  admittedTicketID,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			client := ucmocks.NewMockAnalyticsClient(t)
			if tt.wantOnce == "" {
				client.EXPECT().Enqueue(mock.Anything, commerceFanID, tt.wantEvent, tt.wantProps).Return(nil).Once()
			} else {
				client.EXPECT().EnqueueOnce(mock.Anything, commerceFanID, tt.wantOnce, atMillis, tt.wantEvent, tt.wantProps).Return(nil).Once()
			}

			c := event.NewAnalyticsConsumer(client, recordingMetrics(t, "forwarded", 1), newTestLogger(t))
			require.NoError(t, tt.handle(c, makeAnalyticsMsg(t, tt.data)))
		})
	}
}

// TestAnalyticsConsumer_FanSignals_Skips covers the skip and failure paths
// shared by the fan-level handlers.
func TestAnalyticsConsumer_FanSignals_Skips(t *testing.T) {
	t.Parallel()

	t.Run("nil client", func(t *testing.T) {
		t.Parallel()
		c := event.NewAnalyticsConsumer(nil, recordingMetrics(t, "skipped_nil_client", 1), newTestLogger(t))
		require.NoError(t, c.HandleTicketAdmitted(makeAnalyticsMsg(t, entity.TicketAdmittedData{TicketID: admittedTicketID, UserID: commerceFanID})))
	})

	t.Run("missing user id", func(t *testing.T) {
		t.Parallel()
		client := ucmocks.NewMockAnalyticsClient(t)
		c := event.NewAnalyticsConsumer(client, recordingMetrics(t, "skipped_empty_user_id", 1), newTestLogger(t))
		require.NoError(t, c.HandleCheckoutExpired(makeAnalyticsMsg(t, entity.CheckoutExpiredData{CheckoutID: "c"})))
	})

	t.Run("bad payload", func(t *testing.T) {
		t.Parallel()
		client := ucmocks.NewMockAnalyticsClient(t)
		c := event.NewAnalyticsConsumer(client, recordingMetrics(t, "skipped_parse_error", 1), newTestLogger(t))
		msg := message.NewMessage("bad", []byte("{not json"))
		msg.Metadata.Set("ce_time", time.Now().UTC().Format(time.RFC3339))
		assert.ErrorIs(t, c.HandleArtistHypeChanged(msg), apperr.ErrInternal)
	})

	t.Run("enqueue fails", func(t *testing.T) {
		t.Parallel()
		client := ucmocks.NewMockAnalyticsClient(t)
		client.EXPECT().Enqueue(mock.Anything, commerceFanID, usecase.EventArtistHypeChanged, mock.Anything).Return(errors.New("queue full")).Once()
		c := event.NewAnalyticsConsumer(client, recordingMetrics(t, "enqueue_error", 1), newTestLogger(t))
		err := c.HandleArtistHypeChanged(makeAnalyticsMsg(t, entity.ArtistHypeChangedData{UserID: commerceFanID, ArtistID: "a"}))
		assert.ErrorIs(t, err, apperr.ErrInternal)
	})
}

// paidAt is the paid Order's pay time as read back from the database.
var paidAt = time.Date(2026, 11, 5, 9, 0, 0, 123456000, time.UTC)

// paidCheckout is the ORDER.paid announcement of a fan's 2-ticket Checkout.
var paidCheckout = entity.OrderPaidData{
	OrderID:     "019a0000-0000-7000-8000-00000000a001",
	UserID:      commerceFanID,
	EventID:     "event-1",
	TicketCount: 2,
	Amount:      16000,
	Currency:    "JPY",
	CheckoutID:  "checkout-1",
	PayTime:     paidAt,
}

// @spec components/usecase/order/fulfill-payment "Paid checkout"
func TestAnalyticsConsumer_HandleOrderPaid_PaidCheckout(t *testing.T) {
	t.Parallel()

	client := ucmocks.NewMockAnalyticsClient(t)
	client.EXPECT().
		EnqueueOnce(mock.Anything, commerceFanID, paidCheckout.OrderID, paidAt.Truncate(time.Millisecond), usecase.EventOrderPaid, usecase.AnalyticsProperties{
			"order_id":     paidCheckout.OrderID,
			"event_id":     "event-1",
			"ticket_count": 2,
			"amount":       int64(16000),
			"currency":     "JPY",
			"source":       "checkout",
			"checkout_id":  "checkout-1",
		}).
		Return(nil).
		Once()

	c := event.NewAnalyticsConsumer(client, recordingMetrics(t, "forwarded", 1), newTestLogger(t))
	require.NoError(t, c.HandleOrderPaid(makeAnalyticsMsg(t, paidCheckout)))
}

// An Order announced twice is handed off with the same event id (the Order
// id) and the same timestamp (its pay time), both when the same message is
// redelivered and when FulfillPayment re-publishes the Order later as a new
// message with its own publish time (a webhook retry after the stream's
// 2-minute duplicate window). PostHog treats events with the same uuid, event
// name, timestamp and distinct_id as duplicates and keeps one
// (https://posthog.com/docs/data-pipelines/troubleshooting), so one
// order-paid signal is recorded for the Order.
//
// @spec components/usecase/order/fulfill-payment "Announcement handled twice"
func TestAnalyticsConsumer_HandleOrderPaid_HandledTwice(t *testing.T) {
	t.Parallel()

	first := makeAnalyticsMsg(t, paidCheckout)
	first.Metadata.Set("ce_time", "2026-11-05T09:00:01Z")
	// The re-published announcement: a new message, published 10 minutes
	// later. Its pay time went through a round trip of a lower precision
	// store (microseconds), as an Order re-read from the database does.
	republishedData := paidCheckout
	republishedData.PayTime = paidAt.Truncate(time.Microsecond)
	republished := makeAnalyticsMsg(t, republishedData)
	republished.Metadata.Set("ce_time", "2026-11-05T09:10:01Z")

	type handoff struct {
		eventID string
		at      time.Time
	}
	var handoffs []handoff
	client := ucmocks.NewMockAnalyticsClient(t)
	client.EXPECT().
		EnqueueOnce(mock.Anything, commerceFanID, mock.Anything, mock.Anything, usecase.EventOrderPaid, mock.Anything).
		RunAndReturn(func(_ context.Context, _, eventID string, at time.Time, _ usecase.AnalyticsEventName, _ usecase.AnalyticsProperties) error {
			handoffs = append(handoffs, handoff{eventID: eventID, at: at})
			return nil
		}).
		Times(3)

	c := event.NewAnalyticsConsumer(client, recordingMetrics(t, "forwarded", 3), newTestLogger(t))
	require.NoError(t, c.HandleOrderPaid(first))
	require.NoError(t, c.HandleOrderPaid(first.Copy()))
	require.NoError(t, c.HandleOrderPaid(republished))

	require.Len(t, handoffs, 3)
	want := handoff{eventID: paidCheckout.OrderID, at: paidAt.Truncate(time.Millisecond)}
	for _, h := range handoffs {
		assert.Equal(t, want, h, "every hand-off carries the Order id and its pay time, not the publish time")
	}
}

// An announcement published before OrderPaidData carried the pay time falls
// back to its publish time.
func TestAnalyticsConsumer_HandleOrderPaid_WithoutPayTime(t *testing.T) {
	t.Parallel()

	data := paidCheckout
	data.PayTime = time.Time{}
	msg := makeAnalyticsMsg(t, data)
	msg.Metadata.Set("ce_time", "2026-11-05T09:00:01Z")
	client := ucmocks.NewMockAnalyticsClient(t)
	client.EXPECT().
		EnqueueOnce(mock.Anything, commerceFanID, data.OrderID, time.Date(2026, 11, 5, 9, 0, 1, 0, time.UTC), usecase.EventOrderPaid, mock.Anything).
		Return(nil).Once()

	c := event.NewAnalyticsConsumer(client, recordingMetrics(t, "forwarded", 1), newTestLogger(t))
	require.NoError(t, c.HandleOrderPaid(msg))
}

// A paid won lottery entry names the entry as its source.
func TestAnalyticsConsumer_HandleOrderPaid_LotterySource(t *testing.T) {
	t.Parallel()

	data := paidCheckout
	data.CheckoutID, data.LotteryEntryID = "", "entry-1"
	client := ucmocks.NewMockAnalyticsClient(t)
	client.EXPECT().
		EnqueueOnce(mock.Anything, commerceFanID, data.OrderID, mock.Anything, usecase.EventOrderPaid,
			mock.MatchedBy(func(p usecase.AnalyticsProperties) bool {
				_, hasCheckout := p["checkout_id"]
				return p["source"] == "lottery_entry" && p["lottery_entry_id"] == "entry-1" && !hasCheckout
			})).
		Return(nil).
		Once()

	c := event.NewAnalyticsConsumer(client, recordingMetrics(t, "forwarded", 1), newTestLogger(t))
	require.NoError(t, c.HandleOrderPaid(makeAnalyticsMsg(t, data)))
}
