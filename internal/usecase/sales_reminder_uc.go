package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-logging/logging"
)

const (
	// quietStartHour is the start of the quiet window (22:00 local).
	quietStartHour = 22
	// quietEndHour is the end of the quiet window (08:00 local).
	quietEndHour = 8
	// resultDayNotifyHour is the hour of day at which RESULT_DAY fires (09:00 in the user's TZ).
	resultDayNotifyHour = 9
	// fallbackTimeZone is used when a user has no time_zone set.
	fallbackTimeZone = "Asia/Tokyo"
)

// SalesReminderUseCase scans upcoming sales phases for milestones that became
// due in the current scan window and publishes a SALES_PHASE.reminder.due
// event for each (user, phase, stage) triple not yet recorded in the sent-log.
type SalesReminderUseCase interface {
	// ScanDueReminders runs one scan pass. It lists phases in the upcoming
	// window, resolves audiences, applies quiet-hours logic, checks the
	// sent-log, and publishes due reminders. Returns the number of events
	// published.
	ScanDueReminders(ctx context.Context) (int, error)
}

type salesReminderUseCase struct {
	salesPhaseRepo entity.SalesPhaseRepository
	reminderRepo   entity.SalesPhaseReminderRepository
	journeyRepo    entity.TicketJourneyRepository
	userRepo       entity.UserRepository
	publisher      EventPublisher
	// lookahead is the forward horizon passed to ListPhasesWithPendingMilestones.
	lookahead time.Duration
	// lookbackMargin is the grace period passed to ListPhasesWithPendingMilestones.
	lookbackMargin time.Duration
	logger         *logging.Logger
}

// Compile-time interface compliance check.
var _ SalesReminderUseCase = (*salesReminderUseCase)(nil)

// reminderScanLookbackMargin is the default grace period. Include phases whose
// latest milestone fired up to 2 hours ago so a milestone that occurred just
// before this scan run is not silently dropped.
const reminderScanLookbackMargin = 2 * time.Hour

// NewSalesReminderUseCase wires the reminder scan use case.
func NewSalesReminderUseCase(
	salesPhaseRepo entity.SalesPhaseRepository,
	reminderRepo entity.SalesPhaseReminderRepository,
	journeyRepo entity.TicketJourneyRepository,
	userRepo entity.UserRepository,
	publisher EventPublisher,
	lookahead time.Duration,
	logger *logging.Logger,
) SalesReminderUseCase {
	return &salesReminderUseCase{
		salesPhaseRepo: salesPhaseRepo,
		reminderRepo:   reminderRepo,
		journeyRepo:    journeyRepo,
		userRepo:       userRepo,
		publisher:      publisher,
		lookahead:      lookahead,
		lookbackMargin: reminderScanLookbackMargin,
		logger:         logger,
	}
}

// ScanDueReminders implements [SalesReminderUseCase].
func (uc *salesReminderUseCase) ScanDueReminders(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	uc.logger.Info(ctx, "sales_reminder: starting scan", slog.String("now", now.Format(time.RFC3339)))

	phases, err := uc.salesPhaseRepo.ListPhasesWithPendingMilestones(ctx, uc.lookahead, uc.lookbackMargin)
	if err != nil {
		return 0, fmt.Errorf("sales_reminder: list phases: %w", err)
	}
	uc.logger.Info(ctx, "sales_reminder: phases in window", slog.Int("count", len(phases)))

	var totalPublished int
	for _, phase := range phases {
		if ctx.Err() != nil {
			break
		}
		n, err := uc.processPhase(ctx, phase, now)
		if err != nil {
			uc.logger.Error(ctx, "sales_reminder: error processing phase", err,
				slog.String("phase_id", phase.ID),
			)
			continue
		}
		totalPublished += n
	}

	uc.logger.Info(ctx, "sales_reminder: scan complete",
		slog.Int("phases_processed", len(phases)),
		slog.Int("reminders_published", totalPublished),
	)
	return totalPublished, nil
}

// allStages is the full ordered set of reminder stages evaluated each scan.
var allStages = []entity.ReminderStage{
	entity.ReminderStageApplyOpen,
	entity.ReminderStageApplyClose24H,
	entity.ReminderStageApplyClose1H,
	entity.ReminderStageResultDay,
}

