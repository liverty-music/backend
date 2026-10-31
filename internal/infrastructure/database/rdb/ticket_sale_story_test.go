package rdb_test

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Story tests for stories/hear-about-a-new-ticket-sale and
// stories/get-reminded-of-ticket-sale-milestones. They run the real use cases
// over the real repositories and local Postgres, from discovery or the
// reminder scan through the delivery use case, and stop at the push sender:
// a fake sender stands in for the browser and records what each registered
// browser would receive. The searcher is faked; its own behaviour is covered
// by the gemini package. Messaging is replaced by a publisher that captures
// each event so the test can hand it to the next use case, as the consumers
// do in production.

// storyPublisher captures published events in order.
type storyPublisher struct {
	mu     sync.Mutex
	events []storyEvent
}

// storyWorkflows records the workflows a story starts; a start under an id
// already started starts nothing new, as the workflow runtime does.
type storyWorkflows struct {
	mu      sync.Mutex
	pending []storyStart
	started map[string]bool
}

type storyStart struct {
	workflow string
	input    any
}

func (w *storyWorkflows) Start(_ context.Context, workflow, id string, input any, _ ...usecase.StartOption) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started == nil {
		w.started = map[string]bool{}
	}
	if w.started[id] {
		return nil
	}
	w.started[id] = true
	w.pending = append(w.pending, storyStart{workflow: workflow, input: input})
	return nil
}

func (w *storyWorkflows) Send(context.Context, string, string, any) error { return nil }

func (w *storyWorkflows) Await(context.Context, string, time.Duration, any) error { return nil }

func (w *storyWorkflows) Cancel(context.Context, string) (bool, error) { return false, nil }

// take returns the inputs of the started runs of workflow not yet taken.
func (w *storyWorkflows) take(workflow string) []any {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []any
	rest := w.pending[:0]
	for _, p := range w.pending {
		if p.workflow == workflow {
			out = append(out, p.input)
			continue
		}
		rest = append(rest, p)
	}
	w.pending = rest
	return out
}

type storyEvent struct {
	subject string
	data    any
}

func (p *storyPublisher) PublishEvent(_ context.Context, subject string, data any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, storyEvent{subject: subject, data: data})
	return nil
}

func (p *storyPublisher) take(subject string) []any {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []any
	rest := p.events[:0]
	for _, e := range p.events {
		if e.subject == subject {
			out = append(out, e.data)
			continue
		}
		rest = append(rest, e)
	}
	p.events = rest
	return out
}

// storyPush records each push a browser would receive.
type storyPush struct {
	mu    sync.Mutex
	sends []storySend
}

type storySend struct {
	userID  string
	payload entity.NotificationPayload
}

func (s *storyPush) Send(_ context.Context, payload []byte, sub *entity.PushSubscription) error {
	var p entity.NotificationPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sends = append(s.sends, storySend{userID: sub.UserID, payload: p})
	return nil
}

func (s *storyPush) to(userID string) []entity.NotificationPayload {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []entity.NotificationPayload
	for _, send := range s.sends {
		if send.userID == userID {
			out = append(out, send.payload)
		}
	}
	return out
}

type storyMetrics struct{}

func (storyMetrics) RecordPushSend(context.Context, string)                {}
func (storyMetrics) RecordDeliveryOutcome(context.Context, string, string) {}

// storySearcher returns fixed sales and records the series it was asked for.
type storySearcher struct {
	sales    []*entity.DiscoveredTicketSale
	searched [][]string
}

func (s *storySearcher) SearchPublishedSales(_ context.Context, in *entity.PublishedSaleSearchInput) ([]*entity.DiscoveredTicketSale, error) {
	ids := make([]string, 0, len(in.Series))
	for _, ref := range in.Series {
		ids = append(ids, ref.SeriesID)
	}
	s.searched = append(s.searched, ids)
	return s.sales, nil
}

