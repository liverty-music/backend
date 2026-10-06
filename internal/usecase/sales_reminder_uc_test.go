package usecase_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// la is America/Los_Angeles, used for RESULT_DAY tz tests.
var la = func() *time.Location {
	loc, _ := time.LoadLocation("America/Los_Angeles")
	return loc
}()

// phaseFarPast is a DiscoveredTime well in the past so it never trips the first-sight guard.
var phaseFarPast = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

func jstAt(y int, m time.Month, d, hh, mm int) time.Time {
	return time.Date(y, m, d, hh, mm, 0, 0, jst)
}

// ---- scheduledFireTime ----

func TestScheduledFireTime(t *testing.T) {
	t.Parallel()

	// kickoff is a lottery opening 5 Oct 18:00, closing 22 Oct 23:59 and
	// announcing results 3 Nov 15:00 (JST).
	kickoff := &entity.SalesPhase{
		Method:            entity.SalesMethodLottery,
		ApplyStartTime:    jstAt(2026, 10, 5, 18, 0),
		ApplyEndTime:      jstAt(2026, 10, 22, 23, 59),
		LotteryResultTime: jstAt(2026, 11, 3, 15, 0),
		DiscoveredTime:    phaseFarPast,
	}
	// firstCome opens 6 Oct 19:00 JST and ends when tickets run out.
	firstCome := &entity.SalesPhase{
		Method:         entity.SalesMethodFirstCome,
		ApplyStartTime: jstAt(2026, 10, 6, 19, 0),
		DiscoveredTime: phaseFarPast,
	}

	tests := []struct {
		name       string
		stage      entity.ReminderStage
		phase      *entity.SalesPhase
		tz         *time.Location
		wantTime   time.Time
		wantExpiry time.Time
		wantOK     bool
	}{
		// @spec components/entity/sales-phase-reminder "Lottery stages"
		{
			name: "Lottery stages: APPLY_OPEN at the apply start", stage: entity.ReminderStageApplyOpen, phase: kickoff, tz: jst,
			wantTime: jstAt(2026, 10, 5, 18, 0), wantExpiry: jstAt(2026, 10, 22, 23, 59), wantOK: true,
		},
		{
			// The 21 Oct 23:59 anchor falls in quiet hours and moves to 08:00 on the closing day.
			name: "Lottery stages: APPLY_CLOSE_24H anchored 24h before the close", stage: entity.ReminderStageApplyClose24H, phase: kickoff, tz: jst,
			wantTime: jstAt(2026, 10, 22, 8, 0), wantExpiry: jstAt(2026, 10, 22, 23, 59), wantOK: true,
		},
		{
			name: "Lottery stages: RESULT_DAY on the result day", stage: entity.ReminderStageResultDay, phase: kickoff, tz: jst,
			wantTime: jstAt(2026, 11, 3, 9, 0), wantExpiry: jstAt(2026, 11, 4, 0, 0), wantOK: true,
		},

		// @spec components/entity/sales-phase-reminder "First-come stage"
		// @spec components/usecase/sales-phase/scan-due-reminders "First-come sale about to open"
		{
			name: "First-come stage: APPLY_OPEN 30 minutes before the start", stage: entity.ReminderStageApplyOpen, phase: firstCome, tz: jst,
			wantTime: jstAt(2026, 10, 6, 18, 30), wantExpiry: jstAt(2026, 10, 6, 19, 0), wantOK: true,
		},
		{
			name: "First-come stage: no APPLY_CLOSE_24H", stage: entity.ReminderStageApplyClose24H,
			phase: &entity.SalesPhase{Method: entity.SalesMethodFirstCome, ApplyStartTime: jstAt(2026, 10, 6, 19, 0),
				ApplyEndTime: jstAt(2026, 10, 25, 23, 59), DiscoveredTime: phaseFarPast},
			tz: jst, wantOK: false,
		},
		{
			name: "First-come stage: no RESULT_DAY", stage: entity.ReminderStageResultDay, phase: firstCome, tz: jst, wantOK: false,
		},

		// @spec components/entity/sales-phase-reminder "Lottery without a result time"
		{
			name: "Lottery without a result time: no RESULT_DAY", stage: entity.ReminderStageResultDay,
			phase: &entity.SalesPhase{Method: entity.SalesMethodLottery, ApplyStartTime: jstAt(2026, 10, 5, 18, 0),
				ApplyEndTime: jstAt(2026, 10, 22, 23, 59), DiscoveredTime: phaseFarPast},
			tz: jst, wantOK: false,
		},
		{
			name: "undefined stage value 3 does not apply", stage: entity.ReminderStage(3), phase: kickoff, tz: jst, wantOK: false,
		},

		// @spec components/usecase/sales-phase/scan-due-reminders "First-come sale at midnight"
		// @spec stories/get-reminded-of-ticket-sale-milestones "First-come sale at midnight"
		{
			name: "First-come sale at midnight: 23:30 anchor moves to 21:00 that evening", stage: entity.ReminderStageApplyOpen,
			phase: &entity.SalesPhase{Method: entity.SalesMethodFirstCome, ApplyStartTime: jstAt(2026, 10, 7, 0, 0), DiscoveredTime: phaseFarPast},
			tz:    jst, wantTime: jstAt(2026, 10, 6, 21, 0), wantExpiry: jstAt(2026, 10, 7, 0, 0), wantOK: true,
		},
		{
			name: "first-come sale at 05:00: 04:30 anchor moves to 21:00 the previous evening", stage: entity.ReminderStageApplyOpen,
			phase: &entity.SalesPhase{Method: entity.SalesMethodFirstCome, ApplyStartTime: jstAt(2026, 10, 7, 5, 0), DiscoveredTime: phaseFarPast},
			tz:    jst, wantTime: jstAt(2026, 10, 6, 21, 0), wantExpiry: jstAt(2026, 10, 7, 5, 0), wantOK: true,
		},
		{
			name: "first-come sale at 08:30: 08:00 anchor is outside quiet hours", stage: entity.ReminderStageApplyOpen,
			phase: &entity.SalesPhase{Method: entity.SalesMethodFirstCome, ApplyStartTime: jstAt(2026, 10, 7, 8, 30), DiscoveredTime: phaseFarPast},
			tz:    jst, wantTime: jstAt(2026, 10, 7, 8, 0), wantExpiry: jstAt(2026, 10, 7, 8, 30), wantOK: true,
		},

		// @spec components/usecase/sales-phase/scan-due-reminders "Lottery opening during the night"
		// @spec stories/get-reminded-of-ticket-sale-milestones "Lottery opening at 02:00"
		{
			name: "Lottery opening during the night: 02:00 open moves to 08:00", stage: entity.ReminderStageApplyOpen,
			phase: &entity.SalesPhase{Method: entity.SalesMethodLottery, ApplyStartTime: jstAt(2026, 10, 7, 2, 0),
				ApplyEndTime: jstAt(2026, 10, 14, 23, 59), DiscoveredTime: phaseFarPast},
			tz: jst, wantTime: jstAt(2026, 10, 7, 8, 0), wantExpiry: jstAt(2026, 10, 14, 23, 59), wantOK: true,
		},

		// @spec components/usecase/sales-phase/scan-due-reminders "Close reminder at night"
		{
			name: "Close reminder at night: 23:59 close fires 08:00 on the closing day", stage: entity.ReminderStageApplyClose24H,
			phase: &entity.SalesPhase{Method: entity.SalesMethodLottery, ApplyStartTime: jstAt(2026, 10, 5, 18, 0),
				ApplyEndTime: jstAt(2026, 10, 22, 23, 59), DiscoveredTime: phaseFarPast},
			tz: jst, wantTime: jstAt(2026, 10, 22, 8, 0), wantExpiry: jstAt(2026, 10, 22, 23, 59), wantOK: true,
		},
		{
			name: "close at 12:00 fires at the 12:00 anchor the day before", stage: entity.ReminderStageApplyClose24H,
			phase: &entity.SalesPhase{Method: entity.SalesMethodLottery, ApplyStartTime: jstAt(2026, 10, 5, 18, 0),
				ApplyEndTime: jstAt(2026, 10, 22, 12, 0), DiscoveredTime: phaseFarPast},
			tz: jst, wantTime: jstAt(2026, 10, 21, 12, 0), wantExpiry: jstAt(2026, 10, 22, 12, 0), wantOK: true,
		},

		// @spec components/usecase/sales-phase/scan-due-reminders "Result day"
		{
			name: "Result day: 15 July 18:00 result is due 09:00 that day", stage: entity.ReminderStageResultDay,
			phase: &entity.SalesPhase{Method: entity.SalesMethodLottery, ApplyStartTime: jstAt(2026, 7, 1, 10, 0),
				ApplyEndTime: jstAt(2026, 7, 10, 23, 59), LotteryResultTime: jstAt(2026, 7, 15, 18, 0), DiscoveredTime: phaseFarPast},
			tz: jst, wantTime: jstAt(2026, 7, 15, 9, 0), wantExpiry: jstAt(2026, 7, 16, 0, 0), wantOK: true,
		},
		{
			name: "result day in the fan's time zone (America/Los_Angeles)", stage: entity.ReminderStageResultDay,
			phase: &entity.SalesPhase{Method: entity.SalesMethodLottery, ApplyStartTime: jstAt(2026, 7, 1, 10, 0),
				ApplyEndTime: jstAt(2026, 7, 10, 23, 59), LotteryResultTime: jstAt(2026, 7, 15, 18, 0), DiscoveredTime: phaseFarPast},
			// 15 July 18:00 JST is 15 July 02:00 PDT.
			tz: la, wantTime: time.Date(2026, 7, 15, 9, 0, 0, 0, la), wantExpiry: time.Date(2026, 7, 16, 0, 0, 0, 0, la), wantOK: true,
		},

		// @spec components/usecase/sales-phase/scan-due-reminders "Milestone already past when the phase was discovered"
		{
			name: "Milestone already past when the phase was discovered", stage: entity.ReminderStageApplyOpen,
			phase: &entity.SalesPhase{Method: entity.SalesMethodLottery, ApplyStartTime: jstAt(2026, 10, 5, 10, 0),
				ApplyEndTime: jstAt(2026, 10, 12, 23, 59), DiscoveredTime: jstAt(2026, 10, 5, 12, 0)},
			tz: jst, wantOK: false,
		},
		{
			name: "first-sight guard: anchor equal to discovery fires", stage: entity.ReminderStageApplyOpen,
			phase: &entity.SalesPhase{Method: entity.SalesMethodLottery, ApplyStartTime: jstAt(2026, 10, 5, 10, 0),
				ApplyEndTime: jstAt(2026, 10, 12, 23, 59), DiscoveredTime: jstAt(2026, 10, 5, 10, 0)},
			tz: jst, wantTime: jstAt(2026, 10, 5, 10, 0), wantExpiry: jstAt(2026, 10, 12, 23, 59), wantOK: true,
		},
		{
			name: "first-come sale discovered at 21:00 the evening before a midnight opening still fires", stage: entity.ReminderStageApplyOpen,
			phase: &entity.SalesPhase{Method: entity.SalesMethodFirstCome, ApplyStartTime: jstAt(2026, 10, 7, 0, 0),
				DiscoveredTime: jstAt(2026, 10, 6, 21, 3)},
			tz: jst, wantTime: jstAt(2026, 10, 6, 21, 0), wantExpiry: jstAt(2026, 10, 7, 0, 0), wantOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotTime, gotExpiry, gotOK := usecase.ExportedScheduledFireTime(tt.stage, tt.phase, tt.tz)
			require.Equal(t, tt.wantOK, gotOK)
			if !tt.wantOK {
				return
			}
			assert.True(t, tt.wantTime.Equal(gotTime), "fire: got %s, want %s", gotTime.In(tt.tz), tt.wantTime.In(tt.tz))
			assert.True(t, tt.wantExpiry.Equal(gotExpiry), "expiry: got %s, want %s", gotExpiry.In(tt.tz), tt.wantExpiry.In(tt.tz))
		})
	}
}