// processPhase evaluates all reminder stages for one phase and returns the
// number of reminder events published.
func (uc *salesReminderUseCase) processPhase(ctx context.Context, phase *entity.SalesPhase, now time.Time) (int, error) {
	attrs := []slog.Attr{
		slog.String("phase_id", phase.ID),
		slog.String("series_id", phase.SeriesID),
	}

	// Resolve audience via the shared helper: users with a Tracking journey on
	// any event of the phase's series.
	userIDs, err := ResolveSalesPhaseAudience(ctx, phase.SeriesID, uc.journeyRepo)
	if err != nil {
		return 0, err
	}
	if len(userIDs) == 0 {
		return 0, nil
	}

	// Hydrate users for timezone and language.
	// TODO(perf): batch user hydration when UserRepository gains ListByIDs.
	users := make([]*entity.User, 0, len(userIDs))
	for _, uid := range userIDs {
		u, err := uc.userRepo.Get(ctx, uid)
		if err != nil {
			uc.logger.Warn(ctx, "sales_reminder: failed to hydrate user; skipping",
				append(attrs, slog.String("user_id", uid), slog.String("error", err.Error()))...)
			continue
		}
		users = append(users, u)
	}
	if len(users) == 0 {
		return 0, nil
	}

	// Batch already-sent check: one query per phase instead of per (user,stage).
	onlyUserIDs := make([]string, len(users))
	for i, u := range users {
		onlyUserIDs[i] = u.ID
	}
	sentSet, err := uc.reminderRepo.ListSentStages(ctx, phase.ID, onlyUserIDs)
	if err != nil {
		uc.logger.Error(ctx, "sales_reminder: ListSentStages failed", err, attrs...)
		// Non-fatal: fall back to publishing (consumer will guard with AlreadySent).
		sentSet = make(map[string]map[entity.ReminderStage]bool)
	}

	var published int
	for _, stage := range allStages {
		for _, user := range users {
			if ctx.Err() != nil {
				return published, nil
			}

			// Consult the batched sent set.
			if sentSet[user.ID][stage] {
				continue
			}

			tz := userTimezone(user)
			fire, ok := scheduledFireTime(stage, phase, tz)
			if !ok {
				// Stage not applicable for this phase (zero timestamp or first-sight guard).
				continue
			}
			if now.Before(fire) {
				// Not yet time — a later scan will fire this.
				continue
			}

			payload := buildReminderPayload(phase, stage, user)
			data := entity.SalesPhaseReminderDueData{
				UserID:  user.ID,
				PhaseID: phase.ID,
				Stage:   int16(stage),
				Payload: payload,
			}
			if err := uc.publisher.PublishEvent(ctx, entity.SubjectSalesPhaseReminderDue, data); err != nil {
				uc.logger.Error(ctx, "sales_reminder: publish failed", err,
					append(attrs, slog.String("user_id", user.ID), slog.Int("stage", int(stage)))...)
				continue
			}
			// NOTE: RecordSent is intentionally NOT called here (fix #1).
			// The consumer (SalesReminderConsumer) is the sole writer of the
			// sent-log, after a confirmed successful push delivery. The
			// ListSentStages check above is a best-effort de-dup optimization.
			published++
		}
	}
	return published, nil
}

