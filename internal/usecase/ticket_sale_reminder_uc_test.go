package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// jst is Japan Standard Time, used throughout the tests.
var jst = func() *time.Location {
	loc, _ := time.LoadLocation("Asia/Tokyo")
	return loc
}()

// la is America/Los_Angeles, a fan time zone other than Japan time.
var la = func() *time.Location {
	loc, _ := time.LoadLocation("America/Los_Angeles")
	return loc
}()

// saleFarPast is a DiscoveredTime well in the past so it never trips the first-sight guard.
var saleFarPast = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

// organizerLottery returns an Organizer's lottery of series-org offering the
// Tokyo event only.
func organizerLottery(start, end time.Time) *entity.TicketSale {
	return &entity.TicketSale{
		ID: "sale-org", SeriesID: "series-org", Name: "FC先行", Method: entity.TicketSaleMethodLottery,
		SaleStartTime: start, SaleEndTime: end,
		EventIDs: []string{"event-tokyo"},
	}
}

func jstAt(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, jst)
}

// ---- scheduledFireTime ----

func TestScheduledFireTime(t *testing.T) {
	t.Parallel()

	// kickoff is a lottery opening 5 Oct 18:00 and closing 22 Oct 23:59 (JST).
	kickoff := &entity.TicketSale{
		Method:         entity.TicketSaleMethodLottery,
		SaleStartTime:  jstAt(2026, 10, 5, 18, 0),
		SaleEndTime:    jstAt(2026, 10, 22, 23, 59),
		DiscoveredTime: new(saleFarPast),
	}
	// firstCome opens 6 Oct 19:00 JST and closes 10 Oct 23:59.
	firstCome := &entity.TicketSale{
		Method:         entity.TicketSaleMethodFirstCome,
		SaleStartTime:  jstAt(2026, 10, 6, 19, 0),
		SaleEndTime:    jstAt(2026, 10, 10, 23, 59),
		DiscoveredTime: new(saleFarPast),
	}

	tests := []struct {
		name       string
		stage      entity.ReminderStage
		sale       *entity.TicketSale
		tz         *time.Location
		wantTime   time.Time
		wantExpiry time.Time
		wantOK     bool
	}{
		// @spec components/entity/ticket-sale-reminder "Lottery stages"
		// @spec stories/get-reminded-of-ticket-sale-milestones "Lottery reminders"
		{
			name: "Lottery stages: APPLY_OPEN at the apply start", stage: entity.ReminderStageApplyOpen, sale: kickoff, tz: jst,
			wantTime: jstAt(2026, 10, 5, 18, 0), wantExpiry: jstAt(2026, 10, 22, 23, 59), wantOK: true,
		},
		{
			// The 21 Oct 23:59 anchor falls in quiet hours and moves to 08:00 on the closing day.
			name: "Lottery stages: APPLY_CLOSE_24H anchored 24h before the close", stage: entity.ReminderStageApplyClose24H, sale: kickoff, tz: jst,
			wantTime: jstAt(2026, 10, 22, 8, 0), wantExpiry: jstAt(2026, 10, 22, 23, 59), wantOK: true,
		},

		// @spec components/entity/ticket-sale-reminder "First-come stage"
		// @spec components/usecase/ticket-sale/scan-due-reminders "First-come sale about to open"
		{
			name: "First-come stage: APPLY_OPEN 30 minutes before the start", stage: entity.ReminderStageApplyOpen, sale: firstCome, tz: jst,
			wantTime: jstAt(2026, 10, 6, 18, 30), wantExpiry: jstAt(2026, 10, 6, 19, 0), wantOK: true,
		},
		{
			name: "First-come stage: no APPLY_CLOSE_24H", stage: entity.ReminderStageApplyClose24H,
			sale: &entity.TicketSale{Method: entity.TicketSaleMethodFirstCome, SaleStartTime: jstAt(2026, 10, 6, 19, 0),
				SaleEndTime: jstAt(2026, 10, 25, 23, 59), DiscoveredTime: new(saleFarPast)},
			tz: jst, wantOK: false,
		},

		{
			name: "undefined stage value 3 does not apply", stage: entity.ReminderStage(3), sale: kickoff, tz: jst, wantOK: false,
		},

		// @spec components/usecase/ticket-sale/scan-due-reminders "First-come sale at midnight"
		// @spec stories/get-reminded-of-ticket-sale-milestones "First-come sale at midnight"
		{
			name: "First-come sale at midnight: 23:30 anchor moves to 21:00 that evening", stage: entity.ReminderStageApplyOpen,
			sale: &entity.TicketSale{Method: entity.TicketSaleMethodFirstCome, SaleStartTime: jstAt(2026, 10, 7, 0, 0), DiscoveredTime: new(saleFarPast)},
			tz:   jst, wantTime: jstAt(2026, 10, 6, 21, 0), wantExpiry: jstAt(2026, 10, 7, 0, 0), wantOK: true,
		},
		{
			name: "first-come sale at 05:00: 04:30 anchor moves to 21:00 the previous evening", stage: entity.ReminderStageApplyOpen,
			sale: &entity.TicketSale{Method: entity.TicketSaleMethodFirstCome, SaleStartTime: jstAt(2026, 10, 7, 5, 0), DiscoveredTime: new(saleFarPast)},
			tz:   jst, wantTime: jstAt(2026, 10, 6, 21, 0), wantExpiry: jstAt(2026, 10, 7, 5, 0), wantOK: true,
		},
		{
			name: "first-come sale at 08:30: 08:00 anchor is outside quiet hours", stage: entity.ReminderStageApplyOpen,
			sale: &entity.TicketSale{Method: entity.TicketSaleMethodFirstCome, SaleStartTime: jstAt(2026, 10, 7, 8, 30), DiscoveredTime: new(saleFarPast)},
			tz:   jst, wantTime: jstAt(2026, 10, 7, 8, 0), wantExpiry: jstAt(2026, 10, 7, 8, 30), wantOK: true,
		},

		// @spec components/usecase/ticket-sale/scan-due-reminders "Lottery opening during the night"
		// @spec stories/get-reminded-of-ticket-sale-milestones "Lottery opening at 02:00"
		{
			name: "Lottery opening during the night: 02:00 open moves to 08:00", stage: entity.ReminderStageApplyOpen,
			sale: &entity.TicketSale{Method: entity.TicketSaleMethodLottery, SaleStartTime: jstAt(2026, 10, 7, 2, 0),
				SaleEndTime: jstAt(2026, 10, 14, 23, 59), DiscoveredTime: new(saleFarPast)},
			tz: jst, wantTime: jstAt(2026, 10, 7, 8, 0), wantExpiry: jstAt(2026, 10, 14, 23, 59), wantOK: true,
		},

		// @spec components/usecase/ticket-sale/scan-due-reminders "Close reminder at night"
		{
			name: "Close reminder at night: 23:59 close fires 08:00 on the closing day", stage: entity.ReminderStageApplyClose24H,
			sale: &entity.TicketSale{Method: entity.TicketSaleMethodLottery, SaleStartTime: jstAt(2026, 10, 5, 18, 0),
				SaleEndTime: jstAt(2026, 10, 22, 23, 59), DiscoveredTime: new(saleFarPast)},
			tz: jst, wantTime: jstAt(2026, 10, 22, 8, 0), wantExpiry: jstAt(2026, 10, 22, 23, 59), wantOK: true,
		},
		{
			name: "close at 12:00 fires at the 12:00 anchor the day before", stage: entity.ReminderStageApplyClose24H,
			sale: &entity.TicketSale{Method: entity.TicketSaleMethodLottery, SaleStartTime: jstAt(2026, 10, 5, 18, 0),
				SaleEndTime: jstAt(2026, 10, 22, 12, 0), DiscoveredTime: new(saleFarPast)},
			tz: jst, wantTime: jstAt(2026, 10, 21, 12, 0), wantExpiry: jstAt(2026, 10, 22, 12, 0), wantOK: true,
		},

		// @spec components/usecase/ticket-sale/scan-due-reminders "Milestone already past when the sale was discovered"
		{
			name: "Milestone already past when the sale was discovered", stage: entity.ReminderStageApplyOpen,
			sale: &entity.TicketSale{Method: entity.TicketSaleMethodLottery, SaleStartTime: jstAt(2026, 10, 5, 10, 0),
				SaleEndTime: jstAt(2026, 10, 12, 23, 59), DiscoveredTime: new(jstAt(2026, 10, 5, 12, 0))},
			tz: jst, wantOK: false,
		},
		{
			name: "first-sight guard: anchor equal to discovery fires", stage: entity.ReminderStageApplyOpen,
			sale: &entity.TicketSale{Method: entity.TicketSaleMethodLottery, SaleStartTime: jstAt(2026, 10, 5, 10, 0),
				SaleEndTime: jstAt(2026, 10, 12, 23, 59), DiscoveredTime: new(jstAt(2026, 10, 5, 10, 0))},
			tz: jst, wantTime: jstAt(2026, 10, 5, 10, 0), wantExpiry: jstAt(2026, 10, 12, 23, 59), wantOK: true,
		},
		{
			name: "first-come sale discovered at 21:00 the evening before a midnight opening still fires", stage: entity.ReminderStageApplyOpen,
			sale: &entity.TicketSale{Method: entity.TicketSaleMethodFirstCome, SaleStartTime: jstAt(2026, 10, 7, 0, 0),
				DiscoveredTime: new(jstAt(2026, 10, 6, 21, 3))},
			tz: jst, wantTime: jstAt(2026, 10, 6, 21, 0), wantExpiry: jstAt(2026, 10, 7, 0, 0), wantOK: true,
		},

		// @spec components/usecase/ticket-sale/scan-due-reminders "Organizer's sale created after it opened"
		{
			// No first-sight guard: an Organizer's sale has no discovered time.
			name: "Organizer's sale created after it opened: APPLY_OPEN still applies", stage: entity.ReminderStageApplyOpen,
			sale: organizerLottery(jstAt(2026, 10, 5, 10, 0), jstAt(2026, 10, 10, 10, 0)),
			tz:   jst, wantTime: jstAt(2026, 10, 5, 10, 0), wantExpiry: jstAt(2026, 10, 10, 10, 0), wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotTime, gotExpiry, gotOK := usecase.ExportedScheduledFireTime(tt.stage, tt.sale, tt.tz)
			require.Equal(t, tt.wantOK, gotOK)
			if !tt.wantOK {
				return
			}
			assert.True(t, tt.wantTime.Equal(gotTime), "fire: got %s, want %s", gotTime.In(tt.tz), tt.wantTime.In(tt.tz))
			assert.True(t, tt.wantExpiry.Equal(gotExpiry), "expiry: got %s, want %s", gotExpiry.In(tt.tz), tt.wantExpiry.In(tt.tz))
		})
	}
}

