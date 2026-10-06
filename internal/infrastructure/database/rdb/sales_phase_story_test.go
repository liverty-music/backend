package rdb_test

import (
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

func (p *storyPublisher) PublishEventWithID(ctx context.Context, subject, _ string, data any) error {
	return p.PublishEvent(ctx, subject, data)
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

// storySearcher returns fixed phases and records the series it was asked for.
type storySearcher struct {
	phases   []*entity.SalesPhaseCandidate
	searched [][]string
}

func (s *storySearcher) SearchSalesPhases(_ context.Context, in *entity.SalesPhaseSearchInput) ([]*entity.SalesPhaseCandidate, error) {
	ids := make([]string, 0, len(in.Series))
	for _, ref := range in.Series {
		ids = append(ids, ref.SeriesID)
	}
	s.searched = append(s.searched, ids)
	return s.phases, nil
}

// storyFan seeds a user with a language, a time zone and one registered browser.
func storyFan(t *testing.T, name, lang, tz string) string {
	t.Helper()
	ctx := context.Background()
	id := seedUser(t, name, name+"@test.com", "ext-"+name)
	_, err := testDB.Pool.Exec(ctx, `UPDATE users SET preferred_language = $2, time_zone = $3 WHERE id = $1`, id, lang, tz)
	require.NoError(t, err)
	require.NoError(t, rdb.NewPushSubscriptionRepository(testDB).Create(ctx, &entity.PushSubscription{
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
	require.NoError(t, rdb.NewTicketJourneyRepository(testDB).Upsert(context.Background(),
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
	push     *storyPush
	notifs   *rdb.NotificationRepository
	deliver  usecase.NotificationUseCase
	reminder usecase.SalesReminderDeliveryUseCase
	logger   *logging.Logger
}

func newStoryDeps(t *testing.T) *storyDeps {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	d := &storyDeps{
		pub:    &storyPublisher{},
		push:   &storyPush{},
		notifs: rdb.NewNotificationRepository(testDB),
		logger: logger,
	}
	pushSubs := rdb.NewPushSubscriptionRepository(testDB)
	d.deliver = usecase.NewNotificationUseCase(d.notifs, pushSubs, d.push, d.pub, storyMetrics{}, logger)
	d.reminder = usecase.NewSalesReminderDeliveryUseCase(
		rdb.NewSalesPhaseReminderRepository(testDB), d.notifs, pushSubs, d.push, d.pub, storyMetrics{}, logger)
	return d
}

// deliverRequested hands every captured NOTIFICATION.requested to the
// delivery use case, as the deliver-notification consumer does.
func (d *storyDeps) deliverRequested(t *testing.T) {
	t.Helper()
	for _, data := range d.pub.take(entity.SubjectNotificationRequested) {
		req := data.(entity.NotificationRequestedData)
		_, err := d.deliver.Deliver(context.Background(), req.UserID, req.Type, req.Payload)
		require.NoError(t, err)
	}
}

// deliverReminders hands every captured SALES_PHASE.reminder.due to
// DeliverReminder, as the sales-reminder consumer does.
func (d *storyDeps) deliverReminders(t *testing.T) {
	t.Helper()
	for _, data := range d.pub.take(entity.SubjectSalesPhaseReminderDue) {
		require.NoError(t, d.reminder.DeliverReminder(context.Background(), data.(entity.SalesPhaseReminderDueData)))
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
	require.NoError(t, rdb.NewArtistRepository(testDB).CreateOfficialSite(ctx,
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
	searcher := &storySearcher{phases: []*entity.SalesPhaseCandidate{{
		SeriesID:       banquet,
		Method:         entity.SalesMethodFirstCome,
		ApplyStartTime: time.Date(2026, 10, 6, 19, 0, 0, 0, jst),
	}}}
	discover := usecase.NewSalesPhaseDiscoveryUseCase(
		rdb.NewConcertRepository(testDB), rdb.NewArtistRepository(testDB), rdb.NewSalesPhaseRepository(testDB),
		rdb.NewSalesPhaseSearchLogRepository(testDB), rdb.NewTicketJourneyRepository(testDB),
		searcher, d.pub, d.logger)
	announce := usecase.NewSalesPhaseAnnouncementUseCase(
		rdb.NewUserRepository(testDB), rdb.NewTicketJourneyRepository(testDB), rdb.NewSeriesRepository(testDB), d.pub, d.logger)

	n, err := discover.DiscoverForArtist(ctx, &entity.Artist{ID: artistID, Name: "tuki."})
	require.NoError(t, err)
	require.Equal(t, 1, n)
	// Only the tracked tour is searched.
	assert.Equal(t, [][]string{{banquet}}, searcher.searched)

	for _, data := range d.pub.take(entity.SubjectSalesPhaseDiscovered) {
		require.NoError(t, announce.AnnounceDiscoveredPhase(ctx, data.(entity.SalesPhaseDiscoveredData)))
	}
	d.deliverRequested(t)

	pushes := d.push.to(tracking)
	require.Len(t, pushes, 1, "the tracking fan's browser receives one push")
	assert.Equal(t, "チケット先着販売のお知らせ", pushes[0].Title)
	assert.Equal(t, "10/6(火) 19:00からチケットの先着販売スタート!!\n星降る晩餐会", pushes[0].Body)
	assert.Equal(t, "/concerts/"+banquetEvent, pushes[0].Data[entity.NotificationDataKeyURL],
		"tapping the push opens the tracked 20 December event")
	assert.Equal(t, 1, d.notificationsOf(t, tracking, entity.NotificationTypeSalesPhaseAnnouncement))

	for _, fan := range []string{follower, applied} {
		assert.Empty(t, d.push.to(fan))
		assert.Zero(t, d.notificationsOf(t, fan, entity.NotificationTypeSalesPhaseAnnouncement))
	}

	// A tracking fan without a browser gets no push, and the record is Failed.
	assert.Empty(t, d.push.to(noBrowser))
	records := d.notificationList(t, noBrowser, entity.NotificationTypeSalesPhaseAnnouncement)
	require.Len(t, records, 1)
	assert.Equal(t, entity.NotificationDeliveryStatusFailed, records[0].DeliveryStatus)
	assert.Equal(t, usecase.NotificationFailureReasonNoSubscription, records[0].FailureReason)

	// The next day the same presale is found again (the search log is cleared
	// so the series is searched): it is not announced again.
	_, err = testDB.Pool.Exec(ctx, `DELETE FROM sales_phase_search_logs`)
	require.NoError(t, err)
	n, err = discover.DiscoverForArtist(ctx, &entity.Artist{ID: artistID, Name: "tuki."})
	require.NoError(t, err)
	assert.Zero(t, n)
	assert.Empty(t, d.pub.take(entity.SubjectSalesPhaseDiscovered))
	assert.Len(t, d.push.to(tracking), 1, "no second push about the same phase")
}

// @spec stories/get-reminded-of-ticket-sale-milestones "First-come sale about to open"
// @spec stories/get-reminded-of-ticket-sale-milestones "Lottery reminders"
// @spec stories/get-reminded-of-ticket-sale-milestones "Fan who has applied"
// @spec stories/get-reminded-of-ticket-sale-milestones "Next scan"
// @spec stories/get-reminded-of-ticket-sale-milestones "No registered browser"
func TestStory_GetRemindedOfTicketSaleMilestones(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	ctx := context.Background()
	d := newStoryDeps(t)
	phases := rdb.NewSalesPhaseRepository(testDB)

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
	_, _, err := phases.Upsert(ctx, &entity.SalesPhaseCandidate{
		SeriesID: shadows, Method: entity.SalesMethodFirstCome, ApplyStartTime: now.Add(10 * time.Minute),
	})
	require.NoError(t, err)
	_, _, err = phases.Upsert(ctx, &entity.SalesPhaseCandidate{
		SeriesID: kickoff, Method: entity.SalesMethodLottery,
		ApplyStartTime: now.Add(-10 * time.Minute), ApplyEndTime: now.AddDate(0, 0, 10), LotteryResultTime: now.AddDate(0, 0, 20),
	})
	require.NoError(t, err)
	_, err = testDB.Pool.Exec(ctx, `UPDATE sales_phases SET discovered_at = NOW() - INTERVAL '1 day'`)
	require.NoError(t, err)

	scan := usecase.NewSalesReminderUseCase(
		phases, rdb.NewSalesPhaseReminderRepository(testDB), rdb.NewTicketJourneyRepository(testDB),
		rdb.NewUserRepository(testDB), rdb.NewSeriesRepository(testDB), d.pub, 7*24*time.Hour, d.logger)

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
	require.NoError(t, rdb.NewPushSubscriptionRepository(testDB).Create(ctx, &entity.PushSubscription{
		UserID: noBrowser, Endpoint: "https://push.example/nobrowser", P256dh: "p256dh", Auth: "auth",
	}))
	requested, err = scan.ScanDueReminders(ctx)
	require.NoError(t, err)
	assert.Zero(t, requested)
	d.deliverReminders(t)
	assert.Len(t, d.push.to(fan), 2)
	assert.Empty(t, d.push.to(noBrowser))
}
