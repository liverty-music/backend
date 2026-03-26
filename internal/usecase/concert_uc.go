package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/liverty-music/backend/internal/entity"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// ConcertUseCase defines the interface for concert-related business logic.
type ConcertUseCase interface {
	// ListByArtist returns all concerts for a specific artist.
	//
	// # Possible errors
	//
	//  - NotFound: If the artist does not exist.
	//  - Internal: database query failure.
	ListByArtist(ctx context.Context, artistID string) ([]*entity.Concert, error)

	// ListByFollower returns all concerts for artists followed by the given user.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	ListByFollower(ctx context.Context, userID string) ([]*entity.Concert, error)

	// ListByFollowerGrouped returns concerts for followed artists, grouped by date
	// and classified into home/nearby/away lanes based on proximity to the user's home.
	//
	// # Possible errors
	//
	//  - NotFound: If the user does not exist.
	ListByFollowerGrouped(ctx context.Context, userID string, home *entity.Home) ([]*entity.ProximityGroup, error)

	// ListWithProximity returns concerts for the specified artists, grouped by date
	// and classified by proximity to the given home.
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	ListWithProximity(ctx context.Context, artistIDs []string, home *entity.Home) ([]*entity.ProximityGroup, error)

	// SearchNewConcerts discovers new concerts for the given artist synchronously.
	// It returns the newly discovered concerts after deduplication against
	// already-known upcoming events.
	//
	// # Possible errors
	//
	//  - NotFound: If the artist does not exist.
	//  - Internal: search or database failure.
	SearchNewConcerts(ctx context.Context, artistID string) ([]*entity.Concert, error)
}

// concertUseCase implements the ConcertUseCase interface.
type concertUseCase struct {
	artistRepo       entity.ArtistRepository
	concertRepo      entity.ConcertRepository
	venueRepo        entity.VenueRepository
	searchLogRepo    entity.SearchLogRepository
	concertSearcher  entity.ConcertSearcher
	centroidResolver CentroidResolver
	publisher        EventPublisher
	logger           *logging.Logger
}

// searchCacheTTL is the duration for which a completed search log is considered fresh.
const searchCacheTTL = 24 * time.Hour

// pendingTimeout is the maximum age of a pending search log before it is
// considered stale and treated as failed (self-healing for crashed workers).
const pendingTimeout = 3 * time.Minute

// statusUpdateTimeout is the context timeout for search log status updates.
// Uses a fresh context.Background() to ensure updates succeed even when the
// parent context (Gemini API call) has expired.
const statusUpdateTimeout = 5 * time.Second

// Compile-time interface compliance check
var _ ConcertUseCase = (*concertUseCase)(nil)

// NewConcertUseCase creates a new concert use case.
// It orchestrates concert searching, retrieval, and event publishing.
func NewConcertUseCase(
	artistRepo entity.ArtistRepository,
	concertRepo entity.ConcertRepository,
	venueRepo entity.VenueRepository,
	searchLogRepo entity.SearchLogRepository,
	concertSearcher entity.ConcertSearcher,
	centroidResolver CentroidResolver,
	publisher EventPublisher,
	logger *logging.Logger,
) ConcertUseCase {
	return &concertUseCase{
		artistRepo:       artistRepo,
		concertRepo:      concertRepo,
		venueRepo:        venueRepo,
		searchLogRepo:    searchLogRepo,
		concertSearcher:  concertSearcher,
		centroidResolver: centroidResolver,
		publisher:        publisher,
		logger:           logger,
	}
}

// ListByArtist returns all concerts for a specific artist.
func (uc *concertUseCase) ListByArtist(ctx context.Context, artistID string) ([]*entity.Concert, error) {
	concerts, err := uc.concertRepo.ListByArtist(ctx, artistID, false)
	if err != nil {
		return nil, err
	}

	return concerts, nil
}