// ---- A reminder's message ----

// TestReminderMessage runs the real DeliverReminder for a due reminder and reads
// the message the browser receives.
func TestReminderMessage(t *testing.T) {
	t.Parallel()

	const kickoffTitle = "King Gnu 10th Anniversary Opening Live “KICKOFF”"
	lottery := &entity.TicketSale{
		ID:            "sale-1",
		Method:        entity.TicketSaleMethodLottery,
		SaleStartTime: jstAt(2026, 10, 5, 18, 0),
		SaleEndTime:   jstAt(2026, 10, 22, 23, 59),
	}
	firstCome := &entity.TicketSale{
		ID:            "sale-1",
		Method:        entity.TicketSaleMethodFirstCome,
		SaleStartTime: jstAt(2026, 10, 6, 19, 0),
	}
	ja := func() *entity.User {
		return &entity.User{ID: "user-001", PreferredLanguage: "ja", TimeZone: "Asia/Tokyo"}
	}
	en := func() *entity.User {
		return &entity.User{ID: "user-001", PreferredLanguage: "en", TimeZone: "Asia/Tokyo"}
	}

	tests := []struct {
		name      string
		sale      *entity.TicketSale
		stage     entity.ReminderStage
		user      *entity.User
		wantTitle string
		wantBody  string
	}{
		{
			// @spec components/usecase/ticket-sale/scan-due-reminders "Lottery closing in Japanese"
			// @spec components/usecase/ticket-sale/scan-due-reminders "Deferred close reminder keeps the absolute deadline"
			name: "Lottery closing in Japanese", sale: lottery, stage: entity.ReminderStageApplyClose24H, user: ja(),
			wantTitle: "[King Gnu] 抽選締切が近づいています",
			wantBody:  "抽選申し込みの締切は10/22(木) 23:59!!\n" + kickoffTitle,
		},
		{
			name: "lottery closing in English", sale: lottery, stage: entity.ReminderStageApplyClose24H, user: en(),
			wantTitle: "[King Gnu] Ticket Lottery Closing Soon",
			wantBody:  "Ticket lottery entry closes Oct 22 (Thu) 23:59!\n" + kickoffTitle,
		},
		{
			name: "lottery opens in Japanese", sale: lottery, stage: entity.ReminderStageApplyOpen, user: ja(),
			wantTitle: "[King Gnu] 抽選受付スタート",
			wantBody:  "抽選申し込みがスタートしました!! 締切は10/22(木) 23:59\n" + kickoffTitle,
		},
		{
			name: "lottery opens in English", sale: lottery, stage: entity.ReminderStageApplyOpen, user: en(),
			wantTitle: "[King Gnu] Ticket Lottery Open",
			wantBody:  "Ticket lottery entry is open! Closes Oct 22 (Thu) 23:59.\n" + kickoffTitle,
		},
		{
			name: "first-come sale in Japanese", sale: firstCome, stage: entity.ReminderStageApplyOpen, user: ja(),
			wantTitle: "[King Gnu] まもなく先着販売開始",
			wantBody:  "10/6(火) 19:00から先着販売スタート!!\n" + kickoffTitle,
		},
		{
			name: "first-come sale, in a language without copy", sale: firstCome, stage: entity.ReminderStageApplyOpen,
			user:      &entity.User{ID: "user-001", PreferredLanguage: "fr", TimeZone: "Asia/Tokyo"},
			wantTitle: "[King Gnu] First-Come Sale Starting Soon",
			wantBody:  "Ticket sale (first come) starts Oct 6 (Tue) 19:00!\n" + kickoffTitle,
		},
		{
			name: "times are in the fan's time zone", sale: firstCome, stage: entity.ReminderStageApplyOpen,
			user:      &entity.User{ID: "user-001", PreferredLanguage: "en", TimeZone: "Asia/Taipei"},
			wantTitle: "[King Gnu] First-Come Sale Starting Soon",
			wantBody:  "Ticket sale (first come) starts Oct 6 (Tue) 18:00!\n" + kickoffTitle,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := newReminderDeliveryTestDeps(t)
			d.reminderRepo.EXPECT().AlreadySent(anyCtx, "user-001", tt.sale.ID, tt.stage).Return(false, nil).Once()
			d.fanReadable(tt.user)
			d.ticketSales.EXPECT().Get(mock.Anything, tt.sale.ID).Return(tt.sale, nil).Once()
			concert := renderedConcert("event-nov1")
			concert.Series.Title = kickoffTitle
			concert.Artists = []*entity.Artist{{ID: "artist-kg", Name: "King Gnu"}}
			d.concertsReadable(concert)
			d.recordsAs("notif-1")
			d.browsers("user-001", "https://push/1")
			pushed := d.capturePushes(1)
			d.reminderRepo.EXPECT().RecordSent(anyCtx, "user-001", tt.sale.ID, tt.stage).Return(nil).Once()

			require.NoError(t, d.reminder.DeliverReminder(context.Background(), entity.TicketSaleReminderDueData{
				UserID: "user-001", TicketSaleID: string(tt.sale.ID), Stage: int16(tt.stage), LinkEventID: "event-nov1",
			}))

			require.Len(t, *pushed, 1)
			msg := decodeMessage(t, (*pushed)[0].raw)
			assert.Equal(t, tt.wantTitle, msg.Title)
			assert.Equal(t, tt.wantBody, msg.Body)
			assert.NotContains(t, msg.Body, "hour", "the text states absolute times only")
			assert.NotContains(t, msg.Body, "時間", "the text states absolute times only")
			assert.Equal(t, "/concerts/event-nov1", msg.Data[entity.NotificationDataKeyURL])
			assert.Equal(t, fmt.Sprintf("ticket-sale-%s-stage-%d", tt.sale.ID, tt.stage), msg.Tag)
		})
	}
}

