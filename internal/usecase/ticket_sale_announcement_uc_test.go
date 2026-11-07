package usecase_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// announcementTestDeps holds the mocks and use case for announcement tests.
type announcementTestDeps struct {
	userRepo    *entitymocks.MockUserRepository
	journeyRepo *entitymocks.MockTicketJourneyRepository
	publisher   *ucmocks.MockWorkflows
	uc          usecase.TicketSaleAnnouncementUseCase
}

func newAnnouncementTestDeps(t *testing.T) *announcementTestDeps {
	t.Helper()
	d := &announcementTestDeps{
		userRepo:    entitymocks.NewMockUserRepository(t),
		journeyRepo: entitymocks.NewMockTicketJourneyRepository(t),
		publisher:   ucmocks.NewMockWorkflows(t),
	}
	d.uc = usecase.NewTicketSaleAnnouncementUseCase(
		d.userRepo,
		d.journeyRepo,
		d.publisher,
		newTestLogger(t),
	)
	return d
}

// announcementRequest is the Deliver request of sale-1 for the fan, linked to
// the event.
func announcementRequest(userID, eventID string) usecase.DeliverRequest {
	return usecase.DeliverRequest{
		UserID:  userID,
		Type:    entity.NotificationTypeTicketSaleAnnouncement,
		Subject: entity.NotificationSubject{TicketSaleID: "sale-1", EventID: eventID},
	}
}

// expectAnnouncementRequested expects the deliver-notification workflow of
// sale-1 for the fan, linked to the event, named after the fan and the sale.
func expectAnnouncementRequested(publisher *ucmocks.MockWorkflows, userID, eventID string) *mock.Call {
	return publisher.EXPECT().
		Start(anyCtx, usecase.WorkflowDeliverNotification, "deliver:ticket_sale_announcement:"+userID+":sale-1", announcementRequest(userID, eventID)).
		Return(nil).
		Once()
}

// announcementJST is Asia/Tokyo for building sale times.
var announcementJST = time.FixedZone("JST", 9*60*60)

// discoveredLottery is a lottery opening on 5 October 2026 18:00 JST.
func discoveredLottery() entity.TicketSaleDiscoveredData {
	return entity.TicketSaleDiscoveredData{
		SeriesID:      "series-1",
		TicketSaleID:  "sale-1",
		Method:        int16(entity.TicketSaleMethodLottery),
		SaleStartTime: time.Date(2026, 10, 5, 18, 0, 0, 0, announcementJST),
	}
}

func trackers(userEvents ...string) []*entity.SeriesTracker {
	out := make([]*entity.SeriesTracker, 0, len(userEvents)/2)
	for i := 0; i+1 < len(userEvents); i += 2 {
		out = append(out, &entity.SeriesTracker{UserID: userEvents[i], EventID: userEvents[i+1]})
	}
	return out
}

// @spec components/usecase/ticket-sale/announce-discovered-sale "Two recipients"
// @spec components/usecase/ticket-sale/announce-discovered-sale "Tracking fan"
// @spec components/usecase/ticket-sale/announce-discovered-sale "Two fans tracking different shows"
// @spec components/usecase/ticket-sale/announce-discovered-sale "Late evening"
// AnnounceDiscoveredSale reads no clock and no quiet window: the requests are
// published as soon as it runs, whatever the recipient's local time.
func TestAnnounceDiscoveredSale_OneRequestPerRecipient(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newAnnouncementTestDeps(t)
	d.journeyRepo.EXPECT().
		ListUserIDsTrackingSeries(ctx, "series-1").
		Return(trackers("fan-a", "event-osaka", "fan-b", "event-tokyo"), nil).
		Once()
	d.userRepo.EXPECT().Get(ctx, "fan-a").Return(&entity.User{ID: "fan-a", PreferredLanguage: "ja"}, nil).Once()
	d.userRepo.EXPECT().Get(ctx, "fan-b").Return(&entity.User{ID: "fan-b", PreferredLanguage: "en"}, nil).Once()

	expectAnnouncementRequested(d.publisher, "fan-a", "event-osaka")
	expectAnnouncementRequested(d.publisher, "fan-b", "event-tokyo")

	require.NoError(t, d.uc.AnnounceDiscoveredSale(ctx, discoveredLottery()))

	// Each started request renders to a message linking to that fan's event,
	// and both replace each other by sale on a device.
	f := newSubjectFixture(t)
	f.fanReadable(renderedFan("fan-a", "ja"))
	f.fanReadable(renderedFan("fan-b", "en"))
	f.ticketSales.EXPECT().Get(mock.Anything, entity.TicketSaleID("sale-1")).
		Return(&entity.TicketSale{ID: "sale-1", Method: entity.TicketSaleMethodLottery}, nil).Twice()
	f.concertsReadable(renderedConcert("event-osaka"), renderedConcert("event-tokyo"))
	msgA := f.push(t, announcementRequest("fan-a", "event-osaka"))
	msgB := f.push(t, announcementRequest("fan-b", "event-tokyo"))
	assert.Equal(t, "/concerts/event-osaka", msgA.Data[entity.NotificationDataKeyURL])
	assert.Equal(t, "/concerts/event-tokyo", msgB.Data[entity.NotificationDataKeyURL])
	assert.Equal(t, "ticket-sale-sale-1", msgA.Tag)
	assert.Equal(t, "ticket-sale-sale-1", msgB.Tag)
}