// ListByFollower returns all concerts for artists followed by the given user.
func (uc *concertUseCase) ListByFollower(ctx context.Context, userID string) ([]*entity.Concert, error) {
	return uc.concertRepo.ListByFollower(ctx, userID)
}

// ListByFollowerGrouped returns concerts for followed artists, grouped by date
// and classified into home/nearby/away lanes based on proximity to the user's home.
func (uc *concertUseCase) ListByFollowerGrouped(ctx context.Context, userID string, home *entity.Home) ([]*entity.ProximityGroup, error) {
	concerts, err := uc.concertRepo.ListByFollower(ctx, userID)
	if err != nil {
		return nil, err
	}

	return entity.GroupByDateAndProximity(concerts, home), nil
}

// ListWithProximity returns concerts for the specified artists, grouped by date
// and classified by proximity to the given home.
func (uc *concertUseCase) ListWithProximity(ctx context.Context, artistIDs []string, home *entity.Home) ([]*entity.ProximityGroup, error) {
	if home != nil && home.Centroid == nil {
		if lat, lng, err := uc.centroidResolver.ResolveCentroid(home); err == nil {
			home.Centroid = &entity.Coordinates{Latitude: lat, Longitude: lng}
		}
	}

	concerts, err := uc.concertRepo.ListByArtists(ctx, artistIDs)
	if err != nil {
		return nil, fmt.Errorf("list concerts by artists: %w", err)
	}

	return entity.GroupByDateAndProximity(concerts, home), nil
}

// SearchNewConcerts discovers new concerts for the given artist synchronously.
// It returns the newly discovered concerts after deduplication against
// already-known upcoming events.
func (uc *concertUseCase) SearchNewConcerts(ctx context.Context, artistID string) ([]*entity.Concert, error) {
	// Check search log — skip if recently completed or currently pending.
	searchLog, err := uc.searchLogRepo.GetByArtistID(ctx, artistID)
	if err != nil && !errors.Is(err, apperr.ErrNotFound) {
		return nil, fmt.Errorf("failed to get search log: %w", err)
	}
	if searchLog != nil {
		now := time.Now()
		if searchLog.IsFresh(now, searchCacheTTL) {
			uc.logger.Debug(ctx, "skipping external search, recently searched",
				slog.String("artist_id", artistID),
				slog.Time("search_time", searchLog.SearchTime),
			)
			return nil, nil
		}
		if searchLog.IsPending(now, pendingTimeout) {
			uc.logger.Debug(ctx, "skipping external search, already pending",
				slog.String("artist_id", artistID),
			)
			return nil, nil
		}
	}

	// Mark as pending. If this fails, abort — without a pending row,
	// downstream UpdateStatus calls silently no-op (0 rows affected).
	if err := uc.searchLogRepo.Upsert(ctx, artistID, entity.SearchLogStatusPending); err != nil {
		return nil, fmt.Errorf("failed to mark search as pending: %w", err)
	}

	return uc.executeSearch(ctx, artistID)
}