// TestReminderMessage_LinksToTheTrackedEvent starts from the real scan: the
// event the audience lists for the fan is the event the pushed message opens.
//
// @spec components/usecase/ticket-sale/scan-due-reminders "Link to the tracked event"
func TestReminderMessage_LinksToTheTrackedEvent(t *testing.T) {
	t.Parallel()

	now := time.Now()
	sale := &entity.TicketSale{
		ID: deliverySaleID, SeriesID: "series-1", Method: entity.TicketSaleMethodLottery,
		SaleStartTime:  now.Add(-10 * time.Minute),
		SaleEndTime:    now.AddDate(0, 0, 10),
		DiscoveredTime: new(now.AddDate(0, 0, -3)),
	}
	user := &entity.User{ID: "user-001", PreferredLanguage: "ja", TimeZone: noonZone(now)}
	scan := newScanDeps(t, sale)
	scan.audience(sale, user, "event-nov1", nil)
	var due entity.TicketSaleReminderDueData
	scan.pub.EXPECT().Start(mock.Anything, usecase.WorkflowDeliverReminder, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _, _ string, input any, _ ...usecase.StartOption) error {
			due = input.(entity.TicketSaleReminderDueData)
			return nil
		}).Once()

	published, err := scan.uc.ScanDueReminders(context.Background())
	require.NoError(t, err)
	require.Equal(t, 1, published)

	d := newReminderDeliveryTestDeps(t)
	d.reminderRepo.EXPECT().AlreadySent(anyCtx, "user-001", deliverySaleID, entity.ReminderStageApplyOpen).Return(false, nil).Once()
	d.fanReadable(user)
	d.ticketSales.EXPECT().Get(mock.Anything, deliverySaleID).Return(sale, nil).Once()
	d.concertsReadable(renderedConcert("event-nov1"))
	d.recordsAs("notif-1")
	d.browsers("user-001", "https://push/1")
	pushed := d.capturePushes(1)
	d.reminderRepo.EXPECT().RecordSent(anyCtx, "user-001", deliverySaleID, entity.ReminderStageApplyOpen).Return(nil).Once()

	require.NoError(t, d.reminder.DeliverReminder(context.Background(), due))

	require.Len(t, *pushed, 1)
	assert.Equal(t, "/concerts/event-nov1", decodeMessage(t, (*pushed)[0].raw).Data[entity.NotificationDataKeyURL])
}

