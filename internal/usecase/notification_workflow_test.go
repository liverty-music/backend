package usecase_test

import (
	"context"
	"testing"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
)

func TestNotificationUseCase_RunDeliver(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	type deps struct {
		notifs   *entitymocks.MockNotificationRepository
		pushSubs *entitymocks.MockPushSubscriptionRepository
		sender   *entitymocks.MockPushNotificationSender
		uc       usecase.NotificationUseCase
	}
	newDeps := func(t *testing.T) *deps {
		d := &deps{
			notifs:   entitymocks.NewMockNotificationRepository(t),
			pushSubs: entitymocks.NewMockPushSubscriptionRepository(t),
			sender:   entitymocks.NewMockPushNotificationSender(t),
		}
		d.uc = usecase.NewNotificationUseCase(d.notifs, d.pushSubs, d.sender, nil, noopMetrics{}, newTestLogger(t),
			usecase.OrderConfirmationDeps{}, usecase.NewConcertsDeps{})
		return d
	}
	// expectDelivered expects one notification of typ for user, delivered to
	// one browser.
	expectDelivered := func(d *deps, user string, typ entity.NotificationType) {
		d.notifs.EXPECT().Create(mock.Anything, mock.MatchedBy(func(n *entity.Notification) bool {
			return n.UserID == user && n.Type == typ
		})).RunAndReturn(func(_ context.Context, n *entity.Notification) error { n.ID = "notif-" + user; return nil }).Once()
		d.pushSubs.EXPECT().ListByUserIDs(mock.Anything, []string{user}).Return([]*entity.PushSubscription{{ID: "sub-" + user, UserID: user}}, nil).Once()
		d.sender.EXPECT().Send(mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
		d.notifs.EXPECT().UpdateDelivery(mock.Anything, "notif-"+user, entity.NotificationDeliveryStatusDelivered, mock.Anything, "").Return(nil).Once()
	}
	request := func(user string, typ entity.NotificationType) usecase.DeliverRequest {
		return usecase.DeliverRequest{UserID: user, Type: typ, Payload: entity.NewNotificationPayload("Title", "Body", "/", "tag")}
	}

	t.Run("notification requested", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/deliver "Notification requested"
		d := newDeps(t)
		expectDelivered(d, "fan-1", entity.NotificationTypeNewConcerts)

		id, err := d.uc.RunDeliver(ctx, &inlineFlow{}, request("fan-1", entity.NotificationTypeNewConcerts))

		require.NoError(t, err)
		assert.Equal(t, "notif-fan-1", id)
	})

	t.Run("discovered sale announced", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/deliver "Discovered sale announced"
		d := newDeps(t)
		expectDelivered(d, "fan-1", entity.NotificationTypeTicketSaleAnnouncement)

		_, err := d.uc.RunDeliver(ctx, &inlineFlow{}, request("fan-1", entity.NotificationTypeTicketSaleAnnouncement))

		require.NoError(t, err)
	})

	t.Run("recording fails for one request", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/deliver "Recording fails for one request"
		// Each fan's notification is a workflow of its own: one that fails is
		// run again by the runtime, the others are unaffected.
		d := newDeps(t)
		expectDelivered(d, "fan-1", entity.NotificationTypeNewConcerts)
		d.notifs.EXPECT().Create(mock.Anything, mock.MatchedBy(func(n *entity.Notification) bool { return n.UserID == "fan-2" })).
			Return(apperr.New(codes.Unavailable, "db down")).Once()
		expectDelivered(d, "fan-3", entity.NotificationTypeNewConcerts)

		_, err1 := d.uc.RunDeliver(ctx, &inlineFlow{}, request("fan-1", entity.NotificationTypeNewConcerts))
		_, err2 := d.uc.RunDeliver(ctx, &inlineFlow{}, request("fan-2", entity.NotificationTypeNewConcerts))
		_, err3 := d.uc.RunDeliver(ctx, &inlineFlow{}, request("fan-3", entity.NotificationTypeNewConcerts))

		assert.NoError(t, err1)
		assert.ErrorIs(t, err2, apperr.ErrUnavailable, "the failed workflow is run again")
		assert.NoError(t, err3)
	})
}