// storyFan seeds a user with a language, a time zone and one registered browser.
func storyFan(t *testing.T, name, lang, tz string) string {
	t.Helper()
	ctx := context.Background()
	id := seedUser(t, name, name+"@test.com", "ext-"+name)
	_, err := testDB.Pool.Exec(ctx, `UPDATE users SET preferred_language = $2, time_zone = $3 WHERE id = $1`, id, lang, tz)
	require.NoError(t, err)
	require.NoError(t, rdb.NewPushSubscriptionRepository(testDB, testOutbox).Create(ctx, &entity.PushSubscription{
		UserID: id, Endpoint: "https://push.example/" + name, P256dh: "p256dh", Auth: "auth",
	}))
	return id
}

// storyFanWithoutBrowser seeds a user who never allowed push notifications.
func storyFanWithoutBrowser(t *testing.T, name, lang, tz string) string {
	t.Helper()
	id := seedUser(t, name, name+"@test.com", "ext-"+name)
	_, err := testDB.Pool.Exec(context.Background(), `UPDATE users SET preferred_language = $2, time_zone = $3 WHERE id = $1`, id, lang, tz)
	require.NoError(t, err)
	return id
}

func storyJourney(t *testing.T, userID, eventID string, status entity.TicketJourneyStatus) {
	t.Helper()
	require.NoError(t, rdb.NewTicketJourneyRepository(testDB, testOutbox).Upsert(context.Background(),
		&entity.TicketJourney{UserID: userID, EventID: eventID, Status: status}))
}

func storyDay(offset int) string {
	return time.Now().AddDate(0, 0, offset).Format(time.DateOnly)
}

// storyNoonZone returns an Etc/GMT zone in which now is between 11:30 and
// 12:30, so a reminder due around now is never in quiet hours.
func storyNoonZone() string {
	now := time.Now().UTC()
	offset := 12 - now.Hour()
	if now.Minute() >= 30 {
		offset--
	}
	switch {
	case offset == 0:
		return "Etc/GMT"
	case offset > 0:
		return fmt.Sprintf("Etc/GMT-%d", offset)
	default:
		return fmt.Sprintf("Etc/GMT+%d", -offset)
	}
}

type storyDeps struct {
	pub      *storyPublisher
	wf       *storyWorkflows
	push     *storyPush
	notifs   *rdb.NotificationRepository
	deliver  usecase.NotificationUseCase
	reminder usecase.TicketSaleReminderDeliveryUseCase
	logger   *logging.Logger
}

func newStoryDeps(t *testing.T) *storyDeps {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	d := &storyDeps{
		pub:    &storyPublisher{},
		wf:     &storyWorkflows{},
		push:   &storyPush{},
		notifs: rdb.NewNotificationRepository(testDB, testOutbox),
		logger: logger,
	}
	pushSubs := rdb.NewPushSubscriptionRepository(testDB, testOutbox)
	d.deliver = usecase.NewNotificationUseCase(d.notifs, pushSubs, d.push, d.wf, storyMetrics{}, logger, usecase.OrderConfirmationDeps{}, usecase.NewConcertsDeps{})
	d.reminder = usecase.NewTicketSaleReminderDeliveryUseCase(
		rdb.NewTicketSaleReminderRepository(testDB), d.notifs, pushSubs, d.push, storyMetrics{}, logger)
	return d
}

// deliverRequested runs every started deliver-notification workflow, as the
// workflow worker does.
func (d *storyDeps) deliverRequested(t *testing.T) {
	t.Helper()
	for _, input := range d.wf.take(usecase.WorkflowDeliverNotification) {
		req := input.(usecase.DeliverRequest)
		_, err := d.deliver.Deliver(context.Background(), req.UserID, req.Type, req.Payload)
		require.NoError(t, err)
	}
}

// deliverReminders hands every started deliver-reminder workflow to
// DeliverReminder, as the workflow does.
func (d *storyDeps) deliverReminders(t *testing.T) {
	t.Helper()
	for _, data := range d.wf.take(usecase.WorkflowDeliverReminder) {
		require.NoError(t, d.reminder.DeliverReminder(context.Background(), data.(entity.TicketSaleReminderDueData)))
	}
}

