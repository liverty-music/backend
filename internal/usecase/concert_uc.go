package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// ConcertUseCase defines the interface for concert-related business logic.
type ConcertUseCase interface {
	// ListByArtist returns all concerts for a specific artist.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the artist ID is empty.
	ListByArtist(ctx context.Context, artistID string) ([]*entity.Concert, error)

	// ListByFollower returns all concerts for artists followed by the given user.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the external user ID is empty.
	//  - NotFound: If the user does not exist.
	ListByFollower(ctx context.Context, externalUserID string) ([]*entity.Concert, error)

	// SearchNewConcerts discovers new concerts using external sources and publishes
	// a concert.discovered.v1 event for downstream processing. Concert persistence,
	// notifications, and venue enrichment are handled by event consumers.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the artist ID is empty or artist/site not found.
	//  - Unavailable: If the external search service is down.
	SearchNewConcerts(ctx context.Context, artistID string) error
}

// concertUseCase implements the ConcertUseCase interface.
type concertUseCase struct {
	artistRepo      entity.ArtistRepository
	concertRepo     entity.ConcertRepository
	venueRepo       entity.VenueRepository
	userRepo        entity.UserRepository
	searchLogRepo   entity.SearchLogRepository
	concertSearcher entity.ConcertSearcher
	publisher       message.Publisher
	logger          *logging.Logger
}

// searchCacheTTL is the duration for which a search log is considered fresh.
const searchCacheTTL = 24 * time.Hour

// Compile-time interface compliance check
var _ ConcertUseCase = (*concertUseCase)(nil)

// NewConcertUseCase creates a new concert use case.
// It orchestrates concert searching, retrieval, and event publishing.
func NewConcertUseCase(
	artistRepo entity.ArtistRepository,
	concertRepo entity.ConcertRepository,
	venueRepo entity.VenueRepository,
	userRepo entity.UserRepository,
	searchLogRepo entity.SearchLogRepository,
	concertSearcher entity.ConcertSearcher,
	publisher message.Publisher,
	logger *logging.Logger,
) ConcertUseCase {
	return &concertUseCase{
		artistRepo:      artistRepo,
		concertRepo:     concertRepo,
		venueRepo:       venueRepo,
		userRepo:        userRepo,
		searchLogRepo:   searchLogRepo,
		concertSearcher: concertSearcher,
		publisher:       publisher,
		logger:          logger,
	}
}

// ListByArtist returns all concerts for a specific artist.
func (uc *concertUseCase) ListByArtist(ctx context.Context, artistID string) ([]*entity.Concert, error) {
	if artistID == "" {
		return nil, apperr.New(codes.InvalidArgument, "artist ID is required")
	}

	concerts, err := uc.concertRepo.ListByArtist(ctx, artistID, false)
	if err != nil {
		return nil, err
	}

	return concerts, nil
}

// ListByFollower returns all concerts for artists followed by the given user.
func (uc *concertUseCase) ListByFollower(ctx context.Context, externalUserID string) ([]*entity.Concert, error) {
	if externalUserID == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID is required")
	}

	internalUserID, err := uc.resolveUserID(ctx, externalUserID)
	if err != nil {
		return nil, err
	}

	return uc.concertRepo.ListByFollower(ctx, internalUserID)
}

// resolveUserID maps an external identity (Zitadel sub claim) to the internal user UUID.
func (uc *concertUseCase) resolveUserID(ctx context.Context, externalID string) (string, error) {
	user, err := uc.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return "", fmt.Errorf("resolve user by external ID: %w", err)
	}
	return user.ID, nil
}

