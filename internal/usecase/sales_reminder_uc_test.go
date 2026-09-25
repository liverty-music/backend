package usecase_test

import (
	"context"
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

// ---- scheduledFireTime ----

func TestScheduledFireTime(t *testing.T) {
	t.Parallel()

	type args struct {
		stage entity.ReminderStage
		phase *entity.SalesPhase
		tz    *time.Location
	}
	tests := []struct {
		name       string
		args       args
		wantTime   time.Time
		wantExpiry time.Time // zero = unbounded (never expires)
		wantOK     bool
	}{
		// --- APPLY_OPEN ---
		{
			name: "APPLY_OPEN base 03:00 JST (quiet) → defers to 08:00 same day",
			args: args{
				stage: entity.ReminderStageApplyOpen,
				phase: &entity.SalesPhase{
					ApplyStartTime: time.Date(2026, 8, 1, 18, 0, 0, 0, time.UTC), // 03:00 JST
					DiscoveredTime: phaseFarPast,
				},
				tz: jst,
			},
			// 08:00 JST = 23:00 UTC prev day; 2026-08-01 03:00 JST → next 08:00 = 2026-08-01 08:00 JST
			wantTime: time.Date(2026, 8, 1, 23, 0, 0, 0, time.UTC), // 08:00 JST on same day
			// ApplyEndTime unset → APPLY_OPEN has no upper bound (unbounded expiry).
			wantExpiry: time.Time{},
			wantOK:     true,
		},
		{
			name: "APPLY_OPEN base 10:00 JST (active) → fires at base",
			args: args{
				stage: entity.ReminderStageApplyOpen,
				phase: &entity.SalesPhase{
					ApplyStartTime: time.Date(2026, 8, 1, 1, 0, 0, 0, time.UTC), // 10:00 JST
					DiscoveredTime: phaseFarPast,
				},
				tz: jst,
			},
			wantTime:   time.Date(2026, 8, 1, 1, 0, 0, 0, time.UTC),
			wantExpiry: time.Time{},
			wantOK:     true,
		},
		{
			name: "APPLY_OPEN with known ApplyEndTime sets expiry to ApplyEndTime",
			args: args{
				stage: entity.ReminderStageApplyOpen,
				phase: &entity.SalesPhase{
					ApplyStartTime: time.Date(2026, 8, 1, 1, 0, 0, 0, time.UTC),  // 10:00 JST, active
					ApplyEndTime:   time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC), // 12:00 JST Aug 10
					DiscoveredTime: phaseFarPast,
				},
				tz: jst,
			},
			wantTime:   time.Date(2026, 8, 1, 1, 0, 0, 0, time.UTC),
			wantExpiry: time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC),
			wantOK:     true,
		},
		{
			name: "APPLY_OPEN zero timestamp → ok=false",
			args: args{
				stage: entity.ReminderStageApplyOpen,
				phase: &entity.SalesPhase{DiscoveredTime: phaseFarPast},
				tz:    jst,
			},
			wantOK: false,
		},

		// --- APPLY_CLOSE_24H ---
		{
			name: "APPLY_CLOSE_24H base 12:00 JST (active) → fires at base",
			args: args{
				stage: entity.ReminderStageApplyClose24H,
				phase: &entity.SalesPhase{
					// deadline 10 Aug 12:00 JST; base = 9 Aug 12:00 JST = 03:00 UTC
					ApplyEndTime:   time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC), // 12:00 JST
					DiscoveredTime: phaseFarPast,
				},
				tz: jst,
			},
			wantTime: time.Date(2026, 8, 9, 3, 0, 0, 0, time.UTC), // base = deadline - 24h
			// expiry = deadline (ApplyEndTime): never send at or after the deadline.
			wantExpiry: time.Date(2026, 8, 10, 3, 0, 0, 0, time.UTC),
			wantOK:     true,
		},
		{
			name: "APPLY_CLOSE_24H ApplyEndTime zero → ok=false",
			args: args{
				stage: entity.ReminderStageApplyClose24H,
				phase: &entity.SalesPhase{DiscoveredTime: phaseFarPast},
				tz:    jst,
			},
			wantOK: false,
		},

		// --- APPLY_CLOSE_1H quiet-hours cases ---
		{
			// base = 01:00 JST (quiet), deadline = 02:00 JST (within quiet).
			// morning (08:00 JST) >= deadline (02:00 JST) → pre-quiet alert.
			// preQuietAlertTime(01:00 JST) = previous day 21:00 JST.
			name: "APPLY_CLOSE_1H base 01:00 quiet, deadline 02:00 (within quiet) → pre-quiet 21:00 prev day",
			args: args{
				stage: entity.ReminderStageApplyClose1H,
				phase: &entity.SalesPhase{
					// deadline = 2026-08-02 02:00 JST = 2026-08-01 17:00 UTC
					// base = deadline - 1h = 2026-08-02 01:00 JST = 2026-08-01 16:00 UTC
					ApplyEndTime:   time.Date(2026, 8, 1, 17, 0, 0, 0, time.UTC), // 02:00 JST Aug 2
					DiscoveredTime: phaseFarPast,
				},
				tz: jst,
			},
			// base = 2026-08-01 16:00 UTC = 01:00 JST Aug 2; in quiet.
			// preQuietAlertTime(01:00 JST Aug 2) = 21:00 JST Aug 1 = 12:00 UTC Aug 1.
			wantTime:   time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC), // 21:00 JST Aug 1
			wantExpiry: time.Date(2026, 8, 1, 17, 0, 0, 0, time.UTC), // deadline
			wantOK:     true,
		},
		{
			// base = 23:00 JST (quiet), deadline = 00:00 next day JST (within quiet).
			// morning >= deadline → pre-quiet 21:00 same day.
			name: "APPLY_CLOSE_1H base 23:00 quiet, deadline 00:00 next day → pre-quiet 21:00 same day",
			args: args{
				stage: entity.ReminderStageApplyClose1H,
				phase: &entity.SalesPhase{
					// deadline = 2026-08-03 00:00 JST = 2026-08-02 15:00 UTC
					// base = deadline - 1h = 2026-08-02 23:00 JST = 2026-08-02 14:00 UTC
					ApplyEndTime:   time.Date(2026, 8, 2, 15, 0, 0, 0, time.UTC), // 00:00 JST Aug 3
					DiscoveredTime: phaseFarPast,
				},
				tz: jst,
			},
			// base = 2026-08-02 14:00 UTC = 23:00 JST Aug 2; in quiet.
			// preQuietAlertTime(23:00 JST Aug 2) = 21:00 JST Aug 2 = 12:00 UTC Aug 2.
			wantTime:   time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC), // 21:00 JST Aug 2
			wantExpiry: time.Date(2026, 8, 2, 15, 0, 0, 0, time.UTC), // deadline
			wantOK:     true,
		},
		{
			// base = 08:00 JST (not quiet: quietEndHour=8, h<8 is quiet), deadline = 09:00 JST.
			name: "APPLY_CLOSE_1H base 08:00 (not quiet) → fires at base",
			args: args{
				stage: entity.ReminderStageApplyClose1H,
				phase: &entity.SalesPhase{
					// deadline = 2026-08-05 09:00 JST = 2026-08-05 00:00 UTC
					// base = deadline - 1h = 2026-08-05 08:00 JST = 2026-08-04 23:00 UTC
					ApplyEndTime:   time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC), // 09:00 JST Aug 5
					DiscoveredTime: phaseFarPast,
				},
				tz: jst,
			},
			// base = 2026-08-04 23:00 UTC = 08:00 JST Aug 5; NOT quiet (h=8 is boundary; quiet is h<8 or h>=22).
			wantTime:   time.Date(2026, 8, 4, 23, 0, 0, 0, time.UTC),
			wantExpiry: time.Date(2026, 8, 5, 0, 0, 0, 0, time.UTC), // deadline
			wantOK:     true,
		},

		// --- RESULT_DAY ---
		{
			// LotteryResultTime in JST → fire at 09:00 JST on that day.
			name: "RESULT_DAY fires at 09:00 user tz (JST)",
			args: args{
				stage: entity.ReminderStageResultDay,
				phase: &entity.SalesPhase{
					LotteryResultTime: time.Date(2026, 8, 15, 5, 0, 0, 0, time.UTC), // any time on Aug 15 JST
					DiscoveredTime:    phaseFarPast,
				},
				tz: jst,
			},
			// Aug 15 in JST (UTC+9): 2026-08-15 05:00 UTC = 2026-08-15 14:00 JST → calendar day = Aug 15 JST.
			// 09:00 JST Aug 15 = 00:00 UTC Aug 15.
			wantTime: time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC),
			// expiry = start of the next local day (00:00 JST Aug 16 = 15:00 UTC Aug 15):
			// never send after the end of RESULT_DAY's local calendar day.
			wantExpiry: time.Date(2026, 8, 15, 15, 0, 0, 0, time.UTC),
			wantOK:     true,
		},
		{
			// RESULT_DAY with tz=America/Los_Angeles: fire at 09:00 LA time, NOT 09:00 JST.
			name: "RESULT_DAY fires at 09:00 in user tz (America/Los_Angeles), NOT 09:00 JST",
			args: args{
				stage: entity.ReminderStageResultDay,
				phase: &entity.SalesPhase{
					// LotteryResultTime = 2026-09-01 00:00 UTC = 2026-08-31 17:00 LA
					// → calendar day in LA = Aug 31
					// → fire at 09:00 LA Aug 31 = 16:00 UTC Aug 31
					LotteryResultTime: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
					DiscoveredTime:    phaseFarPast,
				},
				tz: la,
			},
			// LA is UTC-7 in summer (PDT). 09:00 LA Aug 31 = 16:00 UTC Aug 31.
			wantTime: time.Date(2026, 8, 31, 16, 0, 0, 0, time.UTC),
			// expiry = start of the next local day (00:00 LA Sep 1 = 07:00 UTC Sep 1, PDT).
			wantExpiry: time.Date(2026, 9, 1, 7, 0, 0, 0, time.UTC),
			wantOK:     true,
		},
		{
			name: "RESULT_DAY zero LotteryResultTime → ok=false",
			args: args{
				stage: entity.ReminderStageResultDay,
				phase: &entity.SalesPhase{DiscoveredTime: phaseFarPast},
				tz:    jst,
			},
			wantOK: false,
		},

		// --- First-sight guard ---
		{
			// phase.DiscoveredTime = now, base = 5 days ago → trigger was already past at first sight.
			name: "First-sight guard: APPLY_OPEN base before DiscoveredTime → ok=false",
			args: args{
				stage: entity.ReminderStageApplyOpen,
				phase: &entity.SalesPhase{
					ApplyStartTime: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC), // 5 days before created
					DiscoveredTime: time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC),
				},
				tz: jst,
			},
			wantOK: false,
		},
		{
			// base == DiscoveredTime exactly: base.Before(createdAt) is false → ok=true (fires).
			name: "First-sight guard: base equals DiscoveredTime → ok=true (fires)",
			args: args{
				stage: entity.ReminderStageApplyOpen,
				phase: &entity.SalesPhase{
					ApplyStartTime: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
					DiscoveredTime: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
				},
				tz: jst, // 10:00 UTC = 19:00 JST, not quiet
			},
			wantTime:   time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
			wantExpiry: time.Time{},
			wantOK:     true,
		},
		{
			// base 1s after DiscoveredTime → fires normally.
			name: "First-sight guard: base just after DiscoveredTime → ok=true",
			args: args{
				stage: entity.ReminderStageApplyOpen,
				phase: &entity.SalesPhase{
					ApplyStartTime: time.Date(2026, 8, 1, 10, 0, 1, 0, time.UTC),
					DiscoveredTime: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
				},
				tz: jst, // 19:00 JST, active
			},
			wantTime:   time.Date(2026, 8, 1, 10, 0, 1, 0, time.UTC),
			wantExpiry: time.Time{},
			wantOK:     true,
		},
		{
			// CLOSE_1H, deadline 01:00 JST (quiet), base 00:00 JST (quiet).
			// morning 08:00 JST > deadline 01:00 → pre-quiet = 21:00 prev day JST.
			// Verify pre-quiet < deadline.
			name: "pre-quiet alert is strictly before deadline",
			args: args{
				stage: entity.ReminderStageApplyClose1H,
				phase: &entity.SalesPhase{
					// deadline = 2026-09-10 01:00 JST = 2026-09-09 16:00 UTC
					// base = deadline - 1h = 2026-09-10 00:00 JST = 2026-09-09 15:00 UTC
					ApplyEndTime:   time.Date(2026, 9, 9, 16, 0, 0, 0, time.UTC),
					DiscoveredTime: phaseFarPast,
				},
				tz: jst,
			},
			// base = 15:00 UTC = 00:00 JST Sep 10; quiet.
			// morning = 08:00 JST Sep 10 = 23:00 UTC Sep 9 → but 08:00 JST > deadline 01:00 JST same day.
			// pre-quiet = 21:00 JST Sep 9 = 12:00 UTC Sep 9 < deadline 16:00 UTC Sep 9.
			wantTime:   time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC), // 21:00 JST Sep 9
			wantExpiry: time.Date(2026, 9, 9, 16, 0, 0, 0, time.UTC), // deadline
			wantOK:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotTime, gotExpiry, gotOK := usecase.ExportedScheduledFireTime(tt.args.stage, tt.args.phase, tt.args.tz)
			assert.Equal(t, tt.wantOK, gotOK, "ok mismatch")
			if tt.wantOK {
				assert.Equal(t, tt.wantTime.UTC(), gotTime.UTC(), "fire time mismatch")
				assert.Equal(t, tt.wantExpiry.UTC(), gotExpiry.UTC(), "expiry mismatch")
			}
		})
	}
}

