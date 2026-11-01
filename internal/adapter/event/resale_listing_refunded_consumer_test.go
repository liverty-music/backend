package event_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var listingRefunded = entity.ResaleListingRefundedData{
	ResaleListingID: "listing-1", UserID: "fan-1", OrderID: "order-1", EventID: "event-1",
}

func makeListingRefundedMsg(t *testing.T) *message.Message {
	t.Helper()
	payload, err := json.Marshal(listingRefunded)
	require.NoError(t, err)
	return message.NewMessage(listingRefunded.ResaleListingID, payload)
}

func TestResaleListingRefundedConsumer(t *testing.T) {
	t.Parallel()

	t.Run("sends the sold email", func(t *testing.T) {
		t.Parallel()
		notifications := ucmocks.NewMockNotificationUseCase(t)
		consumer := event.NewResaleListingRefundedConsumer(notifications, newTestLogger(t))
		notifications.EXPECT().SendResaleSold(anyCtx, listingRefunded).Return(nil).Once()

		assert.NoError(t, consumer.Handle(makeListingRefundedMsg(t)))
	})

	t.Run("a failed email is returned for redelivery", func(t *testing.T) {
		t.Parallel()
		notifications := ucmocks.NewMockNotificationUseCase(t)
		consumer := event.NewResaleListingRefundedConsumer(notifications, newTestLogger(t))
		notifications.EXPECT().SendResaleSold(anyCtx, listingRefunded).Return(errors.New("postmark down"))

		assert.Error(t, consumer.Handle(makeListingRefundedMsg(t)))
	})

	t.Run("an unavailable mail service is delivered again later", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-resale-sold "Mail unavailable"
		notifications := ucmocks.NewMockNotificationUseCase(t)
		consumer := event.NewResaleListingRefundedConsumer(notifications, newTestLogger(t))
		notifications.EXPECT().SendResaleSold(anyCtx, listingRefunded).Return(apperr.New(codes.Unavailable, "postmark down")).Once()

		err := consumer.Handle(makeListingRefundedMsg(t))

		assert.True(t, messaging.IsRedeliverLater(err), "the message waits for a later delivery instead of the poison queue")
	})

	t.Run("a malformed payload is rejected", func(t *testing.T) {
		t.Parallel()
		consumer := event.NewResaleListingRefundedConsumer(ucmocks.NewMockNotificationUseCase(t), newTestLogger(t))

		assert.Error(t, consumer.Handle(message.NewMessage("x", []byte("{"))))
	})
}
