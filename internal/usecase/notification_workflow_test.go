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
	"github.com/liverty-music/backend/internal/usecase"
)

func TestNotificationUseCase_RunDeliver(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// newConcertsFor is a subjectFixture in which each fan can be sent the
	// new_concerts notification request returns.
	newConcertsFor := func(t *testing.T, fans ...string) *subjectFixture {
		t.Helper()
		f := newSubjectFixture(t)
		for _, fan := range fans {
			f.fanReadable(renderedFan(fan, "ja"))
		}
		f.artists.EXPECT().Get(mock.Anything, "artist-1").Return(&entity.Artist{ID: "artist-1", Name: "Vaundy"}, nil).Maybe()
		f.concertsReadable(renderedConcert("event-1"))
		return f
	}
	request := func(fan string) usecase.DeliverRequest {
		return usecase.DeliverRequest{
			UserID:  fan,
			Type:    entity.NotificationTypeNewConcerts,
			Subject: entity.NotificationSubject{ArtistID: "artist-1", EventIDs: []string{"event-1"}},
		}
	}
	// delivered expects one browser of the fan to accept the push.
	delivered := func(f *subjectFixture, fan string) *entity.Notification {
		stored := f.recordsAs("notif-" + fan)
		f.browsers(fan, "https://push/"+fan)
		f.sendAccepted("https://push/" + fan)
		return stored
	}

	t.Run("notification requested", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/deliver "Notification requested"
		f := newConcertsFor(t, "fan-1")
		stored := delivered(f, "fan-1")

		id, err := f.uc.RunDeliver(ctx, &inlineFlow{}, request("fan-1"))

		require.NoError(t, err)
		assert.Equal(t, "notif-fan-1", id)
		assert.Equal(t, "fan-1", stored.UserID)
		assert.Equal(t, entity.NotificationTypeNewConcerts, stored.Type)
		assert.Equal(t, request("fan-1").Subject, stored.Subject)
	})

	t.Run("discovered sale announced", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/deliver "Discovered sale announced"
		f := newSubjectFixture(t)
		f.fanReadable(renderedFan("fan-1", "ja"))
		f.ticketSales.EXPECT().Get(mock.Anything, entity.TicketSaleID("sale-1")).
			Return(&entity.TicketSale{ID: "sale-1", Method: entity.TicketSaleMethodLottery}, nil).Once()
		f.concertsReadable(renderedConcert("event-1"))
		stored := delivered(f, "fan-1")

		_, err := f.uc.RunDeliver(ctx, &inlineFlow{}, usecase.DeliverRequest{
			UserID:  "fan-1",
			Type:    entity.NotificationTypeTicketSaleAnnouncement,
			Subject: entity.NotificationSubject{TicketSaleID: "sale-1", EventID: "event-1"},
		})

		require.NoError(t, err)
		assert.Equal(t, entity.NotificationTypeTicketSaleAnnouncement, stored.Type)
		assert.Equal(t, "fan-1", stored.UserID)
	})

	t.Run("recording fails for one request", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/deliver "Recording fails for one request"
		// Each fan's notification is a workflow of its own: one that fails is
		// run again by the runtime, the others are unaffected. Create is matched
		// by fan, because each run is independent.
		f := newConcertsFor(t, "fan-1", "fan-2", "fan-3")
		f.notifs.EXPECT().Create(mock.Anything, mock.MatchedBy(func(n *entity.Notification) bool { return n.UserID == "fan-2" })).
			Return(apperr.New(codes.Unavailable, "db down")).Once()
		for _, fan := range []string{"fan-1", "fan-3"} {
			f.notifs.EXPECT().Create(mock.Anything, mock.MatchedBy(func(n *entity.Notification) bool { return n.UserID == fan })).
				RunAndReturn(func(_ context.Context, n *entity.Notification) error { n.ID = "notif-" + n.UserID; return nil }).Once()
			f.browsers(fan, "https://push/"+fan)
			f.sendAccepted("https://push/" + fan)
		}

		_, err1 := f.uc.RunDeliver(ctx, &inlineFlow{}, request("fan-1"))
		_, err2 := f.uc.RunDeliver(ctx, &inlineFlow{}, request("fan-2"))
		_, err3 := f.uc.RunDeliver(ctx, &inlineFlow{}, request("fan-3"))

		assert.NoError(t, err1)
		assert.ErrorIs(t, err2, apperr.ErrUnavailable, "the failed workflow is run again")
		assert.NoError(t, err3)
	})

	t.Run("subject no longer exists ends the workflow without a Notification", func(t *testing.T) {
		t.Parallel()
		f := newSubjectFixture(t)
		f.fanReadable(renderedFan("fan-1", "ja"))
		f.orders.EXPECT().Get(mock.Anything, entity.OrderID("order-1")).Return(nil, apperr.New(codes.NotFound, "order not found")).Once()

		id, err := f.uc.RunDeliver(ctx, &inlineFlow{}, usecase.DeliverRequest{
			UserID: "fan-1", Type: entity.NotificationTypeConcertCancelled, Subject: entity.NotificationSubject{OrderID: "order-1"},
		})

		require.NoError(t, err)
		assert.Empty(t, id)
	})
}