// ---- channelDisplayName ----

func TestChannelDisplayName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		channel      entity.SalesChannel
		providerName string
		lang         string
		want         string
	}{
		{"provider name takes precedence", entity.SalesChannelPlayguide, "e+", "en", "e+"},
		{"provider name ja takes precedence", entity.SalesChannelPlayguide, "チケットぴあ", "ja", "チケットぴあ"},
		{"unspecified en → Ticket", entity.SalesChannelUnspecified, "", "en", "Ticket"},
		{"unspecified ja → チケット", entity.SalesChannelUnspecified, "", "ja", "チケット"},
		{"fan_club en", entity.SalesChannelFanClub, "", "en", "Fan Club"},
		{"fan_club ja", entity.SalesChannelFanClub, "", "ja", "ファンクラブ"},
		{"general en", entity.SalesChannelGeneral, "", "en", "General"},
		{"general ja", entity.SalesChannelGeneral, "", "ja", "一般"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := usecase.ExportedChannelDisplayName(tt.channel, tt.providerName, tt.lang)
			assert.Equal(t, tt.want, got)
		})
	}
}

// ---- buildReminderPayload ----

func TestBuildReminderPayload(t *testing.T) {
	t.Parallel()

	phase := &entity.SalesPhase{
		ID:             "phase-001",
		SeriesID:       "series-001",
		Channel:        entity.SalesChannelFanClub,
		ApplyStartTime: time.Date(2026, 8, 1, 1, 0, 0, 0, time.UTC), // 10:00 JST
		ApplyEndTime:   time.Date(2026, 8, 10, 14, 59, 0, 0, time.UTC),
		URL:            "https://eplus.jp/example",
	}
	userEN := &entity.User{ID: "user-en", PreferredLanguage: "en", TimeZone: "Asia/Tokyo"}
	userJA := &entity.User{ID: "user-ja", PreferredLanguage: "ja", TimeZone: "Asia/Tokyo"}

	t.Run("APPLY_OPEN en", func(t *testing.T) {
		t.Parallel()
		p := usecase.ExportedBuildReminderPayload(phase, entity.ReminderStageApplyOpen, userEN, "/dashboard")
		assert.Equal(t, "Ticket Sales Open", p.Title)
		assert.Contains(t, p.Body, "Fan Club")
		// The phase's own URL wins over the fallback, which is only used when
		// the phase has none (see the "fallback URL" case below).
		assert.Equal(t, "https://eplus.jp/example", p.Data[entity.NotificationDataKeyURL])
		assert.Equal(t, "sales-phase-phase-001-stage-1", p.Tag)
	})
	t.Run("APPLY_OPEN ja", func(t *testing.T) {
		t.Parallel()
		p := usecase.ExportedBuildReminderPayload(phase, entity.ReminderStageApplyOpen, userJA, "/dashboard")
		assert.Equal(t, "チケット申込受付開始", p.Title)
		assert.Contains(t, p.Body, "ファンクラブ")
	})
	t.Run("APPLY_CLOSE_24H en", func(t *testing.T) {
		t.Parallel()
		p := usecase.ExportedBuildReminderPayload(phase, entity.ReminderStageApplyClose24H, userEN, "/dashboard")
		assert.Equal(t, "Last Day to Apply", p.Title)
	})
	t.Run("RESULT_DAY en", func(t *testing.T) {
		t.Parallel()
		p := usecase.ExportedBuildReminderPayload(phase, entity.ReminderStageResultDay, userEN, "/dashboard")
		assert.Equal(t, "Lottery Results Today", p.Title)
	})
	t.Run("fallback URL when phase URL empty", func(t *testing.T) {
		t.Parallel()
		// The caller (processPhase) resolves this once per phase via
		// ResolveSeriesLinkURL and passes it in; buildReminderPayload itself
		// only decides whether to use the phase's own URL or the fallback.
		noURL := &entity.SalesPhase{ID: "p2", SeriesID: "s2", Channel: entity.SalesChannelGeneral}
		p := usecase.ExportedBuildReminderPayload(noURL, entity.ReminderStageApplyOpen, userEN, "/concerts/event-42")
		assert.Equal(t, "/concerts/event-42", p.Data[entity.NotificationDataKeyURL])
	})
}