// scheduledFireTime returns the absolute wall-clock instant at which stage
// should fire for this phase in the user's timezone, with quiet-hours applied.
//
// ok=false means the stage is not applicable: either the milestone timestamp
// is zero (unknown/N/A), or the milestone was already past when the phase was
// first seen (base < phase.DiscoveredTime — the first-sight guard).
//
// Per stage, base trigger and deadline:
//   - APPLY_OPEN     : base = ApplyStartTime                    ; non-deadline.
//   - RESULT_DAY     : base = 09:00 in tz on day of LotteryResultTime ; non-deadline.
//   - APPLY_CLOSE_24H: base = ApplyEndTime.Add(-24h)             ; deadline = ApplyEndTime.
//   - APPLY_CLOSE_1H : base = ApplyEndTime.Add(-1h)              ; deadline = ApplyEndTime.
//
// Quiet window q(t): hour-in-tz >= 22 OR hour-in-tz < 8.
//
//   - Non-deadline in quiet: fire = next0800After(base).
//   - Non-deadline outside quiet: fire = base.
//   - Deadline outside quiet: fire = base.
//   - Deadline in quiet, next0800After(base) < deadline: fire = next0800After(base).
//   - Deadline in quiet, next0800After(base) >= deadline: fire = quietWindowStart(base)
//     (pre-quiet alert, always < deadline and before the window).
func scheduledFireTime(stage entity.ReminderStage, phase *entity.SalesPhase, tz *time.Location) (time.Time, bool) {
	var base, deadline time.Time
	isDeadline := false

	switch stage {
	case entity.ReminderStageApplyOpen:
		if phase.ApplyStartTime.IsZero() {
			return time.Time{}, false
		}
		base = phase.ApplyStartTime

	case entity.ReminderStageResultDay:
		if phase.LotteryResultTime.IsZero() {
			return time.Time{}, false
		}
		// 09:00 in the USER's timezone on the calendar day of LotteryResultTime.
		local := phase.LotteryResultTime.In(tz)
		base = time.Date(local.Year(), local.Month(), local.Day(), resultDayNotifyHour, 0, 0, 0, tz)

	case entity.ReminderStageApplyClose24H:
		if phase.ApplyEndTime.IsZero() {
			return time.Time{}, false
		}
		base = phase.ApplyEndTime.Add(-24 * time.Hour)
		deadline = phase.ApplyEndTime
		isDeadline = true

	case entity.ReminderStageApplyClose1H:
		if phase.ApplyEndTime.IsZero() {
			return time.Time{}, false
		}
		base = phase.ApplyEndTime.Add(-1 * time.Hour)
		deadline = phase.ApplyEndTime
		isDeadline = true

	default:
		return time.Time{}, false
	}

	// First-sight guard: if the base trigger was already in the past when the
	// phase was first persisted (phase.DiscoveredTime > base), do not fire retroactively.
	if !phase.DiscoveredTime.IsZero() && base.Before(phase.DiscoveredTime) {
		return time.Time{}, false
	}

	inQuiet := func(t time.Time) bool {
		h := t.In(tz).Hour()
		return h >= quietStartHour || h < quietEndHour
	}

	// next0800After returns the next 08:00 in tz that is strictly after t.
	// DST-safe: anchors via time.Date after AddDate so hour is re-confirmed.
	next0800After := func(t time.Time) time.Time {
		local := t.In(tz)
		cand := time.Date(local.Year(), local.Month(), local.Day(), quietEndHour, 0, 0, 0, tz)
		if !cand.After(t) {
			next := cand.AddDate(0, 0, 1).In(tz)
			cand = time.Date(next.Year(), next.Month(), next.Day(), quietEndHour, 0, 0, 0, tz)
		}
		return cand
	}

	// quietWindowStart returns the 22:00 that opened the quiet window containing t.
	// If t is already past midnight (i.e. hour < 8, still inside the window that
	// started the previous evening), it returns the previous day's 22:00.
	quietWindowStart := func(t time.Time) time.Time {
		local := t.In(tz)
		if local.Hour() >= quietStartHour {
			// Already at or past 22:00 today — window started today.
			return time.Date(local.Year(), local.Month(), local.Day(), quietStartHour, 0, 0, 0, tz)
		}
		// Hour < 8: inside a window that started the previous evening.
		prev := t.AddDate(0, 0, -1).In(tz)
		return time.Date(prev.Year(), prev.Month(), prev.Day(), quietStartHour, 0, 0, 0, tz)
	}

	if !inQuiet(base) {
		return base, true
	}

	// base is inside the quiet window.
	if !isDeadline {
		// Non-deadline: simply defer to next 08:00.
		return next0800After(base), true
	}

	// Deadline stage: prefer next 08:00 if it is still before the deadline.
	morning := next0800After(base)
	if morning.Before(deadline) {
		return morning, true
	}
	// Morning >= deadline: fire the pre-quiet alert (22:00 before the window).
	// quietWindowStart(base) is always strictly before base (which is inside the
	// window), and base < deadline, so the pre-quiet alert < deadline.
	return quietWindowStart(base), true
}

// userTimezone parses the user's IANA timezone. Never returns nil: falls back to
// Asia/Tokyo, then UTC if both LoadLocation calls fail (e.g. tzdata not embedded).
func userTimezone(u *entity.User) *time.Location {
	if u.TimeZone != "" {
		if tz, err := time.LoadLocation(u.TimeZone); err == nil {
			return tz
		}
	}
	if tz, err := time.LoadLocation(fallbackTimeZone); err == nil {
		return tz
	}
	return time.UTC
}

