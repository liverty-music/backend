package usecase_test

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

// subjectFixture runs the real Deliver and List over mocks of their ports, so a
// test reads the message the Notification entity renders from the records a
// subject names, as the fan's browser receives it.
type subjectFixture struct {
	// now is the clock Deliver and List read; time.Now unless a test pins it.
	now           func() time.Time
	users         *entitymocks.MockUserRepository
	follows       *entitymocks.MockFollowRepository
	artists       *entitymocks.MockArtistRepository
	concerts      *entitymocks.MockConcertRepository
	orders        *entitymocks.MockOrderRepository
	ticketSales   *entitymocks.MockTicketSaleRepository
	entries       *entitymocks.MockLotteryEntryRepository
	resaleEntries *entitymocks.MockResaleEntryRepository
	notifs        *entitymocks.MockNotificationRepository
	pushSubs      *entitymocks.MockPushSubscriptionRepository
	sender        *entitymocks.MockPushNotificationSender
	workflows     *ucmocks.MockWorkflows
	// starts lists the workflows f.uc started.
	starts func() []workflowStart
	// outcomes lists the delivery outcomes f.uc recorded.
	outcomes func() []deliveryOutcomeCall
	logs     *bytes.Buffer
	uc       usecase.NotificationUseCase
}

func newSubjectFixture(t *testing.T) *subjectFixture {
	t.Helper()
	f := &subjectFixture{
		now:           time.Now,
		users:         entitymocks.NewMockUserRepository(t),
		follows:       entitymocks.NewMockFollowRepository(t),
		artists:       entitymocks.NewMockArtistRepository(t),
		concerts:      entitymocks.NewMockConcertRepository(t),
		orders:        entitymocks.NewMockOrderRepository(t),
		ticketSales:   entitymocks.NewMockTicketSaleRepository(t),
		entries:       entitymocks.NewMockLotteryEntryRepository(t),
		resaleEntries: entitymocks.NewMockResaleEntryRepository(t),
		notifs:        entitymocks.NewMockNotificationRepository(t),
		pushSubs:      entitymocks.NewMockPushSubscriptionRepository(t),
		sender:        entitymocks.NewMockPushNotificationSender(t),
		workflows:     ucmocks.NewMockWorkflows(t),
	}
	f.starts = captureStarts(f.workflows)
	metrics := ucmocks.NewMockPushMetrics(t)
	f.outcomes = captureOutcomes(metrics)
	logger, logs := newCaptureLogger(t)
	f.logs = logs
	f.uc = usecase.NewNotificationUseCase(f.notifs, f.pushSubs, f.sender, f.workflows, metrics, logger,
		usecase.OrderConfirmationDeps{Now: time.Now},
		usecase.NewConcertsDeps{ArtistRepo: f.artists, ConcertRepo: f.concerts, FollowRepo: f.follows},
		f.subjects())
	return f
}

// subjects returns the fixture's ports as what Deliver and List read.
func (f *subjectFixture) subjects() usecase.SubjectDeps {
	return usecase.SubjectDeps{
		Now:           func() time.Time { return f.now() },
		Users:         f.users,
		Follows:       f.follows,
		Artists:       f.artists,
		Concerts:      f.concerts,
		Orders:        f.orders,
		TicketSales:   f.ticketSales,
		Entries:       f.entries,
		ResaleEntries: f.resaleEntries,
	}
}

// fanReadable makes the fan and their follows readable.
func (f *subjectFixture) fanReadable(user *entity.User, follows ...*entity.FollowedArtist) {
	f.users.EXPECT().Get(mock.Anything, user.ID).Return(user, nil).Maybe()
	f.follows.EXPECT().ListByUser(mock.Anything, user.ID).Return(follows, nil).Maybe()
}

// concertsReadable makes the concerts readable by id; an id of no given concert
// is left out, as Concert.ListByIDs does.
func (f *subjectFixture) concertsReadable(concerts ...*entity.Concert) {
	f.concerts.EXPECT().ListByIDs(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, ids []string) ([]*entity.Concert, error) {
			found := make([]*entity.Concert, 0, len(ids))
			for _, c := range concerts {
				if slices.Contains(ids, c.ID) {
					found = append(found, c)
				}
			}
			return found, nil
		}).Maybe()
}

// orderReadable makes the Order and its event readable.
func (f *subjectFixture) orderReadable(order *entity.Order, eventID string) {
	f.orders.EXPECT().Get(mock.Anything, order.ID).Return(order, nil).Maybe()
	f.orders.EXPECT().GetEventID(mock.Anything, order.ID).Return(eventID, nil).Maybe()
}

// lotteryEntryReadable makes the entry and the event of its Order readable.
func (f *subjectFixture) lotteryEntryReadable(entry *entity.LotteryEntry, eventID string) {
	f.entries.EXPECT().Get(mock.Anything, entry.ID).Return(entry, nil).Maybe()
	f.orders.EXPECT().GetEventID(mock.Anything, entry.OrderID).Return(eventID, nil).Maybe()
}