// ---- ScanDueReminders ----

// noonZone returns an Etc/GMT zone in which now is between 11:30 and 12:30,
// so a reminder due within ±30 minutes of now is never in quiet hours.
func noonZone(now time.Time) string {
	offset := 12 - now.UTC().Hour()
	if now.UTC().Minute() >= 30 {
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

// scanDeps holds the mocks and use case for ScanDueReminders tests.
type scanDeps struct {
	ticketSaleRepo *entitymocks.MockTicketSaleRepository
	reminderRepo   *entitymocks.MockTicketSaleReminderRepository
	journeyRepo    *entitymocks.MockTicketJourneyRepository
	userRepo       *entitymocks.MockUserRepository
	pub            *ucmocks.MockWorkflows
	uc             usecase.TicketSaleReminderUseCase
}

const scanLookahead = 7 * 24 * time.Hour

func newScanDeps(t *testing.T, sales ...*entity.TicketSale) *scanDeps {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	d := &scanDeps{
		ticketSaleRepo: entitymocks.NewMockTicketSaleRepository(t),
		reminderRepo:   entitymocks.NewMockTicketSaleReminderRepository(t),
		journeyRepo:    entitymocks.NewMockTicketJourneyRepository(t),
		userRepo:       entitymocks.NewMockUserRepository(t),
		pub:            ucmocks.NewMockWorkflows(t),
	}
	d.ticketSaleRepo.EXPECT().
		ListWithPendingMilestones(mock.Anything, scanLookahead, usecase.ReminderScanLookbackMargin).
		Return(sales, nil).Once()
	d.uc = usecase.NewTicketSaleReminderUseCase(
		d.ticketSaleRepo, d.reminderRepo, d.journeyRepo, d.userRepo,
		d.pub, scanLookahead, logger,
	)
	return d
}

// audience registers one tracking fan of a discovered sale's series, with no
// stage sent yet unless sent is given.
func (d *scanDeps) audience(sale *entity.TicketSale, user *entity.User, eventID string, sent map[entity.ReminderStage]bool) {
	d.journeyRepo.EXPECT().ListUserIDsTrackingSeries(mock.Anything, sale.SeriesID).
		Return([]*entity.SeriesTracker{{UserID: user.ID, EventID: eventID}}, nil).Once()
	d.recipient(sale, user, sent)
}

// recipient registers the profile and sent stages of one fan of the sale.
func (d *scanDeps) recipient(sale *entity.TicketSale, user *entity.User, sent map[entity.ReminderStage]bool) {
	d.userRepo.EXPECT().Get(mock.Anything, user.ID).Return(user, nil).Once()
	sentSet := map[string]map[entity.ReminderStage]bool{}
	if sent != nil {
		sentSet[user.ID] = sent
	}
	d.reminderRepo.EXPECT().ListSentStages(mock.Anything, sale.ID, []string{user.ID}).Return(sentSet, nil).Once()
}

// expectReminder expects one deliver-reminder workflow start for the user, sale
// and stage, linked to the event. Its input is compared whole: the scan renders
// nothing.
func (d *scanDeps) expectReminder(userID, saleID string, stage entity.ReminderStage, linkEventID string) {
	d.pub.EXPECT().Start(mock.Anything, usecase.WorkflowDeliverReminder, mock.Anything,
		entity.TicketSaleReminderDueData{UserID: userID, TicketSaleID: saleID, Stage: int16(stage), LinkEventID: linkEventID},
	).Return(nil).Once()
}

// @spec components/usecase/ticket-sale/scan-due-reminders "First-come sale about to open"
// @spec components/usecase/ticket-sale/scan-due-reminders "Tracking fan"
// @spec stories/get-reminded-of-ticket-sale-milestones "First-come sale about to open"
func TestScanDueReminders_FirstComeAboutToOpen(t *testing.T) {
	t.Parallel()

	now := time.Now()
	sale := &entity.TicketSale{
		ID: "sale-fc", SeriesID: "series-fc", Method: entity.TicketSaleMethodFirstCome,
		SaleStartTime:  now.Add(20 * time.Minute), // anchor 10 minutes ago
		DiscoveredTime: new(now.AddDate(0, 0, -3)),
	}
	user := &entity.User{ID: "fan-1", PreferredLanguage: "ja", TimeZone: noonZone(now)}

	d := newScanDeps(t, sale)
	d.audience(sale, user, "event-osaka", nil)
	d.expectReminder("fan-1", "sale-fc", entity.ReminderStageApplyOpen, "event-osaka")

	published, err := d.uc.ScanDueReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, published)
}

// @spec components/usecase/ticket-sale/scan-due-reminders "First-come sale already open"
func TestScanDueReminders_FirstComeAlreadyOpen(t *testing.T) {
	t.Parallel()

	now := time.Now()
	sale := &entity.TicketSale{
		ID: "sale-fc", SeriesID: "series-fc", Method: entity.TicketSaleMethodFirstCome,
		SaleStartTime:  now.Add(-5 * time.Minute), // opened 5 minutes ago
		DiscoveredTime: new(now.AddDate(0, 0, -3)),
	}
	user := &entity.User{ID: "fan-1", PreferredLanguage: "ja", TimeZone: noonZone(now)}

	d := newScanDeps(t, sale)
	d.audience(sale, user, "event-1", nil)
	// No publish is expected: the stage expired when the sale opened.

	published, err := d.uc.ScanDueReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, published)
}