// executeSearch performs the actual Gemini search, deduplication, and event publishing.
// It returns the newly discovered concerts and updates the search log status on exit.
//
// Deduplication uses date-only matching: one event per artist per date.
// An artist cannot perform at two venues simultaneously on the same day.
//
// The seenDate set tracks dates already occupied by existing or earlier-scraped
// concerts. Any scraped concert whose date is already seen is filtered out,
// regardless of start_at value. The DB constraint (artist_id, local_event_date)
// provides the same guarantee; this application-level check avoids unnecessary
// publish/UPSERT round-trips.
func (uc *concertUseCase) executeSearch(ctx context.Context, artistID string) (_ []*entity.Concert, err error) {
	defer func() {
		if err != nil {
			uc.markSearchFailed(ctx, artistID)
		} else {
			uc.markSearchCompleted(ctx, artistID)
		}
	}()

	// Get Artist
	artist, err := uc.artistRepo.Get(ctx, artistID)
	if err != nil {
		return nil, fmt.Errorf("failed to get artist: %w", err)
	}

	// Get Official Site — missing site is not an error; search continues with nil
	site, err := uc.artistRepo.GetOfficialSite(ctx, artistID)
	if err != nil && !errors.Is(err, apperr.ErrNotFound) {
		return nil, fmt.Errorf("failed to get official site: %w", err)
	}
	if errors.Is(err, apperr.ErrNotFound) {
		site = nil
		err = nil
	}

	// Get existing upcoming concerts for deduplication
	existing, err := uc.concertRepo.ListByArtist(ctx, artistID, true)
	if err != nil {
		return nil, fmt.Errorf("failed to list existing concerts: %w", err)
	}

	// Search new concerts via external API (deadline inherited from HandlerTimeout)
	scraped, err := uc.concertSearcher.Search(ctx, artist, site, time.Now())
	if err != nil {
		return nil, fmt.Errorf("failed to search concerts via external API: %w", err)
	}

	// Deduplicate: one event per artist per date.
	// FilterNew handles both cross-batch dedup (against existing) and within-batch dedup.
	newScraped := entity.ScrapedConcerts(scraped).FilterNew(existing)
	if filtered := len(scraped) - len(newScraped); filtered > 0 {
		uc.logger.Debug(ctx, "filtered existing/duplicate events (same date)",
			slog.String("artist_id", artistID),
			slog.Int("filtered_count", filtered),
		)
	}

	// Publish concert.discovered.v1 event (artist-level batch)
	if len(newScraped) == 0 {
		uc.logger.Debug(ctx, "no new concerts after deduplication",
			slog.String("artist_id", artistID),
		)
		return nil, nil
	}

	eventData := entity.ConcertDiscoveredData{
		ArtistID:   artistID,
		ArtistName: artist.Name,
		Concerts:   newScraped,
	}

	if err := uc.publisher.PublishEvent(ctx, entity.SubjectConcertDiscovered, eventData); err != nil {
		uc.logger.Error(ctx, "failed to publish concert.discovered event", err,
			slog.String("artist_id", artistID),
			slog.Int("concert_count", len(newScraped)),
		)
		// Non-fatal: CronJob will re-discover on next run.
		// The defer will call markSearchFailed because err != nil.
		return nil, err
	}

	uc.logger.Info(ctx, "published concert.discovered event",
		slog.String("artist_id", artistID),
		slog.String("artist_name", artist.Name),
		slog.Int("concert_count", len(newScraped)),
	)

	// Build Concert entities from the deduplicated scraped data to return to the caller.
	concerts := make([]*entity.Concert, 0, len(newScraped))
	for _, s := range newScraped {
		concerts = append(concerts, s.ToConcert(artistID, "", ""))
	}
	return concerts, nil
}

// markSearchCompleted updates the search log status to completed.
// It uses a fresh context to ensure the update succeeds even when the caller's
// context has expired (e.g., after a long Gemini API call).
func (uc *concertUseCase) markSearchCompleted(ctx context.Context, artistID string) {
	updateCtx, cancel := context.WithTimeout(context.Background(), statusUpdateTimeout)
	defer cancel()

	if err := uc.searchLogRepo.UpdateStatus(updateCtx, artistID, entity.SearchLogStatusCompleted); err != nil {
		uc.logger.Error(ctx, "failed to mark search as completed", err, slog.String("artist_id", artistID))
	}
}

// markSearchFailed updates the search log status to failed.
// It uses a fresh context to ensure the update succeeds even when the caller's
// context has expired (e.g., after a long Gemini API call).
func (uc *concertUseCase) markSearchFailed(ctx context.Context, artistID string) {
	updateCtx, cancel := context.WithTimeout(context.Background(), statusUpdateTimeout)
	defer cancel()

	if err := uc.searchLogRepo.UpdateStatus(updateCtx, artistID, entity.SearchLogStatusFailed); err != nil {
		uc.logger.Error(ctx, "failed to mark search as failed", err, slog.String("artist_id", artistID))
	}
}