// SearchNewConcerts discovers new concerts using external sources and publishes
// a concert.discovered.v1 event containing the deduplicated batch for downstream consumers.
// If the artist was searched within the last 24 hours, it skips the external API call.
func (uc *concertUseCase) SearchNewConcerts(ctx context.Context, artistID string) error {
	if artistID == "" {
		return apperr.New(codes.InvalidArgument, "artist ID is required")
	}

	// 1. Check search log — skip external API if recently searched
	searchLog, err := uc.searchLogRepo.GetByArtistID(ctx, artistID)
	if err != nil && !errors.Is(err, apperr.ErrNotFound) {
		return fmt.Errorf("failed to get search log: %w", err)
	}
	if searchLog != nil && time.Since(searchLog.SearchTime) < searchCacheTTL {
		uc.logger.Debug(ctx, "skipping external search, recently searched",
			slog.String("artist_id", artistID),
			slog.Time("search_time", searchLog.SearchTime),
		)
		return nil
	}

	// 2. Get Artist
	artist, err := uc.artistRepo.Get(ctx, artistID)
	if err != nil {
		return fmt.Errorf("failed to get artist: %w", err)
	}

	// 3. Get Official Site — missing site is not an error; search continues with nil
	site, err := uc.artistRepo.GetOfficialSite(ctx, artistID)
	if err != nil && !errors.Is(err, apperr.ErrNotFound) {
		return fmt.Errorf("failed to get official site: %w", err)
	}
	if errors.Is(err, apperr.ErrNotFound) {
		site = nil
	}

	// 4. Get existing upcoming concerts for deduplication
	existing, err := uc.concertRepo.ListByArtist(ctx, artistID, true)
	if err != nil {
		return fmt.Errorf("failed to list existing concerts: %w", err)
	}

	// 5. Mark search as in-progress (prevents concurrent redundant API calls)
	if err := uc.searchLogRepo.Upsert(ctx, artistID); err != nil {
		uc.logger.Error(ctx, "failed to upsert search log before Gemini call", err, slog.String("artist_id", artistID))
		// Continue anyway - this is non-fatal
	}

	// 6. Search new concerts via external API
	scraped, err := uc.concertSearcher.Search(ctx, artist, site, time.Now())
	if err != nil {
		// Clean up search log on failure to allow retry
		if delErr := uc.searchLogRepo.Delete(ctx, artistID); delErr != nil {
			uc.logger.Error(ctx, "failed to delete search log after Gemini failure", delErr, slog.String("artist_id", artistID))
		}
		return fmt.Errorf("failed to search concerts via external API: %w", err)
	}

	// 7. Deduplicate against existing concerts
	seen := make(map[string]bool)
	for _, ex := range existing {
		seen[getUniqueKey(ex.LocalDate, ex.StartTime)] = true
	}

	var newConcerts []messaging.ScrapedConcertData
	for _, s := range scraped {
		key := getUniqueKey(s.LocalDate, s.StartTime)
		if seen[key] {
			uc.logger.Debug(ctx, "filtered existing/duplicate event",
				slog.String("artist_id", artistID),
				slog.String("title", s.Title),
				slog.String("venue", s.ListedVenueName),
				slog.String("date", s.LocalDate.Format("2006-01-02")),
			)
			continue
		}
		seen[key] = true

		newConcerts = append(newConcerts, messaging.ScrapedConcertData{
			Title:           s.Title,
			ListedVenueName: s.ListedVenueName,
			AdminArea:       s.AdminArea,
			LocalDate:       s.LocalDate,
			StartTime:       s.StartTime,
			OpenTime:        s.OpenTime,
			SourceURL:       s.SourceURL,
		})
	}

	// 8. Publish concert.discovered.v1 event (artist-level batch)
	if len(newConcerts) == 0 {
		uc.logger.Debug(ctx, "no new concerts after deduplication",
			slog.String("artist_id", artistID),
		)
		return nil
	}

	eventData := messaging.ConcertDiscoveredData{
		ArtistID:   artistID,
		ArtistName: artist.Name,
		Concerts:   newConcerts,
	}

	msg, err := messaging.NewCloudEvent(messaging.EventTypeConcertDiscovered, eventData)
	if err != nil {
		return fmt.Errorf("failed to create concert.discovered event: %w", err)
	}

	if err := uc.publisher.Publish(messaging.EventTypeConcertDiscovered, msg); err != nil {
		uc.logger.Error(ctx, "failed to publish concert.discovered event", err,
			slog.String("artist_id", artistID),
			slog.Int("concert_count", len(newConcerts)),
		)
		// Non-fatal: CronJob will re-discover on next run.
		// Search log is already updated, preventing immediate re-search.
		return nil
	}

	uc.logger.Info(ctx, "published concert.discovered event",
		slog.String("artist_id", artistID),
		slog.String("artist_name", artist.Name),
		slog.Int("concert_count", len(newConcerts)),
	)

	return nil
}

// getUniqueKey generates a unique identifier for a concert based on its date and start time.
// This is used for deduplicating events discovered from multiple sources.
// Format: "YYYY-MM-DD|StartTime" (or "unknown" if start time is nil).
func getUniqueKey(date time.Time, startTime *time.Time) string {
	stStr := "unknown"
	if startTime != nil {
		stStr = startTime.Format(time.RFC3339)
	}
	return date.Format("2006-01-02") + "|" + stStr
}