// ---- buildReminderPayload ----

func TestBuildReminderPayload(t *testing.T) {
	t.Parallel()

	const kickoffTitle = "King Gnu 10th Anniversary Opening Live “KICKOFF”"
	lottery := &entity.SalesPhase{
		ID:                "phase-1",
		Method:            entity.SalesMethodLottery,
		ApplyStartTime:    jstAt(2026, 10, 5, 18, 0),
		ApplyEndTime:      jstAt(2026, 10, 22, 23, 59),
		LotteryResultTime: jstAt(2026, 11, 3, 15, 0),
	}
	firstCome := &entity.SalesPhase{
		ID:             "phase-2",
		Method:         entity.SalesMethodFirstCome,
		ApplyStartTime: jstAt(2026, 10, 6, 19, 0),
	}
	ja := &entity.User{PreferredLanguage: "ja", TimeZone: "Asia/Tokyo"}
	en := &entity.User{PreferredLanguage: "en", TimeZone: "Asia/Tokyo"}

	tests := []struct {
		name      string
		phase     *entity.SalesPhase
		stage     entity.ReminderStage
		user      *entity.User
		wantTitle string
		wantText  string
	}{
		{
			// @spec components/usecase/sales-phase/scan-due-reminders "Lottery closing in Japanese"
			// @spec components/usecase/sales-phase/scan-due-reminders "Deferred close reminder keeps the absolute deadline"
			name: "Lottery closing in Japanese", phase: lottery, stage: entity.ReminderStageApplyClose24H, user: ja,
			wantTitle: "抽選の申し込み締切が近づいています",
			wantText:  "チケットの抽選申し込み締切は10/22(木) 23:59!!\n" + kickoffTitle,
		},
		{
			name: "lottery closing in English", phase: lottery, stage: entity.ReminderStageApplyClose24H, user: en,
			wantTitle: "Ticket Lottery Closing Soon",
			wantText:  "Ticket lottery entry closes Oct 22 (Thu) 23:59!\n" + kickoffTitle,
		},
		{
			name: "lottery opens in Japanese", phase: lottery, stage: entity.ReminderStageApplyOpen, user: ja,
			wantTitle: "チケット申し込み受付開始",
			wantText:  "チケットの抽選申し込みがスタートしました!! 締切は10/22(木) 23:59\n" + kickoffTitle,
		},
		{
			name: "lottery opens in English", phase: lottery, stage: entity.ReminderStageApplyOpen, user: en,
			wantTitle: "Ticket Lottery Open",
			wantText:  "Ticket lottery entry is open! Closes Oct 22 (Thu) 23:59.\n" + kickoffTitle,
		},
		{
			name: "result day in Japanese", phase: lottery, stage: entity.ReminderStageResultDay, user: ja,
			wantTitle: "本日 抽選結果発表",
			wantText:  "本日11/3(火) 15:00にチケットの抽選結果発表!!\n" + kickoffTitle,
		},
		{
			name: "result day in English", phase: lottery, stage: entity.ReminderStageResultDay, user: en,
			wantTitle: "Lottery Results Today",
			wantText:  "Ticket lottery results are out today at Nov 3 (Tue) 15:00!\n" + kickoffTitle,
		},
		{
			name: "first-come sale in Japanese", phase: firstCome, stage: entity.ReminderStageApplyOpen, user: ja,
			wantTitle: "まもなく先着販売開始",
			wantText:  "10/6(火) 19:00からチケットの先着販売スタート!!\n" + kickoffTitle,
		},
		{
			name: "first-come sale in English, unset language", phase: firstCome, stage: entity.ReminderStageApplyOpen,
			user:      &entity.User{TimeZone: "Asia/Tokyo"},
			wantTitle: "First-Come Sale Starting Soon",
			wantText:  "Ticket sale (first come) starts Oct 6 (Tue) 19:00!\n" + kickoffTitle,
		},
		{
			name: "times are in the fan's time zone", phase: firstCome, stage: entity.ReminderStageApplyOpen,
			user:      &entity.User{PreferredLanguage: "en", TimeZone: "Asia/Taipei"},
			wantTitle: "First-Come Sale Starting Soon",
			wantText:  "Ticket sale (first come) starts Oct 6 (Tue) 18:00!\n" + kickoffTitle,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := usecase.ExportedBuildReminderPayload(tt.phase, tt.stage, tt.user, kickoffTitle, "event-nov1")
			assert.Equal(t, tt.wantTitle, p.Title)
			assert.Equal(t, tt.wantText, p.Body)
			assert.NotContains(t, p.Body, "hour", "the text states absolute times only")
			assert.NotContains(t, p.Body, "時間", "the text states absolute times only")
			assert.Equal(t, fmt.Sprintf("sales-phase-%s-stage-%d", tt.phase.ID, tt.stage), p.Tag)
		})
	}

	// @spec components/usecase/sales-phase/scan-due-reminders "Link to the tracked event"
	t.Run("Link to the tracked event", func(t *testing.T) {
		t.Parallel()

		p := usecase.ExportedBuildReminderPayload(lottery, entity.ReminderStageApplyOpen, ja, kickoffTitle, "event-nov1")
		assert.Equal(t, "/concerts/event-nov1", p.Data[entity.NotificationDataKeyURL])
	})

	t.Run("different stages do not share a tag", func(t *testing.T) {
		t.Parallel()

		open := usecase.ExportedBuildReminderPayload(lottery, entity.ReminderStageApplyOpen, ja, kickoffTitle, "e")
		closing := usecase.ExportedBuildReminderPayload(lottery, entity.ReminderStageApplyClose24H, ja, kickoffTitle, "e")
		assert.NotEqual(t, open.Tag, closing.Tag)
	})
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
	salesPhaseRepo *entitymocks.MockSalesPhaseRepository
	reminderRepo   *entitymocks.MockSalesPhaseReminderRepository
	journeyRepo    *entitymocks.MockTicketJourneyRepository
	userRepo       *entitymocks.MockUserRepository
	seriesRepo     *entitymocks.MockSeriesRepository
	pub            *ucmocks.MockEventPublisher
	uc             usecase.SalesReminderUseCase
}