// @spec components/usecase/ticket-sale/announce-discovered-sale "Profile unreadable"
func TestAnnounceDiscoveredSale_HydrationError_SkipsButContinues(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newAnnouncementTestDeps(t)
	d.journeyRepo.EXPECT().
		ListUserIDsTrackingSeries(ctx, "series-1").
		Return(trackers("user-broken", "event-1", "user-ja", "event-1"), nil).
		Once()
	d.userRepo.EXPECT().Get(ctx, "user-ja").Return(&entity.User{ID: "user-ja", PreferredLanguage: "ja"}, nil).Once()
	d.userRepo.EXPECT().Get(ctx, "user-broken").Return(nil, apperr.ErrInternal).Once()

	// Only the readable fan gets a request; a request for user-broken would
	// fail the mock.
	expectAnnouncementRequested(d.publisher, "user-ja", "event-1")

	require.NoError(t, d.uc.AnnounceDiscoveredSale(ctx, discoveredLottery()))
}

// @spec components/usecase/ticket-sale/announce-discovered-sale "Nobody tracking"
func TestAnnounceDiscoveredSale_EmptyAudience_NoOp(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newAnnouncementTestDeps(t)
	d.journeyRepo.EXPECT().
		ListUserIDsTrackingSeries(ctx, "series-1").
		Return(nil, nil).
		Once()
	// No user hydration or publish expected.

	require.NoError(t, d.uc.AnnounceDiscoveredSale(ctx, discoveredLottery()))
}

// @spec components/usecase/ticket-sale/announce-discovered-sale "Request without a series"
func TestAnnounceDiscoveredSale_NoSeries_NoOp(t *testing.T) {
	t.Parallel()

	d := newAnnouncementTestDeps(t)
	require.NoError(t, d.uc.AnnounceDiscoveredSale(context.Background(), entity.TicketSaleDiscoveredData{TicketSaleID: "sale-1"}))
}

// A start fails for the second recipient: the third is not started, the call
// fails, and the run again starts the second and third under the names they
// would have had, while the first's start under its existing name starts
// nothing new.
//
// @spec components/usecase/ticket-sale/announce-discovered-sale "Request fails"
func TestAnnounceDiscoveredSale_RequestFails(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	d := newAnnouncementTestDeps(t)
	d.journeyRepo.EXPECT().
		ListUserIDsTrackingSeries(ctx, "series-1").
		Return(trackers("user-1", "e", "user-2", "e", "user-3", "e"), nil).
		Twice()
	for _, uid := range []string{"user-1", "user-2", "user-3"} {
		d.userRepo.EXPECT().Get(ctx, uid).Return(&entity.User{ID: uid, PreferredLanguage: "en"}, nil).Maybe()
	}
	const failing = "deliver:ticket_sale_announcement:user-2:sale-1"
	publishErr := errors.New("start unavailable")
	failed := false
	started := map[string]bool{}
	d.publisher.EXPECT().
		Start(anyCtx, usecase.WorkflowDeliverNotification, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _, id string, _ any, _ ...usecase.StartOption) error {
			if id == failing && !failed {
				failed = true
				return publishErr
			}
			started[id] = true
			return nil
		})

	err := d.uc.AnnounceDiscoveredSale(ctx, discoveredLottery())

	require.ErrorIs(t, err, publishErr)
	assert.Equal(t, map[string]bool{"deliver:ticket_sale_announcement:user-1:sale-1": true}, started, "the third is not started")

	require.NoError(t, d.uc.AnnounceDiscoveredSale(ctx, discoveredLottery()))

	assert.Equal(t, map[string]bool{
		"deliver:ticket_sale_announcement:user-1:sale-1": true,
		"deliver:ticket_sale_announcement:user-2:sale-1": true,
		"deliver:ticket_sale_announcement:user-3:sale-1": true,
	}, started, "one name per recipient, so the first is not notified again")
}

