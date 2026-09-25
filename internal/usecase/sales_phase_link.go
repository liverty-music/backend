package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-logging/logging"
)

// dashboardURL is the deep-link target for a series-level notification when
// no event can be resolved for its series (e.g. the series was deleted after
// the phase/reminder was scheduled). The fan app has no series-level screen
// (frontend#630), so the dashboard is the safest generic landing page.
const dashboardURL = "/dashboard"

// ResolveSeriesLinkURL resolves the deep-link URL for a series-level
// notification (a sales-phase announcement, or a reminder whose phase has no
// application url): the concert-detail route of the series' earliest
// upcoming event, or its earliest event when none is upcoming. Falls back to
// dashboardURL when the series has no event, or when the lookup itself
// fails, so a repository error never blocks the notification from being
// sent.
//
// Both the announcement use case and the reminder scan call this function so
// the link-resolution logic cannot diverge, mirroring how
// [ResolveSalesPhaseAudience] is already shared between them.
func ResolveSeriesLinkURL(
	ctx context.Context,
	seriesID string,
	concertRepo entity.ConcertRepository,
	logger *logging.Logger,
) string {
	events, err := concertRepo.ListEventsBySeries(ctx, seriesID)
	if err != nil {
		logger.Warn(ctx, "sales_phase_link: failed to list events for series; falling back to dashboard",
			slog.String("series_id", seriesID),
			slog.String("error", err.Error()),
		)
		return dashboardURL
	}

	event := earliestUpcomingOrEarliestEvent(events)
	if event == nil {
		return dashboardURL
	}
	return fmt.Sprintf("/concerts/%s", event.ID)
}

// earliestUpcomingOrEarliestEvent returns the earliest event whose local date
// is today or later, or, if none is upcoming, the earliest event overall.
// Returns nil for an empty input.
func earliestUpcomingOrEarliestEvent(events []*entity.Event) *entity.Event {
	if len(events) == 0 {
		return nil
	}
	today := time.Now().UTC().Truncate(24 * time.Hour)

	var earliestOverall, earliestUpcoming *entity.Event
	for _, e := range events {
		if e == nil {
			continue
		}
		if earliestOverall == nil || eventIsEarlier(e, earliestOverall) {
			earliestOverall = e
		}
		if !e.LocalDate.Before(today) && (earliestUpcoming == nil || eventIsEarlier(e, earliestUpcoming)) {
			earliestUpcoming = e
		}
	}
	if earliestUpcoming != nil {
		return earliestUpcoming
	}
	return earliestOverall
}

// eventIsEarlier reports whether a is scheduled before b: by local date first,
// then by start time when the dates match, with an unknown (nil) start time
// ordered last.
func eventIsEarlier(a, b *entity.Event) bool {
	if !a.LocalDate.Equal(b.LocalDate) {
		return a.LocalDate.Before(b.LocalDate)
	}
	if a.StartTime == nil {
		return false
	}
	if b.StartTime == nil {
		return true
	}
	return a.StartTime.Before(*b.StartTime)
}