const scanLookahead = 7 * 24 * time.Hour

func newScanDeps(t *testing.T, phases ...*entity.SalesPhase) *scanDeps {
	t.Helper()
	logger, err := logging.New()
	require.NoError(t, err)
	d := &scanDeps{
		salesPhaseRepo: entitymocks.NewMockSalesPhaseRepository(t),
		reminderRepo:   entitymocks.NewMockSalesPhaseReminderRepository(t),
		journeyRepo:    entitymocks.NewMockTicketJourneyRepository(t),
		userRepo:       entitymocks.NewMockUserRepository(t),
		seriesRepo:     entitymocks.NewMockSeriesRepository(t),
		pub:            ucmocks.NewMockEventPublisher(t),
	}
	d.salesPhaseRepo.EXPECT().
		ListPhasesWithPendingMilestones(mock.Anything, scanLookahead, usecase.ReminderScanLookbackMargin).
		Return(phases, nil).Once()
	d.uc = usecase.NewSalesReminderUseCase(
		d.salesPhaseRepo, d.reminderRepo, d.journeyRepo, d.userRepo, d.seriesRepo,
		d.pub, scanLookahead, logger,
	)
	return d
}

// audience registers one tracking fan for the phase, with no stage sent yet
// unless sent is given.
func (d *scanDeps) audience(phase *entity.SalesPhase, user *entity.User, eventID string, sent map[entity.ReminderStage]bool) {
	d.journeyRepo.EXPECT().ListUserIDsTrackingSeries(mock.Anything, phase.SeriesID).
		Return([]*entity.SeriesTracker{{UserID: user.ID, EventID: eventID}}, nil).Once()
	d.userRepo.EXPECT().Get(mock.Anything, user.ID).Return(user, nil).Once()
	sentSet := map[string]map[entity.ReminderStage]bool{}
	if sent != nil {
		sentSet[user.ID] = sent
	}
	d.reminderRepo.EXPECT().ListSentStages(mock.Anything, phase.ID, []string{user.ID}).Return(sentSet, nil).Once()
}