// TestAnnouncementMessage runs the real AnnounceDiscoveredSale and the real
// Deliver of what it starts, and reads the message the browser receives.
func TestAnnouncementMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		method    entity.TicketSaleMethod
		start     time.Time
		language  string
		timeZone  string
		tour      string
		wantTitle string
		wantBody  string
	}{
		{
			// @spec components/usecase/ticket-sale/announce-discovered-sale "First-come sale in Japanese"
			name: "First-come sale in Japanese", method: entity.TicketSaleMethodFirstCome,
			start: time.Date(2026, 10, 6, 19, 0, 0, 0, announcementJST), language: "ja", timeZone: "Asia/Tokyo", tour: "星降る晩餐会",
			wantTitle: "[Vaundy] 先着販売のお知らせ",
			wantBody:  "10/6(火) 19:00から先着販売スタート!!\n星降る晩餐会",
		},
		{
			// @spec components/usecase/ticket-sale/announce-discovered-sale "Lottery in English"
			name: "Lottery in English", method: entity.TicketSaleMethodLottery,
			start: time.Date(2026, 10, 5, 18, 0, 0, 0, announcementJST), language: "fr", timeZone: "Asia/Tokyo", tour: "KICKOFF",
			wantTitle: "[Vaundy] Ticket Lottery",
			wantBody:  "Ticket lottery entry starts Oct 5 (Mon) 18:00!\nKICKOFF",
		},
		{
			name: "lottery in Japanese", method: entity.TicketSaleMethodLottery,
			start: time.Date(2026, 10, 5, 18, 0, 0, 0, announcementJST), language: "ja", timeZone: "", tour: "KICKOFF",
			wantTitle: "[Vaundy] 抽選受付のお知らせ",
			wantBody:  "10/5(月) 18:00から抽選申し込みスタート!!\nKICKOFF",
		},
		{
			name: "first-come in English, in the recipient's time zone", method: entity.TicketSaleMethodFirstCome,
			start: time.Date(2026, 10, 6, 19, 0, 0, 0, announcementJST), language: "en", timeZone: "Asia/Taipei", tour: "Tour",
			wantTitle: "[Vaundy] First-Come Ticket Sale",
			wantBody:  "Ticket sale (first come) starts Oct 6 (Tue) 18:00!\nTour",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			fan := &entity.User{ID: "fan-1", PreferredLanguage: tt.language, TimeZone: tt.timeZone}
			f := newSubjectFixture(t)
			journeys := entitymocks.NewMockTicketJourneyRepository(t)
			journeys.EXPECT().ListUserIDsTrackingSeries(ctx, "series-1").Return(trackers("fan-1", "event-1"), nil).Once()
			uc := usecase.NewTicketSaleAnnouncementUseCase(f.users, journeys, f.workflows, newTestLogger(t))
			f.fanReadable(fan)
			f.ticketSales.EXPECT().Get(mock.Anything, entity.TicketSaleID("sale-1")).
				Return(&entity.TicketSale{ID: "sale-1", Method: tt.method, SaleStartTime: tt.start}, nil).Once()
			concert := renderedConcert("event-1")
			concert.Series.Title = tt.tour
			f.concertsReadable(concert)

			require.NoError(t, uc.AnnounceDiscoveredSale(ctx, entity.TicketSaleDiscoveredData{SeriesID: "series-1", TicketSaleID: "sale-1"}))
			req, ok := f.startedRequests(t)["deliver:ticket_sale_announcement:fan-1:sale-1"]
			require.True(t, ok, "named after the recipient and the sale")
			msg := f.push(t, req)

			assert.Equal(t, tt.wantTitle, msg.Title)
			assert.Equal(t, tt.wantBody, msg.Body)
			assert.Equal(t, "/concerts/event-1", msg.Data[entity.NotificationDataKeyURL])
			assert.Equal(t, "ticket-sale-sale-1", msg.Tag)
		})
	}
}