func (d *storyDeps) notificationsOf(t *testing.T, userID string, typ entity.NotificationType) int {
	t.Helper()
	return len(d.notificationList(t, userID, typ))
}

func (d *storyDeps) notificationList(t *testing.T, userID string, typ entity.NotificationType) []*entity.Notification {
	t.Helper()
	list, err := d.notifs.ListByUser(context.Background(), userID, 50)
	require.NoError(t, err)
	var out []*entity.Notification
	for _, nt := range list {
		if nt.Type == typ {
			out = append(out, nt)
		}
	}
	return out
}

// @spec stories/hear-about-a-new-ticket-sale "Tracking fan hears about the new phase"
// @spec stories/hear-about-a-new-ticket-sale "Tapping the push"
// @spec stories/hear-about-a-new-ticket-sale "Follower who tracks nothing"
// @spec stories/hear-about-a-new-ticket-sale "Fan who has already applied"
// @spec stories/hear-about-a-new-ticket-sale "Same phase found the next day"
// @spec stories/hear-about-a-new-ticket-sale "No registered browser"
func TestStory_HearAboutANewTicketSale(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	ctx := context.Background()
	d := newStoryDeps(t)

	artistID := seedArtist(t, "tuki.", "11112222-3333-4444-5555-666677778899")
	venueID := seedVenue(t, "story-venue")
	require.NoError(t, rdb.NewArtistRepository(testDB, testOutbox).CreateOfficialSite(ctx,
		&entity.OfficialSite{ID: entity.NewID(), ArtistID: artistID, URL: "https://tuki-official.net/"}))

	// 星降る晩餐会 is tracked; the other tour is followed but tracked by nobody.
	banquet := seedSeriesOnly(t, "星降る晩餐会")
	banquetEvent := seedEventForSeries(t, banquet, venueID, artistID, storyDay(75))
	otherTour := seedSeriesOnly(t, "秋の修学旅行")
	_ = seedEventForSeries(t, otherTour, venueID, artistID, storyDay(30))

	tracking := storyFan(t, "tracking", "ja", "Asia/Tokyo")
	follower := storyFan(t, "follower", "ja", "Asia/Tokyo")
	applied := storyFan(t, "applied", "ja", "Asia/Tokyo")
	noBrowser := storyFanWithoutBrowser(t, "nobrowser", "ja", "Asia/Tokyo")
	storyJourney(t, tracking, banquetEvent, entity.TicketJourneyStatusTracking)
	storyJourney(t, noBrowser, banquetEvent, entity.TicketJourneyStatusTracking)
	storyJourney(t, applied, banquetEvent, entity.TicketJourneyStatusApplied)

	jst := time.FixedZone("JST", 9*60*60)
	searcher := &storySearcher{sales: []*entity.DiscoveredTicketSale{{
		SeriesID:      banquet,
		Name:          "一般発売",
		Method:        entity.TicketSaleMethodFirstCome,
		SaleStartTime: time.Date(2026, 10, 6, 19, 0, 0, 0, jst),
		SaleEndTime:   time.Date(2026, 10, 20, 23, 59, 0, 0, jst),
	}}}
	discover := usecase.NewTicketSaleDiscoveryUseCase(
		rdb.NewConcertRepository(testDB, testOutbox), rdb.NewArtistRepository(testDB, testOutbox), rdb.NewTicketSaleRepository(testDB, testOutbox),
		rdb.NewTicketSaleSearchRepository(testDB), rdb.NewTicketJourneyRepository(testDB, testOutbox),
		searcher, d.pub, nil, d.logger)
	announce := usecase.NewTicketSaleAnnouncementUseCase(
		rdb.NewUserRepository(testDB, testOutbox), rdb.NewTicketJourneyRepository(testDB, testOutbox), rdb.NewSeriesRepository(testDB, testOutbox), d.wf, d.logger)

	mark := outboxMark(t)
	n, err := discover.DiscoverForArtist(ctx, &entity.Artist{ID: artistID, Name: "tuki."})
	require.NoError(t, err)
	require.Equal(t, 1, n)
	// Only the tracked tour is searched.
	assert.Equal(t, [][]string{{banquet}}, searcher.searched)

	// TicketSale.UpsertDiscovered announced the created sale; its receiver
	// runs on that announcement.
	discovered := announcedSince(t, mark, entity.SubjectTicketSaleDiscovered, func() *entityv1.TicketSale { return &entityv1.TicketSale{} },
		func(s *entityv1.TicketSale) bool { return s.GetSeriesId().GetValue() == banquet })
	require.Len(t, discovered, 1)
	for _, sale := range discovered {
		require.NoError(t, announce.AnnounceDiscoveredSale(ctx, entity.TicketSaleDiscoveredData{
			TicketSaleID:  sale.GetId().GetValue(),
			SeriesID:      sale.GetSeriesId().GetValue(),
			Method:        int16(sale.GetMethod()),
			SaleStartTime: sale.GetSaleStartTime().AsTime(),
		}))
	}
	d.deliverRequested(t)

	pushes := d.push.to(tracking)
	require.Len(t, pushes, 1, "the tracking fan's browser receives one push")
	assert.Equal(t, "チケット先着販売のお知らせ", pushes[0].Title)
	assert.Equal(t, "10/6(火) 19:00からチケットの先着販売スタート!!\n星降る晩餐会", pushes[0].Body)
	assert.Equal(t, "/concerts/"+banquetEvent, pushes[0].Data[entity.NotificationDataKeyURL],
		"tapping the push opens the tracked 20 December event")
	assert.Equal(t, 1, d.notificationsOf(t, tracking, entity.NotificationTypeTicketSaleAnnouncement))

	for _, fan := range []string{follower, applied} {
		assert.Empty(t, d.push.to(fan))
		assert.Zero(t, d.notificationsOf(t, fan, entity.NotificationTypeTicketSaleAnnouncement))
	}

	// A tracking fan without a browser gets no push, and the record is Failed.
	assert.Empty(t, d.push.to(noBrowser))
	records := d.notificationList(t, noBrowser, entity.NotificationTypeTicketSaleAnnouncement)
	require.Len(t, records, 1)
	assert.Equal(t, entity.NotificationDeliveryStatusFailed, records[0].DeliveryStatus)
	assert.Equal(t, usecase.NotificationFailureReasonNoSubscription, records[0].FailureReason)

	// The next day the same presale is found again (the search log is cleared
	// so the series is searched): it is not announced again.
	_, err = testDB.Pool.Exec(ctx, `DELETE FROM ticket_sale_searches`)
	require.NoError(t, err)
	n, err = discover.DiscoverForArtist(ctx, &entity.Artist{ID: artistID, Name: "tuki."})
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Empty(t, d.pub.take(entity.SubjectTicketSaleDiscovered))
	assert.Len(t, d.push.to(tracking), 1, "no second push about the same sale")
}