func (d *scanDeps) seriesTitle(seriesID, title string) {
	d.seriesRepo.EXPECT().Get(mock.Anything, seriesID).Return(&entity.Series{ID: seriesID, Title: title}, nil).Once()
}

// expectReminder expects one SALES_PHASE.reminder.due publish for the user,
// phase and stage whose payload satisfies matches.
func (d *scanDeps) expectReminder(userID, phaseID string, stage entity.ReminderStage, matches func(p *entity.NotificationPayload) bool) {
	d.pub.EXPECT().PublishEvent(mock.Anything, entity.SubjectSalesPhaseReminderDue,
		mock.MatchedBy(func(v any) bool {
			data, ok := v.(entity.SalesPhaseReminderDueData)
			return ok && data.UserID == userID && data.PhaseID == phaseID &&
				entity.ReminderStage(data.Stage) == stage && matches(data.Payload)
		}),
	).Return(nil).Once()
}

func anyPayload(*entity.NotificationPayload) bool { return true }

// @spec components/usecase/sales-phase/scan-due-reminders "First-come sale about to open"
// @spec components/usecase/sales-phase/scan-due-reminders "Tracking fan"
// @spec stories/get-reminded-of-ticket-sale-milestones "First-come sale about to open"
func TestScanDueReminders_FirstComeAboutToOpen(t *testing.T) {
	t.Parallel()

	now := time.Now()
	phase := &entity.SalesPhase{
		ID: "phase-fc", SeriesID: "series-fc", Method: entity.SalesMethodFirstCome,
		ApplyStartTime: now.Add(20 * time.Minute), // anchor 10 minutes ago
		DiscoveredTime: now.AddDate(0, 0, -3),
	}
	user := &entity.User{ID: "fan-1", PreferredLanguage: "ja", TimeZone: noonZone(now)}

	d := newScanDeps(t, phase)
	d.audience(phase, user, "event-osaka", nil)
	d.seriesTitle("series-fc", "SHADOWS")
	d.expectReminder("fan-1", "phase-fc", entity.ReminderStageApplyOpen, func(p *entity.NotificationPayload) bool {
		return p.Title == "まもなく先着販売開始" &&
			strings.HasSuffix(p.Body, "からチケットの先着販売スタート!!\nSHADOWS") &&
			p.Data[entity.NotificationDataKeyURL] == "/concerts/event-osaka"
	})

	published, err := d.uc.ScanDueReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, published)
}

