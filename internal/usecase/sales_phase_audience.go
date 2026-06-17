package usecase

import (
	"context"
	"fmt"

	"github.com/liverty-music/backend/internal/entity"
)

// ResolveSalesPhaseAudience resolves the audience for a sales-phase notification
// from explicit fan intent: the distinct users who have a Tracking ticket
// journey on any event of the phase's series. A Tracking journey is a "notify
// me about this sale" signal, so the audience no longer depends on covered-event
// performers, follower lists, or hype-level proximity (geographic relevance was
// already applied upstream when the fan chose to track the concert).
//
// Both the announcement consumer and the reminder scan call this function so the
// audience logic cannot diverge. An empty result (no one tracking) returns
// (nil, nil).
func ResolveSalesPhaseAudience(
	ctx context.Context,
	seriesID string,
	journeyRepo entity.TicketJourneyRepository,
) ([]string, error) {
	userIDs, err := journeyRepo.ListUserIDsTrackingSeries(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("sales_phase_audience: list tracking users for series %s: %w", seriesID, err)
	}
	return userIDs, nil
}