// @spec components/usecase/ticket-sale/scan-due-reminders "Lottery opens"
// @spec stories/get-reminded-of-ticket-sale-milestones "Lottery reminders"
func TestScanDueReminders_LotteryOpens(t *testing.T) {
	t.Parallel()

	now := time.Now()
	sale := &entity.TicketSale{
		ID: "sale-lot", SeriesID: "series-lot", Method: entity.TicketSaleMethodLottery,
		SaleStartTime:  now.Add(-10 * time.Minute),
		SaleEndTime:    now.AddDate(0, 0, 10),
		DiscoveredTime: new(now.AddDate(0, 0, -3)),
	}
	user := &entity.User{ID: "fan-1", PreferredLanguage: "en", TimeZone: noonZone(now)}

	d := newScanDeps(t, sale)
	d.audience(sale, user, "event-1", nil)
	d.expectReminder("fan-1", "sale-lot", entity.ReminderStageApplyOpen, "event-1")

	published, err := d.uc.ScanDueReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, published)
}

// @spec components/usecase/ticket-sale/scan-due-reminders "Already sent"
func TestScanDueReminders_AlreadySentStageIsNotRepublished(t *testing.T) {
	t.Parallel()

	now := time.Now()
	sale := &entity.TicketSale{
		ID: "sale-sent", SeriesID: "series-sent", Method: entity.TicketSaleMethodLottery,
		SaleStartTime:  now.Add(-10 * time.Minute),
		SaleEndTime:    now.AddDate(0, 0, 10),
		DiscoveredTime: new(now.AddDate(0, 0, -3)),
	}
	user := &entity.User{ID: "fan-1", PreferredLanguage: "en", TimeZone: noonZone(now)}

	d := newScanDeps(t, sale)
	d.audience(sale, user, "event-1", map[entity.ReminderStage]bool{entity.ReminderStageApplyOpen: true})
	// No publish is expected: APPLY_OPEN is already recorded as sent.

	published, err := d.uc.ScanDueReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, published)
}