// buildReminderPayload constructs the per-recipient NotificationPayload for a
// reminder stage, with times formatted in the user's timezone and copy in the
// user's preferred language (default "en").
func buildReminderPayload(phase *entity.SalesPhase, stage entity.ReminderStage, user *entity.User) *entity.NotificationPayload {
	tz := userTimezone(user)
	lang := user.PreferredLanguage
	if lang == "" {
		lang = "en"
	}

	channelLabel := channelDisplayName(phase.Channel, phase.ProviderName, lang)
	url := phase.URL
	if url == "" {
		url = fmt.Sprintf("/series/%s", phase.SeriesID)
	}
	tag := fmt.Sprintf("sales-phase-%s-stage-%d", phase.ID, stage)

	switch stage {
	case entity.ReminderStageApplyOpen:
		timeStr := formatLocalTime(phase.ApplyStartTime, tz, lang)
		return entity.NewNotificationPayload(
			stageTitle(lang, "apply_open"),
			fmt.Sprintf(stageBody(lang, "apply_open"), channelLabel, timeStr),
			url,
			tag,
		)
	case entity.ReminderStageApplyClose24H:
		timeStr := formatLocalTime(phase.ApplyEndTime, tz, lang)
		return entity.NewNotificationPayload(
			stageTitle(lang, "apply_close_24h"),
			fmt.Sprintf(stageBody(lang, "apply_close_24h"), channelLabel, timeStr),
			url,
			tag,
		)
	case entity.ReminderStageApplyClose1H:
		timeStr := formatLocalTime(phase.ApplyEndTime, tz, lang)
		return entity.NewNotificationPayload(
			stageTitle(lang, "apply_close_1h"),
			fmt.Sprintf(stageBody(lang, "apply_close_1h"), channelLabel, timeStr),
			url,
			tag,
		)
	case entity.ReminderStageResultDay:
		timeStr := formatLocalTime(phase.LotteryResultTime, tz, lang)
		return entity.NewNotificationPayload(
			stageTitle(lang, "result_day"),
			fmt.Sprintf(stageBody(lang, "result_day"), channelLabel, timeStr),
			url,
			tag,
		)
	default:
		return entity.NewNotificationPayload("", "", url, tag)
	}
}

// formatLocalTime formats t in tz using a short locale-aware pattern.
func formatLocalTime(t time.Time, tz *time.Location, lang string) string {
	if t.IsZero() {
		return ""
	}
	local := t.In(tz)
	switch lang {
	case "ja":
		return local.Format("1月2日 15:04")
	default:
		return local.Format("Jan 2 15:04")
	}
}

// channelDisplayName returns a human-readable channel label for the
// notification body. When channel is UNSPECIFIED or the provider name is
// empty, a generic "ticket" label is used.
func channelDisplayName(ch entity.SalesChannel, providerName, lang string) string {
	if providerName != "" {
		return providerName
	}
	switch lang {
	case "ja":
		switch ch {
		case entity.SalesChannelFanClub:
			return "ファンクラブ"
		case entity.SalesChannelOfficial:
			return "公式"
		case entity.SalesChannelPlayguide:
			return "プレイガイド"
		case entity.SalesChannelCreditCard:
			return "クレジットカード"
		case entity.SalesChannelMobileCarrier:
			return "携帯キャリア"
		case entity.SalesChannelGeneral:
			return "一般"
		default:
			return "チケット"
		}
	default:
		switch ch {
		case entity.SalesChannelFanClub:
			return "Fan Club"
		case entity.SalesChannelOfficial:
			return "Official"
		case entity.SalesChannelPlayguide:
			return "Play Guide"
		case entity.SalesChannelCreditCard:
			return "Credit Card"
		case entity.SalesChannelMobileCarrier:
			return "Mobile Carrier"
		case entity.SalesChannelGeneral:
			return "General"
		default:
			return "Ticket"
		}
	}
}

// stageTitle returns the notification title for a given stage and language.
func stageTitle(lang, stage string) string {
	titles := map[string]map[string]string{
		"en": {
			"apply_open":      "Ticket Sales Open",
			"apply_close_24h": "Last Day to Apply",
			"apply_close_1h":  "1 Hour Left to Apply",
			"result_day":      "Lottery Results Today",
		},
		"ja": {
			"apply_open":      "チケット申込受付開始",
			"apply_close_24h": "申込締切まで24時間",
			"apply_close_1h":  "申込締切まで1時間",
			"result_day":      "抽選結果発表日",
		},
	}
	if m, ok := titles[lang]; ok {
		if t, ok := m[stage]; ok {
			return t
		}
	}
	if m, ok := titles["en"]; ok {
		return m[stage]
	}
	return ""
}

// stageBody returns the notification body template (%s: channel label, time string).
func stageBody(lang, stage string) string {
	bodies := map[string]map[string]string{
		"en": {
			"apply_open":      "%s sales open at %s",
			"apply_close_24h": "%s application closes tomorrow at %s",
			"apply_close_1h":  "%s application closes at %s — 1 hour left",
			"result_day":      "%s lottery results announced today (%s)",
		},
		"ja": {
			"apply_open":      "%sの申込受付が%sから始まります",
			"apply_close_24h": "%sの申込締切は%sです（あと24時間）",
			"apply_close_1h":  "%sの申込締切は%sです（あと1時間）",
			"result_day":      "%sの抽選結果は本日発表（%s）",
		},
	}
	if m, ok := bodies[lang]; ok {
		if b, ok := m[stage]; ok {
			return b
		}
	}
	if m, ok := bodies["en"]; ok {
		return m[stage]
	}
	return "%s %s"
}
