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
	// fallbackTimeZone is used when a user has no time_zone set.
	fallbackTimeZone = "Asia/Tokyo"
)

// TicketSaleReminderUseCase scans the ticket sales, discovered and the
// Organizer's, for milestones that became due in the current scan window and
// publishes a SALES_PHASE.reminder_due event for each (user, sale, stage) not
// yet recorded in the sent-log.
type TicketSaleReminderUseCase interface {
	// ScanDueReminders runs one scan pass. It lists the sales with a pending
	// milestone, resolves each sale's audience, applies quiet hours, checks
	// the sent-log, and publishes due reminders. Returns the number of events
	// published.
	ScanDueReminders(ctx context.Context) (int, error)
}

type ticketSaleReminderUseCase struct {
	ticketSaleRepo entity.TicketSaleRepository
	reminderRepo   entity.TicketSaleReminderRepository
	journeyRepo    entity.TicketJourneyRepository
	userRepo       entity.UserRepository
	seriesRepo     entity.SeriesRepository
	workflows      Workflows
	// lookahead is the forward horizon passed to ListWithPendingMilestones.
	lookahead time.Duration
	// lookbackMargin is the grace period passed to ListWithPendingMilestones.
	lookbackMargin time.Duration
	logger         *logging.Logger
}

// Compile-time interface compliance check.
var _ TicketSaleReminderUseCase = (*ticketSaleReminderUseCase)(nil)

// reminderScanLookbackMargin is the default grace period. Include sales whose
// latest milestone fired up to 2 hours ago so a milestone that occurred just
// before this scan run is not silently dropped.
const reminderScanLookbackMargin = 2 * time.Hour

// NewTicketSaleReminderUseCase wires the reminder scan use case.
func NewTicketSaleReminderUseCase(
	ticketSaleRepo entity.TicketSaleRepository,
	reminderRepo entity.TicketSaleReminderRepository,
	journeyRepo entity.TicketJourneyRepository,
	userRepo entity.UserRepository,
	seriesRepo entity.SeriesRepository,
	workflows Workflows,
	lookahead time.Duration,
	logger *logging.Logger,
) TicketSaleReminderUseCase {
	return &ticketSaleReminderUseCase{
		ticketSaleRepo: ticketSaleRepo,
		reminderRepo:   reminderRepo,
		journeyRepo:    journeyRepo,
		userRepo:       userRepo,
		seriesRepo:     seriesRepo,
		workflows:      workflows,
		lookahead:      lookahead,
		lookbackMargin: reminderScanLookbackMargin,
		logger:         logger,
	}
}

// ScanDueReminders implements [TicketSaleReminderUseCase].
func (uc *ticketSaleReminderUseCase) ScanDueReminders(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	uc.logger.Info(ctx, "ticket_sale_reminder: starting scan", slog.String("now", now.Format(time.RFC3339)))

	sales, err := uc.ticketSaleRepo.ListWithPendingMilestones(ctx, uc.lookahead, uc.lookbackMargin)
	if err != nil {
		return 0, fmt.Errorf("ticket_sale_reminder: list sales: %w", err)
	}
	uc.logger.Info(ctx, "ticket_sale_reminder: sales in window", slog.Int("count", len(sales)))

	var totalPublished int
	for _, sale := range sales {
		if ctx.Err() != nil {
			break
		}
		n, err := uc.processSale(ctx, sale, now)
		if err != nil {
			uc.logger.Error(ctx, "ticket_sale_reminder: error processing sale", err,
				slog.String("ticket_sale_id", string(sale.ID)),
			)
			continue
		}
		totalPublished += n
	}

	uc.logger.Info(ctx, "ticket_sale_reminder: scan complete",
		slog.Int("sales_processed", len(sales)),
		slog.Int("reminders_published", totalPublished),
	)
	return totalPublished, nil
}

// allStages is the full ordered set of reminder stages evaluated each scan.
var allStages = []entity.ReminderStage{
	entity.ReminderStageApplyOpen,
	entity.ReminderStageApplyClose24H,
}

// saleAudience returns the fans to remind of a sale, each with the event the
// reminder links them to. A discovered sale reaches every fan tracking an
// event of its series. An Organizer's sale reaches only the fans tracking an
// event it names, because it may cover only some events of the tour.
func (uc *ticketSaleReminderUseCase) saleAudience(ctx context.Context, sale *entity.TicketSale) ([]*entity.SeriesTracker, error) {
	if sale.IsDiscovered() {
		return uc.journeyRepo.ListUserIDsTrackingSeries(ctx, sale.SeriesID)
	}
	return uc.journeyRepo.ListUserIDsTrackingEvents(ctx, sale.EventIDs)
}