// TestScanDueReminders_ExpiredStagesAreSkipped proves that no stage fires
// once its moment has passed.
//
// @spec components/usecase/ticket-sale/scan-due-reminders "Application window closed before the open reminder was sent"
func TestScanDueReminders_ExpiredStagesAreSkipped(t *testing.T) {
	t.Parallel()

	now := time.Now()
	sale := &entity.TicketSale{
		ID: "sale-expired", SeriesID: "series-expired", Method: entity.TicketSaleMethodLottery,
		SaleStartTime:  now.AddDate(0, 0, -30),
		SaleEndTime:    now.AddDate(0, 0, -20),
		DiscoveredTime: new(now.AddDate(0, 0, -60)),
	}
	user := &entity.User{ID: "fan-1", PreferredLanguage: "en", TimeZone: "Asia/Tokyo"}

	d := newScanDeps(t, sale)
	d.audience(sale, user, "event-1", nil)
	// No publish expectation: the mock fails the test on any stale reminder.

	published, err := d.uc.ScanDueReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, published)
}

// @spec components/usecase/ticket-sale/scan-due-reminders "Fan no longer listed"
func TestScanDueReminders_FanNoLongerListed(t *testing.T) {
	t.Parallel()

	now := time.Now()
	sale := &entity.TicketSale{
		ID: "sale-rd", SeriesID: "series-rd", Method: entity.TicketSaleMethodLottery,
		SaleStartTime:  now.AddDate(0, 0, -5),
		SaleEndTime:    now.Add(12 * time.Hour),
		DiscoveredTime: new(now.AddDate(0, 0, -30)),
	}

	d := newScanDeps(t, sale)
	// The fan who tracked earlier is no longer returned when the closing
	// reminder becomes due.
	d.journeyRepo.EXPECT().ListUserIDsTrackingSeries(mock.Anything, "series-rd").Return(nil, nil).Once()

	published, err := d.uc.ScanDueReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, published)
}