// ---- ScanDueReminders end-to-end: expiry and pre-quiet-alert fixes (issue #472) ----
//
// Both tests below fix real timestamps far enough from "now" (>= 48h) that
// the outcome is deterministic regardless of the wall-clock hour the test
// happens to run at: the quiet-hours adjustment inside scheduledFireTime can
// shift a fire time by at most 10h (the width of the 22:00-08:00 window), so
// a 48h margin leaves no ambiguity about whether a stage is due or expired.

// TestScanDueReminders_ExpiredStagesAreSkipped proves that a phase whose
// deadline (ApplyEndTime), application window, and result day are all long
// past does NOT publish any reminder stage. Before the fix, ScanDueReminders
// only checked now.Before(fire) with no upper bound, so a stage could fire
// arbitrarily long after its deadline, application close, or result day had
// passed (issue #472's "late sends").
func TestScanDueReminders_ExpiredStagesAreSkipped(t *testing.T) {
	t.Parallel()

	logger, _ := logging.New()
	ctx := context.Background()

	now := time.Now().UTC()
	// Every milestone is far enough in the past that its expiry (the deadline
	// for APPLY_CLOSE_24H/1H, ApplyEndTime for APPLY_OPEN, and the end of the
	// local calendar day for RESULT_DAY) has already elapsed.
	phase := &entity.SalesPhase{
		ID:                "phase-expired",
		SeriesID:          "series-expired",
		Channel:           entity.SalesChannelPlayguide,
		ProviderName:      "e+",
		ApplyStartTime:    now.AddDate(0, 0, -30), // 30 days ago
		ApplyEndTime:      now.AddDate(0, 0, -20), // 20 days ago — deadline long past
		LotteryResultTime: now.AddDate(0, 0, -10), // result day long past
		URL:               "https://eplus.jp/expired",
		DiscoveredTime:    now.AddDate(0, 0, -60),
	}

	user := &entity.User{
		ID:                "user-001",
		PreferredLanguage: "en",
		TimeZone:          "Asia/Tokyo",
	}

	lookahead := 7 * 24 * time.Hour

	salesPhaseRepo := entitymocks.NewMockSalesPhaseRepository(t)
	reminderRepo := entitymocks.NewMockSalesPhaseReminderRepository(t)
	journeyRepo := entitymocks.NewMockTicketJourneyRepository(t)
	userRepo := entitymocks.NewMockUserRepository(t)
	// The phase carries its own URL (below), so ResolveSeriesLinkURL is never
	// called and this mock needs no expectations set.
	concertRepo := entitymocks.NewMockConcertRepository(t)
	pub := ucmocks.NewMockEventPublisher(t)

	salesPhaseRepo.On("ListPhasesWithPendingMilestones", ctx, lookahead, usecase.ReminderScanLookbackMargin).
		Return([]*entity.SalesPhase{phase}, nil)
	journeyRepo.On("ListUserIDsTrackingSeries", ctx, "series-expired").Return([]string{"user-001"}, nil)
	userRepo.On("Get", ctx, "user-001").Return(user, nil)
	reminderRepo.On("ListSentStages", ctx, "phase-expired", []string{"user-001"}).
		Return(map[string]map[entity.ReminderStage]bool{}, nil)

	// No PublishEvent expectation is registered for any stage: the mock fails
	// the test outright if ScanDueReminders tries to publish a stale reminder.

	uc := usecase.NewSalesReminderUseCase(
		salesPhaseRepo, reminderRepo, journeyRepo, userRepo, concertRepo,
		pub, lookahead, logger,
	)

	published, err := uc.ScanDueReminders(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, published, "an expired phase must not publish any stage")
}

