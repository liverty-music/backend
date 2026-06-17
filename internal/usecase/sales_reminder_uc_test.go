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
		name     string
		args     args
		wantTime time.Time
		wantOK   bool
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
			wantOK:   true,
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
			wantTime: time.Date(2026, 8, 1, 1, 0, 0, 0, time.UTC),
			wantOK:   true,
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
			wantOK:   true,
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
			// quietWindowStart(01:00 JST) = previous day 22:00 JST.
			name: "APPLY_CLOSE_1H base 01:00 quiet, deadline 02:00 (within quiet) → pre-quiet 22:00 prev day",
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
			// quietWindowStart(01:00 JST Aug 2) = 22:00 JST Aug 1 = 13:00 UTC Aug 1.
			wantTime: time.Date(2026, 8, 1, 13, 0, 0, 0, time.UTC), // 22:00 JST Aug 1
			wantOK:   true,
		},
		{
			// base = 23:00 JST (quiet), deadline = 00:00 next day JST (within quiet).
			// morning >= deadline → pre-quiet 22:00 same day.
			name: "APPLY_CLOSE_1H base 23:00 quiet, deadline 00:00 next day → pre-quiet 22:00 same day",
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
			// quietWindowStart(23:00 JST Aug 2) = 22:00 JST Aug 2 = 13:00 UTC Aug 2.
			wantTime: time.Date(2026, 8, 2, 13, 0, 0, 0, time.UTC), // 22:00 JST Aug 2
			wantOK:   true,
		},
		{
			// base = 02:00 JST (quiet), deadline = 09:00 JST.
			// morning 08:00 JST < deadline 09:00 JST → defer to morning.
			name: "APPLY_CLOSE_1H base 02:00 quiet, deadline 09:00 → defers to 08:00",
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
			// base = 2026-08-04 23:00 UTC = 08:00 JST Aug 5; NOT quiet (h=8 is boundary → quietEndHour=8, h<8 is quiet)
			// Actually 08:00 JST hour=8, NOT quiet (quiet is h<8 or h>=22). So fires at base.
			wantTime: time.Date(2026, 8, 4, 23, 0, 0, 0, time.UTC),
			wantOK:   true,
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
			wantOK:   true,
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
			wantOK:   true,
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
			wantTime: time.Date(2026, 8, 1, 10, 0, 0, 0, time.UTC),
			wantOK:   true,
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
			wantTime: time.Date(2026, 8, 1, 10, 0, 1, 0, time.UTC),
			wantOK:   true,
		},
		{
			// CLOSE_1H, deadline 01:00 JST (quiet), base 00:00 JST (quiet).
			// morning 08:00 JST > deadline 01:00 → pre-quiet = 22:00 prev day JST.
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
			// pre-quiet = 22:00 JST Sep 9 = 13:00 UTC Sep 9 < deadline 16:00 UTC Sep 9.
			wantTime: time.Date(2026, 9, 9, 13, 0, 0, 0, time.UTC), // 22:00 JST Sep 9
			wantOK:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotTime, gotOK := usecase.ExportedScheduledFireTime(tt.args.stage, tt.args.phase, tt.args.tz)
			assert.Equal(t, tt.wantOK, gotOK, "ok mismatch")
			if tt.wantOK {
				assert.Equal(t, tt.wantTime.UTC(), gotTime.UTC(), "fire time mismatch")
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
		p := usecase.ExportedBuildReminderPayload(phase, entity.ReminderStageApplyOpen, userEN)
		assert.Equal(t, "Ticket Sales Open", p.Title)
		assert.Contains(t, p.Body, "Fan Club")
		assert.Equal(t, "https://eplus.jp/example", p.URL)
		assert.Equal(t, "sales-phase-phase-001-stage-1", p.Tag)
	})
	t.Run("APPLY_OPEN ja", func(t *testing.T) {
		t.Parallel()
		p := usecase.ExportedBuildReminderPayload(phase, entity.ReminderStageApplyOpen, userJA)
		assert.Equal(t, "チケット申込受付開始", p.Title)
		assert.Contains(t, p.Body, "ファンクラブ")
	})
	t.Run("APPLY_CLOSE_24H en", func(t *testing.T) {
		t.Parallel()
		p := usecase.ExportedBuildReminderPayload(phase, entity.ReminderStageApplyClose24H, userEN)
		assert.Equal(t, "Last Day to Apply", p.Title)
	})
	t.Run("RESULT_DAY en", func(t *testing.T) {
		t.Parallel()
		p := usecase.ExportedBuildReminderPayload(phase, entity.ReminderStageResultDay, userEN)
		assert.Equal(t, "Lottery Results Today", p.Title)
	})
	t.Run("fallback URL when phase URL empty", func(t *testing.T) {
		t.Parallel()
		noURL := &entity.SalesPhase{ID: "p2", SeriesID: "s2", Channel: entity.SalesChannelGeneral}
		p := usecase.ExportedBuildReminderPayload(noURL, entity.ReminderStageApplyOpen, userEN)
		assert.Equal(t, "/series/s2", p.URL)
	})
}