// @spec components/usecase/ticket-sale/scan-due-reminders "Sale opening in 3 days"
// @spec components/usecase/ticket-sale/scan-due-reminders "One sale fails"
func TestScanDueReminders_EvaluatesEachListedSale(t *testing.T) {
	t.Parallel()

	now := time.Now()
	broken := &entity.TicketSale{
		ID: "sale-broken", SeriesID: "series-broken", Method: entity.TicketSaleMethodLottery,
		SaleStartTime: now.Add(-10 * time.Minute), SaleEndTime: now.AddDate(0, 0, 10),
		DiscoveredTime: new(now.AddDate(0, 0, -3)),
	}
	inThreeDays := &entity.TicketSale{
		ID: "sale-3d", SeriesID: "series-3d", Method: entity.TicketSaleMethodLottery,
		SaleStartTime: now.AddDate(0, 0, 3), SaleEndTime: now.AddDate(0, 0, 10),
		DiscoveredTime: new(now.AddDate(0, 0, -1)),
	}
	open := &entity.TicketSale{
		ID: "sale-open", SeriesID: "series-open", Method: entity.TicketSaleMethodLottery,
		SaleStartTime: now.Add(-10 * time.Minute), SaleEndTime: now.AddDate(0, 0, 10),
		DiscoveredTime: new(now.AddDate(0, 0, -3)),
	}
	user := &entity.User{ID: "fan-1", PreferredLanguage: "en", TimeZone: noonZone(now)}

	d := newScanDeps(t, broken, inThreeDays, open)
	d.journeyRepo.EXPECT().ListUserIDsTrackingSeries(mock.Anything, "series-broken").
		Return(nil, errors.New("db down")).Once()
	// The sale opening in 3 days is evaluated (its audience is read) but has
	// nothing due yet.
	d.audience(inThreeDays, user, "event-3d", nil)
	d.audience(open, user, "event-open", nil)
	d.expectReminder("fan-1", "sale-open", entity.ReminderStageApplyOpen, "event-open")

	published, err := d.uc.ScanDueReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, published)
}

// ---- userTimezone ----

// TestUserTimezone covers the fallback used for the fan's quiet-hours window
// and due-time calculations: an unset or unrecognised time zone falls back
// to Asia/Tokyo.
//
// @spec components/usecase/ticket-sale/scan-due-reminders "Time zone fallback"
func TestUserTimezone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		user *entity.User
		want *time.Location
	}{
		{
			name: "valid IANA time zone is used as-is",
			user: &entity.User{TimeZone: "America/Los_Angeles"},
			want: la,
		},
		{
			name: "unset time zone falls back to Asia/Tokyo",
			user: &entity.User{TimeZone: ""},
			want: jst,
		},
		{
			name: "unrecognised time zone falls back to Asia/Tokyo",
			user: &entity.User{TimeZone: "Not/AZone"},
			want: jst,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := usecase.ExportedUserTimezone(tt.user)
			assert.Equal(t, tt.want.String(), got.String())
		})
	}
}

// TestScanDueReminders_OrganizerSale shows that an Organizer's sale is
// evaluated like a discovered one, reaching only the fans tracking an event it
// offers.
//
// @spec components/usecase/ticket-sale/scan-due-reminders "Organizer's sale is evaluated"
// @spec components/usecase/ticket-sale/scan-due-reminders "Organizer's sale for some events of the tour"
// @spec components/usecase/ticket-sale/scan-due-reminders "Reminder requested"
func TestScanDueReminders_OrganizerSale(t *testing.T) {
	t.Parallel()

	now := time.Now()
	sale := organizerLottery(now.Add(-10*time.Minute), now.AddDate(0, 0, 5))
	fanA := &entity.User{ID: "fan-a", PreferredLanguage: "ja", TimeZone: noonZone(now)}

	d := newScanDeps(t, sale)
	// The audience is read over the offered events, not the series: fan B,
	// who tracks only the Osaka event, is not returned.
	d.journeyRepo.EXPECT().ListUserIDsTrackingEvents(mock.Anything, []string{"event-tokyo"}).
		Return([]*entity.SeriesTracker{{UserID: "fan-a", EventID: "event-tokyo"}}, nil).Once()
	d.recipient(sale, fanA, nil)
	d.expectReminder("fan-a", "sale-org", entity.ReminderStageApplyOpen, "event-tokyo")

	published, err := d.uc.ScanDueReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, published)
}