// TestScanDueReminders_NotYetExpiredStageStillPublishes is a regression guard
// for the expiry check added by the fix above: a stage that is due AND still
// within its valid window must keep firing as before. ApplyEndTime and
// LotteryResultTime are left unset so APPLY_CLOSE_24H/1H and RESULT_DAY are
// inapplicable (ok=false from scheduledFireTime) and stay out of this
// assertion; only APPLY_OPEN is exercised here.
func TestScanDueReminders_NotYetExpiredStageStillPublishes(t *testing.T) {
	t.Parallel()

	logger, _ := logging.New()
	ctx := context.Background()

	now := time.Now().UTC()
	phase := &entity.SalesPhase{
		ID:             "phase-active",
		SeriesID:       "series-active",
		Channel:        entity.SalesChannelPlayguide,
		ProviderName:   "e+",
		ApplyStartTime: now.Add(-48 * time.Hour), // due
		URL:            "https://eplus.jp/active",
		DiscoveredTime: now.AddDate(0, 0, -60),
	}

	user := &entity.User{
		ID:                "user-001",
		PreferredLanguage: "en",
		TimeZone:          "Asia/Tokyo",
	}

	lookahead := 7 * 24 * time.Hour

	salesPhaseRepo := entitymocks.NewMockSalesPhaseRepository(t)
	reminderRepo := entitymocks.NewMockSalesPhaseReminderRepository(t)
	journeyRepo := entitymocks.NewMockTicketJourneyRepository(t)
	userRepo := entitymocks.NewMockUserRepository(t)
	pub := ucmocks.NewMockEventPublisher(t)

	salesPhaseRepo.On("ListPhasesWithPendingMilestones", ctx, lookahead, usecase.ReminderScanLookbackMargin).
		Return([]*entity.SalesPhase{phase}, nil)
	journeyRepo.On("ListUserIDsTrackingSeries", ctx, "series-active").Return([]string{"user-001"}, nil)
	userRepo.On("Get", ctx, "user-001").Return(user, nil)
	reminderRepo.On("ListSentStages", ctx, "phase-active", []string{"user-001"}).
		Return(map[string]map[entity.ReminderStage]bool{}, nil)

	pub.On("PublishEvent", ctx, entity.SubjectSalesPhaseReminderDue,
		mock.MatchedBy(func(v any) bool {
			d, ok := v.(entity.SalesPhaseReminderDueData)
			return ok && d.UserID == "user-001" && d.PhaseID == "phase-active" &&
				entity.ReminderStage(d.Stage) == entity.ReminderStageApplyOpen
		}),
	).Return(nil).Once()

	uc := usecase.NewSalesReminderUseCase(
		salesPhaseRepo, reminderRepo, journeyRepo, userRepo,
		pub, lookahead, logger,
	)

	published, err := uc.ScanDueReminders(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, published, "APPLY_OPEN must still publish when due and not yet expired")
}