// @spec stories/get-reminded-of-ticket-sale-milestones "First-come sale about to open"
// @spec stories/get-reminded-of-ticket-sale-milestones "Lottery reminders"
// @spec stories/get-reminded-of-ticket-sale-milestones "Fan who has applied"
// @spec stories/get-reminded-of-ticket-sale-milestones "Next scan"
// @spec stories/get-reminded-of-ticket-sale-milestones "No registered browser"
// The quiet-hours scenarios "Lottery opening at 02:00" and "First-come sale
// at midnight" are covered by TestScheduledFireTime in the usecase package.
func TestStory_GetRemindedOfTicketSaleMilestones(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	ctx := context.Background()
	d := newStoryDeps(t)
	sales := rdb.NewTicketSaleRepository(testDB, testOutbox)

	artistID := seedArtist(t, "story-artist", "11112222-3333-4444-5555-666677778800")
	venueID := seedVenue(t, "story-venue")
	shadows := seedSeriesOnly(t, "SHADOWS")
	shadowsEvent := seedEventForSeries(t, shadows, venueID, artistID, storyDay(20))
	kickoff := seedSeriesOnly(t, "KICKOFF")
	kickoffEvent := seedEventForSeries(t, kickoff, venueID, artistID, storyDay(60))

	tz := storyNoonZone()
	fan := storyFan(t, "fan", "ja", tz)
	appliedFan := storyFan(t, "applied", "ja", tz)
	noBrowser := storyFanWithoutBrowser(t, "nobrowser", "ja", tz)
	storyJourney(t, fan, shadowsEvent, entity.TicketJourneyStatusTracking)
	storyJourney(t, noBrowser, shadowsEvent, entity.TicketJourneyStatusTracking)
	storyJourney(t, fan, kickoffEvent, entity.TicketJourneyStatusTracking)
	storyJourney(t, appliedFan, kickoffEvent, entity.TicketJourneyStatusApplied)

	now := time.Now()
	// A first-come sale opening in 10 minutes and a lottery that opened 10
	// minutes ago, both known since yesterday.
	_, _, err := sales.UpsertDiscovered(ctx, &entity.DiscoveredTicketSale{
		SeriesID: shadows, Name: "一般発売", Method: entity.TicketSaleMethodFirstCome,
		SaleStartTime: now.Add(10 * time.Minute), SaleEndTime: now.AddDate(0, 0, 10),
	})
	require.NoError(t, err)
	_, _, err = sales.UpsertDiscovered(ctx, &entity.DiscoveredTicketSale{
		SeriesID: kickoff, Name: "オフィシャル先行", Method: entity.TicketSaleMethodLottery,
		SaleStartTime: now.Add(-10 * time.Minute), SaleEndTime: now.AddDate(0, 0, 10),
	})
	require.NoError(t, err)
	_, err = testDB.Pool.Exec(ctx, `UPDATE ticket_sales SET discovered_at = NOW() - INTERVAL '1 day' WHERE discovered_at IS NOT NULL`)
	require.NoError(t, err)

	scan := usecase.NewTicketSaleReminderUseCase(
		sales, rdb.NewTicketSaleReminderRepository(testDB), rdb.NewTicketJourneyRepository(testDB, testOutbox),
		rdb.NewUserRepository(testDB, testOutbox), rdb.NewSeriesRepository(testDB, testOutbox), d.wf, 7*24*time.Hour, d.logger)

	requested, err := scan.ScanDueReminders(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, requested, "two for the fan, one for the fan without a browser")
	d.deliverReminders(t)

	pushes := d.push.to(fan)
	require.Len(t, pushes, 2)
	byTitle := map[string]entity.NotificationPayload{}
	for _, p := range pushes {
		byTitle[p.Title] = p
	}
	firstCome, ok := byTitle["まもなく先着販売開始"]
	require.True(t, ok, "first-come reminder arrives before the sale opens")
	assert.True(t, strings.HasSuffix(firstCome.Body, "からチケットの先着販売スタート!!\nSHADOWS"), firstCome.Body)
	assert.Equal(t, "/concerts/"+shadowsEvent, firstCome.Data[entity.NotificationDataKeyURL])
	lottery, ok := byTitle["チケット申し込み受付開始"]
	require.True(t, ok, "lottery open reminder")
	assert.True(t, strings.HasSuffix(lottery.Body, "\nKICKOFF"), lottery.Body)
	assert.Equal(t, "/concerts/"+kickoffEvent, lottery.Data[entity.NotificationDataKeyURL])
	assert.Equal(t, 2, d.notificationsOf(t, fan, entity.NotificationTypeSalesReminder))

	assert.Empty(t, d.push.to(appliedFan), "a fan who has applied gets no reminder")

	// A fan without a browser gets nothing pushed.
	assert.Empty(t, d.push.to(noBrowser))

	// The next scan sends nothing again, including to the fan without a
	// browser who registers one later.
	require.NoError(t, rdb.NewPushSubscriptionRepository(testDB, testOutbox).Create(ctx, &entity.PushSubscription{
		UserID: noBrowser, Endpoint: "https://push.example/nobrowser", P256dh: "p256dh", Auth: "auth",
	}))
	requested, err = scan.ScanDueReminders(ctx)
	require.NoError(t, err)
	assert.Zero(t, requested)
	d.deliverReminders(t)
	assert.Len(t, d.push.to(fan), 2)
	assert.Empty(t, d.push.to(noBrowser))
}