// resaleEntryReadable makes the entry and the event of its Order readable.
func (f *subjectFixture) resaleEntryReadable(entry *entity.ResaleEntry, eventID string) {
	f.resaleEntries.EXPECT().Get(mock.Anything, entry.ID).Return(entry, nil).Maybe()
	f.orders.EXPECT().GetEventID(mock.Anything, entry.OrderID).Return(eventID, nil).Maybe()
}

// recordsAs expects one Notification.Create, which assigns the id, and returns
// what it stored.
func (f *subjectFixture) recordsAs(id string) *entity.Notification {
	stored := &entity.Notification{}
	f.notifs.EXPECT().Create(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, n *entity.Notification) error {
			n.ID = id
			n.CreateTime = time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC)
			*stored = *n
			return nil
		}).Once()
	return stored
}

// browsers expects one listing of the fan's PushSubscriptions, one per endpoint.
func (f *subjectFixture) browsers(userID string, endpoints ...string) {
	subs := make([]*entity.PushSubscription, 0, len(endpoints))
	for _, endpoint := range endpoints {
		subs = append(subs, sub(userID, endpoint))
	}
	f.pushSubs.EXPECT().ListByUserIDs(mock.Anything, []string{userID}).Return(subs, nil).Once()
}

// push runs the real Deliver for a fan with one browser that accepts the send
// and returns the message that browser received.
func (f *subjectFixture) push(t *testing.T, req usecase.DeliverRequest) *entity.NotificationPayload {
	t.Helper()
	f.recordsAs("notif-" + req.UserID)
	f.browsers(req.UserID, "https://push/"+req.UserID)
	var raw []byte
	f.sender.EXPECT().Send(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, msg []byte, _ *entity.PushSubscription) error {
			raw = msg
			return nil
		}).Once()

	outcome, err := f.uc.Deliver(context.Background(), req)

	require.NoError(t, err)
	require.Equal(t, usecase.DeliveryStatusDelivered, outcome.Status)
	return decodeMessage(t, raw)
}

// startedRequests returns the Deliver requests of the workflows f.uc started,
// by workflow id. A second start under an id already started starts nothing
// new, as in the workflow runtime, so the first request under an id is kept.
func (f *subjectFixture) startedRequests(t *testing.T) map[string]usecase.DeliverRequest {
	t.Helper()
	started := f.starts()
	requests := make(map[string]usecase.DeliverRequest, len(started))
	for _, start := range started {
		req, ok := start.input.(usecase.DeliverRequest)
		require.True(t, ok, "workflow %s is not a deliver-notification", start.id)
		if _, ok := requests[start.id]; !ok {
			requests[start.id] = req
		}
	}
	return requests
}

// decodeMessage reads a pushed message as the Service Worker does.
func decodeMessage(t *testing.T, raw []byte) *entity.NotificationPayload {
	t.Helper()
	var msg entity.NotificationPayload
	require.NoError(t, json.Unmarshal(raw, &msg))
	return &msg
}

// renderedConcert is a concert of the first-party tour Liverty Live 2026 by
// Vaundy on 20 November 2026 at Zepp Shinjuku.
func renderedConcert(id string) *entity.Concert {
	return &entity.Concert{
		ID: id, LocalDate: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC),
		Venue:   &entity.Venue{Name: "Zepp Shinjuku"},
		Series:  &entity.Series{Title: "Liverty Live 2026", OrganizerID: new("org-1")},
		Artists: []*entity.Artist{{ID: "artist-1", Name: "Vaundy"}},
	}
}

// renderedFan is a fan in Japan with the language.
func renderedFan(id, language string) *entity.User {
	return &entity.User{ID: id, Email: id + "@example.com", PreferredLanguage: language, TimeZone: "Asia/Tokyo"}
}

// newConcertMessages runs the real NotifyNewConcerts for data and then the real
// Deliver of each notification it started, and returns the message each
// follower's browser received, by follower.
func newConcertMessages(
	t *testing.T,
	artist *entity.Artist,
	concerts []*entity.Concert,
	followers []*entity.Follower,
	data usecase.ConcertCreatedData,
) map[string]*entity.NotificationPayload {
	t.Helper()
	f := newSubjectFixture(t)
	f.artists.EXPECT().Get(mock.Anything, artist.ID).Return(artist, nil).Maybe()
	f.follows.EXPECT().ListFollowers(mock.Anything, artist.ID).Return(followers, nil).Once()
	f.concertsReadable(concerts...)
	for _, follower := range followers {
		f.fanReadable(follower.User)
	}

	require.NoError(t, f.uc.NotifyNewConcerts(context.Background(), data))

	messages := make(map[string]*entity.NotificationPayload, len(followers))
	for _, req := range f.startedRequests(t) {
		assert.Equal(t, entity.NotificationTypeNewConcerts, req.Type)
		assert.Equal(t, artist.ID, req.Subject.ArtistID)
		messages[req.UserID] = f.push(t, req)
	}
	return messages
}