// TestReminderWorkflowID shows how the deliver-reminder starts are named: a
// run repeated within the same 15-minute period names the same workflow, so
// the reminder runs once, and the next period's run names a new one, so a
// reminder not yet recorded as sent runs again.
func TestReminderWorkflowID(t *testing.T) {
	t.Parallel()

	data := entity.TicketSaleReminderDueData{UserID: "fan-a", TicketSaleID: "sale-1", Stage: int16(entity.ReminderStageApplyOpen)}
	at := func(hh, mm int) time.Time { return time.Date(2026, 10, 22, hh, mm, 0, 0, time.UTC) }

	// @spec components/usecase/ticket-sale/scan-due-reminders "Run repeated in the same period"
	t.Run("a run repeated in the same period starts the same workflow", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, usecase.ExportedReminderWorkflowID(data, at(18, 30)), usecase.ExportedReminderWorkflowID(data, at(18, 37)))
	})

	// @spec components/usecase/ticket-sale/scan-due-reminders "Not yet recorded as sent"
	t.Run("the next period's run starts a new workflow", func(t *testing.T) {
		t.Parallel()
		assert.NotEqual(t, usecase.ExportedReminderWorkflowID(data, at(18, 30)), usecase.ExportedReminderWorkflowID(data, at(18, 45)))
	})

	t.Run("another stage of the same sale is a separate workflow", func(t *testing.T) {
		t.Parallel()
		other := data
		other.Stage = int16(entity.ReminderStageApplyClose24H)
		assert.NotEqual(t, usecase.ExportedReminderWorkflowID(data, at(18, 30)), usecase.ExportedReminderWorkflowID(other, at(18, 30)))
	})
}

// A discovered sale whose anchor was past when it was discovered is never
// requested, even with the fan tracking and the window open.
//
// @spec components/usecase/ticket-sale/scan-due-reminders "Milestone already past when the sale was discovered"
func TestScanDueReminders_MilestonePastAtDiscovery(t *testing.T) {
	t.Parallel()

	now := time.Now()
	sale := &entity.TicketSale{
		ID: "sale-late", SeriesID: "series-late", Method: entity.TicketSaleMethodLottery,
		SaleStartTime:  now.Add(-2 * time.Hour),
		SaleEndTime:    now.AddDate(0, 0, 10),
		DiscoveredTime: new(now.Add(-time.Hour)),
	}
	user := &entity.User{ID: "fan-1", PreferredLanguage: "en", TimeZone: noonZone(now)}

	d := newScanDeps(t, sale)
	d.audience(sale, user, "event-1", nil)

	published, err := d.uc.ScanDueReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, published)
}

// An Organizer's sale has no discovered time, so a sale created after it opened
// is requested for each tracking fan on the next run.
//
// @spec components/usecase/ticket-sale/scan-due-reminders "Organizer's sale created after it opened"
func TestScanDueReminders_OrganizerSaleCreatedAfterItOpened(t *testing.T) {
	t.Parallel()

	now := time.Now()
	sale := organizerLottery(now.Add(-2*time.Hour), now.AddDate(0, 0, 5))
	user := &entity.User{ID: "fan-a", PreferredLanguage: "ja", TimeZone: noonZone(now)}

	d := newScanDeps(t, sale)
	d.journeyRepo.EXPECT().ListUserIDsTrackingEvents(mock.Anything, []string{"event-tokyo"}).
		Return([]*entity.SeriesTracker{{UserID: "fan-a", EventID: "event-tokyo"}}, nil).Once()
	d.recipient(sale, user, nil)
	d.expectReminder("fan-a", "sale-org", entity.ReminderStageApplyOpen, "event-tokyo")

	published, err := d.uc.ScanDueReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, published)
}

// The sent stages cannot be read: the due reminder is requested anyway, and
// DeliverReminder's own check keeps it from repeating.
func TestScanDueReminders_SentStagesUnreadable(t *testing.T) {
	t.Parallel()

	now := time.Now()
	sale := &entity.TicketSale{
		ID: "sale-lot", SeriesID: "series-lot", Method: entity.TicketSaleMethodLottery,
		SaleStartTime:  now.Add(-10 * time.Minute),
		SaleEndTime:    now.AddDate(0, 0, 10),
		DiscoveredTime: new(now.AddDate(0, 0, -3)),
	}
	user := &entity.User{ID: "fan-1", PreferredLanguage: "en", TimeZone: noonZone(now)}

	d := newScanDeps(t, sale)
	d.journeyRepo.EXPECT().ListUserIDsTrackingSeries(mock.Anything, "series-lot").
		Return([]*entity.SeriesTracker{{UserID: "fan-1", EventID: "event-1"}}, nil).Once()
	d.userRepo.EXPECT().Get(mock.Anything, "fan-1").Return(user, nil).Once()
	d.reminderRepo.EXPECT().ListSentStages(mock.Anything, sale.ID, []string{"fan-1"}).Return(nil, errors.New("db down")).Once()
	d.expectReminder("fan-1", "sale-lot", entity.ReminderStageApplyOpen, "event-1")

	published, err := d.uc.ScanDueReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, published)
}