// @spec components/usecase/sales-phase/scan-due-reminders "First-come sale already open"
func TestScanDueReminders_FirstComeAlreadyOpen(t *testing.T) {
	t.Parallel()

	now := time.Now()
	phase := &entity.SalesPhase{
		ID: "phase-fc", SeriesID: "series-fc", Method: entity.SalesMethodFirstCome,
		ApplyStartTime: now.Add(-5 * time.Minute), // opened 5 minutes ago
		DiscoveredTime: now.AddDate(0, 0, -3),
	}
	user := &entity.User{ID: "fan-1", PreferredLanguage: "ja", TimeZone: noonZone(now)}

	d := newScanDeps(t, phase)
	d.audience(phase, user, "event-1", nil)
	// No publish is expected: the stage expired when the sale opened.

	published, err := d.uc.ScanDueReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, published)
}

// @spec components/usecase/sales-phase/scan-due-reminders "Lottery opens"
// @spec stories/get-reminded-of-ticket-sale-milestones "Lottery reminders"
func TestScanDueReminders_LotteryOpens(t *testing.T) {
	t.Parallel()

	now := time.Now()
	phase := &entity.SalesPhase{
		ID: "phase-lot", SeriesID: "series-lot", Method: entity.SalesMethodLottery,
		ApplyStartTime: now.Add(-10 * time.Minute),
		ApplyEndTime:   now.AddDate(0, 0, 10),
		DiscoveredTime: now.AddDate(0, 0, -3),
	}
	user := &entity.User{ID: "fan-1", PreferredLanguage: "en", TimeZone: noonZone(now)}

	d := newScanDeps(t, phase)
	d.audience(phase, user, "event-1", nil)
	d.seriesTitle("series-lot", "KICKOFF")
	d.expectReminder("fan-1", "phase-lot", entity.ReminderStageApplyOpen, func(p *entity.NotificationPayload) bool {
		return p.Title == "Ticket Lottery Open" && strings.HasSuffix(p.Body, "\nKICKOFF")
	})

	published, err := d.uc.ScanDueReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, published)
}