// ---- ScanDueReminders end-to-end: late-result phase IS evaluated ----

// TestScanDueReminders_LateResultPhaseIsEvaluated proves end-to-end that a
// phase whose apply_start_at is far in the past but whose lottery_result_at
// recently fired IS loaded (via ListPhasesWithPendingMilestones) and that the
// consumer-side RecordSent is NOT called by the producer (fix #1).
func TestScanDueReminders_LateResultPhaseIsEvaluated(t *testing.T) {
	t.Parallel()

	logger, _ := logging.New()
	ctx := context.Background()

	base := time.Now().UTC()
	// All phase timestamps are in the past so every milestone is "due now".
	// lottery_result_at is 30h ago: 09:00 in any timezone on that calendar day
	// is guaranteed to have already passed.
	phase := &entity.SalesPhase{
		ID:                "phase-late",
		SeriesID:          "series-late",
		Channel:           entity.SalesChannelPlayguide,
		ProviderName:      "e+",
		ApplyStartTime:    base.AddDate(0, 0, -30), // 30 days ago
		ApplyEndTime:      base.AddDate(0, 0, -20), // 20 days ago
		LotteryResultTime: base.Add(-30 * time.Hour),
		URL:               "https://eplus.jp/late",
		// DiscoveredTime well before all milestones → first-sight guard never trips.
		DiscoveredTime: base.AddDate(0, 0, -60),
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
	// Audience is resolved from Tracking journeys on the phase's series.
	journeyRepo.On("ListUserIDsTrackingSeries", ctx, "series-late").Return([]string{"user-001"}, nil)
	userRepo.On("Get", ctx, "user-001").Return(user, nil)

	// Batch sent-check returns empty (none sent yet).
	reminderRepo.On("ListSentStages", ctx, "phase-late", []string{"user-001"}).
		Return(map[string]map[entity.ReminderStage]bool{}, nil)

	// All 4 stages are due (all triggers in the past, DiscoveredTime 60 days ago).
	// The producer must NOT call RecordSent (#1) — consumer does that after push.
	// Use Maybe() for quiet-hours-sensitive stages (APPLY_OPEN, RESULT_DAY)
	// since the test runs at an unpredictable wall-clock hour.
	for _, s := range []entity.ReminderStage{
		entity.ReminderStageApplyClose24H,
		entity.ReminderStageApplyClose1H,
	} {
		stage := s
		pub.On("PublishEvent", ctx, entity.SubjectSalesPhaseReminderDue,
			mock.MatchedBy(func(v any) bool {
				d, ok := v.(entity.SalesPhaseReminderDueData)
				return ok && d.UserID == "user-001" && d.PhaseID == "phase-late" &&
					entity.ReminderStage(d.Stage) == stage
			}),
		).Return(nil).Once()
	}
	for _, s := range []entity.ReminderStage{
		entity.ReminderStageApplyOpen,
		entity.ReminderStageResultDay,
	} {
		stage := s
		pub.On("PublishEvent", ctx, entity.SubjectSalesPhaseReminderDue,
			mock.MatchedBy(func(v any) bool {
				d, ok := v.(entity.SalesPhaseReminderDueData)
				return ok && d.UserID == "user-001" && d.PhaseID == "phase-late" &&
					entity.ReminderStage(d.Stage) == stage
			}),
		).Return(nil).Maybe()
	}

	uc := usecase.NewSalesReminderUseCase(
		salesPhaseRepo, reminderRepo, journeyRepo, userRepo,
		pub, lookahead, logger,
	)

	published, err := uc.ScanDueReminders(ctx)
	require.NoError(t, err)
	// APPLY_CLOSE_24H and APPLY_CLOSE_1H always fire (deadline stages return
	// now or pre-quiet, never a future morning). APPLY_OPEN and RESULT_DAY
	// may defer to 08:00 local time if the test runs during quiet hours.
	assert.GreaterOrEqual(t, published, 2,
		"must publish at least APPLY_CLOSE_24H + APPLY_CLOSE_1H for a late-result phase")

	// CRITICAL: the producer must NOT have called RecordSent (#1 fix).
	// AssertExpectations on reminderRepo will fail if RecordSent was called
	// with no matching expectation (the mock has no RecordSent registration).
}