// processSale evaluates all reminder stages for one sale and returns the
// number of reminder events published.
func (uc *ticketSaleReminderUseCase) processSale(ctx context.Context, sale *entity.TicketSale, now time.Time) (int, error) {
	attrs := []slog.Attr{
		slog.String("ticket_sale_id", string(sale.ID)),
		slog.String("series_id", sale.SeriesID),
	}

	trackers, err := uc.saleAudience(ctx, sale)
	if err != nil {
		return 0, fmt.Errorf("ticket_sale_reminder: list tracking users: %w", err)
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
			uc.logger.Warn(ctx, "ticket_sale_reminder: failed to hydrate user; skipping",
				append(attrs, slog.String("user_id", tr.UserID), slog.String("error", err.Error()))...)
			continue
		}
		recipients = append(recipients, recipient{user: u, linkEventID: tr.EventID})
	}
	if len(recipients) == 0 {
		return 0, nil
	}

	// Batch already-sent check: one query per sale instead of per (user,stage).
	onlyUserIDs := make([]string, len(recipients))
	for i, r := range recipients {
		onlyUserIDs[i] = r.user.ID
	}
	sentSet, err := uc.reminderRepo.ListSentStages(ctx, sale.ID, onlyUserIDs)
	if err != nil {
		uc.logger.Error(ctx, "ticket_sale_reminder: ListSentStages failed", err, attrs...)
		// Non-fatal: fall back to publishing (consumer will guard with AlreadySent).
		sentSet = make(map[string]map[entity.ReminderStage]bool)
	}

	// The series title is read once per sale; it is the last line of every
	// reminder text. It is read lazily, so a sale with nothing due costs no
	// lookup.
	var seriesTitle *string
	title := func() (string, error) {
		if seriesTitle == nil {
			series, err := uc.seriesRepo.Get(ctx, sale.SeriesID)
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
			fire, expiry, ok := scheduledFireTime(stage, sale, tz)
			if !ok {
				// Stage not applicable for this sale (method, unknown
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
			payload := buildReminderPayload(sale, stage, user, seriesTitle, r.linkEventID)
			data := entity.TicketSaleReminderDueData{
				UserID:       user.ID,
				TicketSaleID: string(sale.ID),
				Stage:        int16(stage),
				Payload:      payload,
			}
			if err := uc.workflows.Start(ctx, WorkflowDeliverReminder, reminderWorkflowID(data, now), data); err != nil {
				uc.logger.Error(ctx, "ticket_sale_reminder: start failed", err,
					append(attrs, slog.String("user_id", user.ID), slog.Int("stage", int(stage)))...)
				continue
			}
			// RecordSent is intentionally NOT called here: the delivery use
			// case is the sole writer of the sent-log, after a confirmed
			// delivery. The ListSentStages check above is a best-effort
			// de-dup optimization.
			published++
		}
	}
	return published, nil
}

// scheduledFireTime returns the absolute instant at which stage should fire
// for this sale in the user's timezone, with quiet hours applied, plus the
// expiry instant at or after which the stage must no longer be sent.
//
// ok=false means the stage does not apply (see [entity.ReminderStage.Anchor])
// or, for a discovered sale, its anchor was already past when the sale was
// first stored (the first-sight guard). An Organizer's sale has no such
// guard: a sale created after it opened still gets APPLY_OPEN.
//
// Per stage and method:
//
//   - APPLY_OPEN, FirstCome: anchor = start - 30m; expiry = start. In quiet
//     hours it fires earlier, at 21:00 on the local date the quiet window
//     started (the same day when the anchor is at or after 22:00, the
//     previous day when it is before 08:00), so it still arrives before the
//     sale opens.
//   - APPLY_OPEN, Lottery: anchor = start; expiry = end.
//   - APPLY_CLOSE_24H, Lottery: anchor = end - 24h; expiry = end. The next
//     08:00 after an anchor in quiet hours is always before the end, because
//     the anchor is 24 hours before it.
//
// Every stage other than the first-come APPLY_OPEN that falls in the quiet
// window (22:00–08:00 in tz) fires at the next 08:00.
func scheduledFireTime(stage entity.ReminderStage, sale *entity.TicketSale, tz *time.Location) (fire time.Time, expiry time.Time, ok bool) {
	base, ok := stage.Anchor(sale)
	if !ok {
		return time.Time{}, time.Time{}, false
	}
	firstComeOpen := false

	switch stage {
	case entity.ReminderStageApplyOpen:
		if sale.Method == entity.TicketSaleMethodFirstCome {
			expiry = sale.SaleStartTime
			firstComeOpen = true
		} else {
			expiry = sale.SaleEndTime
		}
	case entity.ReminderStageApplyClose24H:
		expiry = sale.SaleEndTime
	}

	// First-sight guard: a discovered sale whose anchor was already in the
	// past when it was first stored does not fire retroactively.
	if sale.DiscoveredTime != nil && base.Before(*sale.DiscoveredTime) {
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
// when quiet hours move the reminder. The same copy serves a discovered sale
// and an Organizer's sale.
func buildReminderPayload(
	sale *entity.TicketSale,
	stage entity.ReminderStage,
	user *entity.User,
	seriesTitle string,
	linkEventID string,
) *entity.NotificationPayload {
	tz := userTimezone(user)
	lang := copyLanguage(user.PreferredLanguage)
	url := concertLinkURL(linkEventID)
	tag := fmt.Sprintf("ticket-sale-%s-stage-%d", sale.ID, stage)

	var key string
	var at time.Time
	switch {
	case stage == entity.ReminderStageApplyOpen && sale.Method == entity.TicketSaleMethodFirstCome:
		key, at = copyApplyOpenFirstCome, sale.SaleStartTime
	case stage == entity.ReminderStageApplyOpen:
		key, at = copyApplyOpenLottery, sale.SaleEndTime
	case stage == entity.ReminderStageApplyClose24H:
		key, at = copyApplyClose24HLottery, sale.SaleEndTime
	default:
		return entity.NewNotificationPayload("", "", url, tag)
	}
	title, text := ticketSaleText(lang, key, formatSalesTime(at, tz, lang), seriesTitle)
	return entity.NewNotificationPayload(title, text, url, tag)
}