// @spec components/usecase/sales-phase/scan-due-reminders "Already sent"
func TestScanDueReminders_AlreadySentStageIsNotRepublished(t *testing.T) {
	t.Parallel()

	now := time.Now()
	phase := &entity.SalesPhase{
		ID: "phase-sent", SeriesID: "series-sent", Method: entity.SalesMethodLottery,
		ApplyStartTime: now.Add(-10 * time.Minute),
		ApplyEndTime:   now.AddDate(0, 0, 10),
		DiscoveredTime: now.AddDate(0, 0, -3),
	}
	user := &entity.User{ID: "fan-1", PreferredLanguage: "en", TimeZone: noonZone(now)}

	d := newScanDeps(t, phase)
	d.audience(phase, user, "event-1", map[entity.ReminderStage]bool{entity.ReminderStageApplyOpen: true})
	// No publish is expected: APPLY_OPEN is already recorded as sent.

	published, err := d.uc.ScanDueReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, published)
}

// TestScanDueReminders_ExpiredStagesAreSkipped proves that no stage fires
// once its moment has passed.
//
// @spec components/usecase/sales-phase/scan-due-reminders "Application window closed before the open reminder was sent"
// @spec components/usecase/sales-phase/scan-due-reminders "Result day has ended"
func TestScanDueReminders_ExpiredStagesAreSkipped(t *testing.T) {
	t.Parallel()

	now := time.Now()
	phase := &entity.SalesPhase{
		ID: "phase-expired", SeriesID: "series-expired", Method: entity.SalesMethodLottery,
		ApplyStartTime:    now.AddDate(0, 0, -30),
		ApplyEndTime:      now.AddDate(0, 0, -20),
		LotteryResultTime: now.AddDate(0, 0, -10),
		DiscoveredTime:    now.AddDate(0, 0, -60),
	}
	user := &entity.User{ID: "fan-1", PreferredLanguage: "en", TimeZone: "Asia/Tokyo"}

	d := newScanDeps(t, phase)
	d.audience(phase, user, "event-1", nil)
	// No publish expectation: the mock fails the test on any stale reminder.

	published, err := d.uc.ScanDueReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, published)
}

