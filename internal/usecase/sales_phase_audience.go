package usecase

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-logging/logging"
)

// ResolveSalesPhaseAudience resolves the audience for a sales phase notification:
//
//  1. Load covered events (with Venue + Performers populated by ListByIDs).
//  2. Build the venueAreas map from Venue.AdminArea values.
//  3. For each unique performing artist, list followers and apply the full
//     ShouldNotify proximity filter — identical to NotifyNewConcerts.
//
// Returns the hydrated concerts (for caller convenience) and the unique
// filtered follower user IDs. Both the reminder scan and the announcement
// consumer call this function so the audience logic cannot diverge.
func ResolveSalesPhaseAudience(
	ctx context.Context,
	phase *entity.SalesPhase,
	concertRepo entity.ConcertRepository,
	followRepo entity.FollowRepository,
	attrs []slog.Attr,
	logger *logging.Logger,
) ([]*entity.Concert, []string, error) {
	if len(phase.CoveredEventIDs) == 0 {
		return nil, nil, nil
	}

	concerts, err := concertRepo.ListByIDs(ctx, phase.CoveredEventIDs)
	if err != nil {
		return nil, nil, fmt.Errorf("sales_phase_audience: list concerts for phase %s: %w", phase.ID, err)
	}

	// Build venue-areas set for HypeHome.ShouldNotify — mirrors NotifyNewConcerts.
	venueAreas := make(map[string]struct{})
	for _, c := range concerts {
		if c.Venue != nil && c.Venue.AdminArea != nil {
			venueAreas[*c.Venue.AdminArea] = struct{}{}
		}
	}

	// Collect unique artist IDs across covered events.
	artistSeen := make(map[string]bool)
	var artistIDs []string
	for _, c := range concerts {
		for _, p := range c.Performers {
			if p == nil || artistSeen[p.ID] {
				continue
			}
			artistSeen[p.ID] = true
			artistIDs = append(artistIDs, p.ID)
		}
	}
	if len(artistIDs) == 0 {
		logger.Warn(ctx, "sales_phase_audience: no performers found for covered events", attrs...)
		return concerts, nil, nil
	}

	// Collect unique follower user IDs, applying the full ShouldNotify gate.
	userSeen := make(map[string]bool)
	var userIDs []string
	for _, artistID := range artistIDs {
		followers, err := followRepo.ListFollowers(ctx, artistID)
		if err != nil {
			return nil, nil, fmt.Errorf("sales_phase_audience: list followers for artist %s: %w", artistID, err)
		}
		for _, f := range followers {
			if f.User == nil || userSeen[f.User.ID] {
				continue
			}
			if !f.Hype.ShouldNotify(f.User.Home, venueAreas, concerts) {
				continue
			}
			userSeen[f.User.ID] = true
			userIDs = append(userIDs, f.User.ID)
		}
	}
	return concerts, userIDs, nil
}