// TestStory_OrganizerSalesAreReminded runs the scan and the delivery over an
// Organizer's sales: a lottery and a first-come sale offering only the Tokyo
// event of a Published tour, and a lottery of a Cancelled tour.
//
// @spec stories/get-reminded-of-ticket-sale-milestones "Organizer's lottery reminders"
// @spec stories/get-reminded-of-ticket-sale-milestones "Organizer's first-come sale"
// @spec stories/get-reminded-of-ticket-sale-milestones "Sale for another event of the tour"
// @spec stories/get-reminded-of-ticket-sale-milestones "Series cancelled before the sale opens"
// The later milestone of "Organizer's lottery reminders" (the close reminder
// 24 hours before the end) is covered by TestScheduledFireTime in the usecase
// package.
func TestStory_OrganizerSalesAreReminded(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	ctx := context.Background()
	d := newStoryDeps(t)

	tour := seedFirstPartyEvents(t, "PUBLISHED", "PUBLISHED")
	tokyo, osaka := tour[0], tour[1]
	cancelled := seedFirstPartyEvents(t, "CANCELLED")[0]

	tz := storyNoonZone()
	tokyoFan := storyFan(t, "tokyo", "ja", tz)
	osakaFan := storyFan(t, "osaka", "ja", tz)
	cancelledFan := storyFan(t, "cancelled", "ja", tz)
	storyJourney(t, tokyoFan, tokyo, entity.TicketJourneyStatusTracking)
	storyJourney(t, osakaFan, osaka, entity.TicketJourneyStatusTracking)
	storyJourney(t, cancelledFan, cancelled, entity.TicketJourneyStatusTracking)

	now := time.Now()
	// A lottery that opened 10 minutes ago and a first-come sale opening in
	// 10 minutes, both offering the Tokyo event only, and a lottery of the
	// cancelled tour opening tomorrow.
	createSale(t, newSaleFor(t, entity.TicketSaleMethodLottery, now.Add(-10*time.Minute), now.AddDate(0, 0, 5), tokyo))
	createSale(t, newSaleFor(t, entity.TicketSaleMethodFirstCome, now.Add(10*time.Minute), now.AddDate(0, 0, 5), tokyo))
	createSale(t, newSaleFor(t, entity.TicketSaleMethodLottery, now.Add(-10*time.Minute), now.AddDate(0, 0, 5), cancelled))

	scan := usecase.NewTicketSaleReminderUseCase(
		rdb.NewTicketSaleRepository(testDB, testOutbox), rdb.NewTicketSaleReminderRepository(testDB), rdb.NewTicketJourneyRepository(testDB, testOutbox),
		rdb.NewUserRepository(testDB, testOutbox), rdb.NewSeriesRepository(testDB, testOutbox), d.wf, 7*24*time.Hour, d.logger)

	requested, err := scan.ScanDueReminders(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, requested, "the lottery open and the first-come open reminders for the Tokyo fan")
	d.deliverReminders(t)

	titles := map[string]entity.NotificationPayload{}
	for _, p := range d.push.to(tokyoFan) {
		titles[p.Title] = p
	}
	lottery, ok := titles["チケット申し込み受付開始"]
	require.True(t, ok, "the Organizer's lottery is reminded when it opens")
	assert.Equal(t, "/concerts/"+tokyo, lottery.Data[entity.NotificationDataKeyURL])
	_, ok = titles["まもなく先着販売開始"]
	assert.True(t, ok, "the Organizer's first-come sale is reminded before it opens")
	assert.Equal(t, 2, d.notificationsOf(t, tokyoFan, entity.NotificationTypeSalesReminder))

	assert.Empty(t, d.push.to(osakaFan), "no reminder for a sale that does not offer the tracked event")
	assert.Empty(t, d.push.to(cancelledFan), "no reminder for a sale of a cancelled Series")
}