// @spec components/usecase/sales-phase/scan-due-reminders "Fan no longer listed"
func TestScanDueReminders_FanNoLongerListed(t *testing.T) {
	t.Parallel()

	now := time.Now()
	phase := &entity.SalesPhase{
		ID: "phase-rd", SeriesID: "series-rd", Method: entity.SalesMethodLottery,
		ApplyStartTime:    now.AddDate(0, 0, -20),
		ApplyEndTime:      now.AddDate(0, 0, -10),
		LotteryResultTime: now,
		DiscoveredTime:    now.AddDate(0, 0, -30),
	}

	d := newScanDeps(t, phase)
	// The fan who tracked earlier is no longer returned on the result day.
	d.journeyRepo.EXPECT().ListUserIDsTrackingSeries(mock.Anything, "series-rd").Return(nil, nil).Once()

	published, err := d.uc.ScanDueReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, published)
}

// @spec components/usecase/sales-phase/scan-due-reminders "Phase opening in 3 days"
// @spec components/usecase/sales-phase/scan-due-reminders "One phase fails"
func TestScanDueReminders_EvaluatesEachListedPhase(t *testing.T) {
	t.Parallel()

	now := time.Now()
	broken := &entity.SalesPhase{
		ID: "phase-broken", SeriesID: "series-broken", Method: entity.SalesMethodLottery,
		ApplyStartTime: now.Add(-10 * time.Minute), ApplyEndTime: now.AddDate(0, 0, 10),
		DiscoveredTime: now.AddDate(0, 0, -3),
	}
	inThreeDays := &entity.SalesPhase{
		ID: "phase-3d", SeriesID: "series-3d", Method: entity.SalesMethodLottery,
		ApplyStartTime: now.AddDate(0, 0, 3), ApplyEndTime: now.AddDate(0, 0, 10),
		DiscoveredTime: now.AddDate(0, 0, -1),
	}
	open := &entity.SalesPhase{
		ID: "phase-open", SeriesID: "series-open", Method: entity.SalesMethodLottery,
		ApplyStartTime: now.Add(-10 * time.Minute), ApplyEndTime: now.AddDate(0, 0, 10),
		DiscoveredTime: now.AddDate(0, 0, -3),
	}
	user := &entity.User{ID: "fan-1", PreferredLanguage: "en", TimeZone: noonZone(now)}

	d := newScanDeps(t, broken, inThreeDays, open)
	d.journeyRepo.EXPECT().ListUserIDsTrackingSeries(mock.Anything, "series-broken").
		Return(nil, errors.New("db down")).Once()
	// The phase opening in 3 days is evaluated (its audience is read) but has
	// nothing due yet.
	d.audience(inThreeDays, user, "event-3d", nil)
	d.audience(open, user, "event-open", nil)
	d.seriesTitle("series-open", "Tour")
	d.expectReminder("fan-1", "phase-open", entity.ReminderStageApplyOpen, anyPayload)

	published, err := d.uc.ScanDueReminders(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, published)
}

// ---- userTimezone ----

// TestUserTimezone covers the fallback used for the fan's quiet-hours window
// and due-time calculations: an unset or unrecognised time zone falls back
// to Asia/Tokyo.
//
// @spec components/usecase/sales-phase/scan-due-reminders "Time zone fallback"
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
