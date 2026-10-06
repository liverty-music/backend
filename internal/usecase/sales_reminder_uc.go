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
	// preQuietAlertHour is one hour before quietStartHour (21:00 local). A
	// first-come APPLY_OPEN reminder that would land inside quiet hours fires
	// here, the evening before the window, so it still arrives before the sale
	// opens.
	preQuietAlertHour = quietStartHour - 1
	// firstComeOpenLead is how long before a first-come sale opens its
	// APPLY_OPEN reminder is anchored.
	firstComeOpenLead = 30 * time.Minute
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
	seriesRepo     entity.SeriesRepository
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
	seriesRepo entity.SeriesRepository,
	publisher EventPublisher,
	lookahead time.Duration,
	logger *logging.Logger,
) SalesReminderUseCase {
	return &salesReminderUseCase{
		salesPhaseRepo: salesPhaseRepo,
		reminderRepo:   reminderRepo,
		journeyRepo:    journeyRepo,
		userRepo:       userRepo,
		seriesRepo:     seriesRepo,
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
	// any event of the phase's series, each with the event to link them to.
	trackers, err := ResolveSalesPhaseAudience(ctx, phase.SeriesID, uc.journeyRepo)
	if err != nil {
		return 0, err
	}
	if len(trackers) == 0 {
		return 0, nil
	}

	// Hydrate users for timezone and language.
	// TODO(perf): batch user hydration when UserRepository gains ListByIDs.
	type recipient struct {
		user        *entity.User
		linkEventID string
	}
	recipients := make([]recipient, 0, len(trackers))
	for _, tr := range trackers {
		u, err := uc.userRepo.Get(ctx, tr.UserID)
		if err != nil {
			uc.logger.Warn(ctx, "sales_reminder: failed to hydrate user; skipping",
				append(attrs, slog.String("user_id", tr.UserID), slog.String("error", err.Error()))...)
			continue
		}
		recipients = append(recipients, recipient{user: u, linkEventID: tr.EventID})
	}
	if len(recipients) == 0 {
		return 0, nil
	}

	// Batch already-sent check: one query per phase instead of per (user,stage).
	onlyUserIDs := make([]string, len(recipients))
	for i, r := range recipients {
		onlyUserIDs[i] = r.user.ID
	}
	sentSet, err := uc.reminderRepo.ListSentStages(ctx, phase.ID, onlyUserIDs)
	if err != nil {
		uc.logger.Error(ctx, "sales_reminder: ListSentStages failed", err, attrs...)
		// Non-fatal: fall back to publishing (consumer will guard with AlreadySent).
		sentSet = make(map[string]map[entity.ReminderStage]bool)
	}

	// The series title is read once per phase; it is the last line of every
	// reminder text. It is read lazily, so a phase with nothing due costs no
	// lookup.
	var seriesTitle *string
	title := func() (string, error) {
		if seriesTitle == nil {
			series, err := uc.seriesRepo.Get(ctx, phase.SeriesID)
			if err != nil {
				return "", err
			}
			seriesTitle = &series.Title
		}
		return *seriesTitle, nil
	}

	var published int
	for _, stage := range allStages {
		for _, r := range recipients {
			if ctx.Err() != nil {
				return published, nil
			}
			user := r.user

			// Consult the batched sent set.
			if sentSet[user.ID][stage] {
				continue
			}

			tz := userTimezone(user)
			fire, expiry, ok := scheduledFireTime(stage, phase, tz)
			if !ok {
				// Stage not applicable for this phase (method, unknown
				// milestone, or the first-sight guard).
				continue
			}
			if now.Before(fire) {
				// Not yet time — a later scan will fire this.
				continue
			}
			if !now.Before(expiry) {
				// The reminder's moment has passed (sale opened, application
				// window closed, or the result day already ended) — do not
				// send a stale reminder.
				continue
			}

			seriesTitle, err := title()
			if err != nil {
				return published, err
			}
			payload := buildReminderPayload(phase, stage, user, seriesTitle, r.linkEventID)
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

// scheduledFireTime returns the absolute instant at which stage should fire
// for this phase in the user's timezone, with quiet hours applied, plus the
// expiry instant at or after which the stage must no longer be sent.
//
// ok=false means the stage does not apply: the phase's method has no such
// stage, its milestone is unknown, or its anchor was already past when the
// phase was first seen (anchor < phase.DiscoveredTime — the first-sight guard).
//
// Per stage and method (design D9):
//
//   - APPLY_OPEN, FIRST_COME: anchor = ApplyStartTime - 30m; expiry =
//     ApplyStartTime. In quiet hours it fires earlier, at 21:00 on the local
//     date the quiet window started (the same day when the anchor is at or
//     after 22:00, the previous day when it is before 08:00), so it still
//     arrives before the sale opens.
//   - APPLY_OPEN, LOTTERY: anchor = ApplyStartTime; expiry = ApplyEndTime.
//   - APPLY_CLOSE_24H, LOTTERY: anchor = ApplyEndTime - 24h; expiry =
//     ApplyEndTime. The next 08:00 after an anchor in quiet hours is always
//     before the end, because the anchor is 24 hours before it.
//   - RESULT_DAY, LOTTERY with a result time: fires at 09:00 in tz on the day
//     of LotteryResultTime and expires at the end of that local day.
//
// Every stage other than the first-come APPLY_OPEN that falls in the quiet
// window (22:00–08:00 in tz) fires at the next 08:00.
func scheduledFireTime(stage entity.ReminderStage, phase *entity.SalesPhase, tz *time.Location) (fire time.Time, expiry time.Time, ok bool) {
	var base time.Time
	firstComeOpen := false

	switch {
	case stage == entity.ReminderStageApplyOpen && phase.Method == entity.SalesMethodFirstCome:
		if phase.ApplyStartTime.IsZero() {
			return time.Time{}, time.Time{}, false
		}
		base = phase.ApplyStartTime.Add(-firstComeOpenLead)
		expiry = phase.ApplyStartTime
		firstComeOpen = true

	case stage == entity.ReminderStageApplyOpen && phase.Method == entity.SalesMethodLottery:
		if phase.ApplyStartTime.IsZero() || phase.ApplyEndTime.IsZero() {
			return time.Time{}, time.Time{}, false
		}
		base = phase.ApplyStartTime
		expiry = phase.ApplyEndTime

	case stage == entity.ReminderStageApplyClose24H && phase.Method == entity.SalesMethodLottery:
		if phase.ApplyEndTime.IsZero() {
			return time.Time{}, time.Time{}, false
		}
		base = phase.ApplyEndTime.Add(-24 * time.Hour)
		expiry = phase.ApplyEndTime

	case stage == entity.ReminderStageResultDay && phase.Method == entity.SalesMethodLottery:
		if phase.LotteryResultTime.IsZero() {
			return time.Time{}, time.Time{}, false
		}
		// 09:00 in the USER's timezone on the calendar day of LotteryResultTime.
		local := phase.LotteryResultTime.In(tz)
		base = time.Date(local.Year(), local.Month(), local.Day(), resultDayNotifyHour, 0, 0, 0, tz)
		// Valid only through the end of that local calendar day.
		expiry = time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, tz)

	default:
		return time.Time{}, time.Time{}, false
	}

	// First-sight guard: if the anchor was already in the past when the phase
	// was first persisted (phase.DiscoveredTime > base), do not fire retroactively.
	if !phase.DiscoveredTime.IsZero() && base.Before(phase.DiscoveredTime) {
		return time.Time{}, time.Time{}, false
	}

	local := base.In(tz)
	if local.Hour() < quietStartHour && local.Hour() >= quietEndHour {
		return base, expiry, true
	}

	if firstComeOpen {
		// 21:00 on the local date the quiet window containing base started.
		day := local
		if local.Hour() < quietEndHour {
			day = base.AddDate(0, 0, -1).In(tz)
		}
		return time.Date(day.Year(), day.Month(), day.Day(), preQuietAlertHour, 0, 0, 0, tz), expiry, true
	}

	// The next 08:00 in tz strictly after base. DST-safe: anchors via
	// time.Date after AddDate so the hour is re-confirmed.
	morning := time.Date(local.Year(), local.Month(), local.Day(), quietEndHour, 0, 0, 0, tz)
	if !morning.After(base) {
		next := morning.AddDate(0, 0, 1).In(tz)
		morning = time.Date(next.Year(), next.Month(), next.Day(), quietEndHour, 0, 0, 0, tz)
	}
	return morning, expiry, true
}

// userTimezone returns the user's time zone, falling back to Asia/Tokyo when
// it is unset or not recognised.
func userTimezone(u *entity.User) *time.Location {
	return timeZoneOf(u.TimeZone)
}

// buildReminderPayload builds the per-recipient NotificationPayload for a
// reminder stage, with times in the user's time zone, copy in the user's
// language, the series title on its last line, and a link to the user's
// tracked event. The text states absolute times only, so it stays correct
// when quiet hours move the reminder.
func buildReminderPayload(
	phase *entity.SalesPhase,
	stage entity.ReminderStage,
	user *entity.User,
	seriesTitle string,
	linkEventID string,
) *entity.NotificationPayload {
	tz := userTimezone(user)
	lang := copyLanguage(user.PreferredLanguage)
	url := concertLinkURL(linkEventID)
	tag := fmt.Sprintf("sales-phase-%s-stage-%d", phase.ID, stage)

	var key string
	var at time.Time
	switch {
	case stage == entity.ReminderStageApplyOpen && phase.Method == entity.SalesMethodFirstCome:
		key, at = copyApplyOpenFirstCome, phase.ApplyStartTime
	case stage == entity.ReminderStageApplyOpen:
		key, at = copyApplyOpenLottery, phase.ApplyEndTime
	case stage == entity.ReminderStageApplyClose24H:
		key, at = copyApplyClose24HLottery, phase.ApplyEndTime
	case stage == entity.ReminderStageResultDay:
		key, at = copyResultDayLottery, phase.LotteryResultTime
	default:
		return entity.NewNotificationPayload("", "", url, tag)
	}
	title, text := salesPhaseText(lang, key, formatSalesTime(at, tz, lang), seriesTitle)
	return entity.NewNotificationPayload(title, text, url, tag)
}
