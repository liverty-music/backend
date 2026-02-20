package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/liverty-music/backend/internal/entity"

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

	// SearchNewConcerts discovers new concerts using external sources.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the artist ID is empty or artist/site not found.
	//  - Unavailable: If the external search service is down.
	SearchNewConcerts(ctx context.Context, artistID string) ([]*entity.Concert, error)
}

// concertUseCase implements the ConcertUseCase interface.
type concertUseCase struct {
	artistRepo      entity.ArtistRepository
	concertRepo     entity.ConcertRepository
	venueRepo       entity.VenueRepository
	searchLogRepo   entity.SearchLogRepository
	concertSearcher entity.ConcertSearcher
	logger          *logging.Logger
}

// searchCacheTTL is the duration for which a search log is considered fresh.
const searchCacheTTL = 24 * time.Hour

// Compile-time interface compliance check
var _ ConcertUseCase = (*concertUseCase)(nil)

// NewConcertUseCase creates a new concert use case.
// It orchestrates concert searching, retrieval, and persistence.
func NewConcertUseCase(
	artistRepo entity.ArtistRepository,
	concertRepo entity.ConcertRepository,
	venueRepo entity.VenueRepository,
	searchLogRepo entity.SearchLogRepository,
	concertSearcher entity.ConcertSearcher,
	logger *logging.Logger,
) ConcertUseCase {
	return &concertUseCase{
		artistRepo:      artistRepo,
		concertRepo:     concertRepo,
		venueRepo:       venueRepo,
		searchLogRepo:   searchLogRepo,
		concertSearcher: concertSearcher,
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

// SearchNewConcerts discovers new concerts using external sources.
// If the artist was searched within the last 24 hours, it skips the external
// API call and returns an empty result.
func (uc *concertUseCase) SearchNewConcerts(ctx context.Context, artistID string) ([]*entity.Concert, error) {
	if artistID == "" {
		return nil, apperr.New(codes.InvalidArgument, "artist ID is required")
	}

	// 1. Check search log — skip external API if recently searched
	searchLog, err := uc.searchLogRepo.GetByArtistID(ctx, artistID)
	if err != nil && !errors.Is(err, apperr.ErrNotFound) {
		return nil, fmt.Errorf("failed to get search log: %w", err)
	}
	if searchLog != nil && time.Since(searchLog.SearchTime) < searchCacheTTL {
		uc.logger.Debug(ctx, "skipping external search, recently searched",
			slog.String("artist_id", artistID),
			slog.Time("search_time", searchLog.SearchTime),
		)
		return nil, nil
	}

	// 2. Get Artist
	artist, err := uc.artistRepo.Get(ctx, artistID)
	if err != nil {
		return nil, fmt.Errorf("failed to get artist: %w", err)
	}

	// 3. Get Official Site
	site, err := uc.artistRepo.GetOfficialSite(ctx, artistID)
	if err != nil {
		return nil, fmt.Errorf("failed to get official site: %w", err)
	}

	// 4. Get existing upcoming concerts
	existing, err := uc.concertRepo.ListByArtist(ctx, artistID, true)
	if err != nil {
		return nil, fmt.Errorf("failed to list existing concerts: %w", err)
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
		return nil, fmt.Errorf("failed to search concerts via external API: %w", err)
	}

	// 7. Deduplicate and map to entities
	var discovered []*entity.Concert
	seen := make(map[string]bool)
	for _, ex := range existing {
		seen[getUniqueKey(ex.LocalEventDate, ex.StartTime)] = true
	}

	for _, s := range scraped {
		key := getUniqueKey(s.LocalEventDate, s.StartTime)
		if seen[key] {
			uc.logger.Debug(ctx, "filtered existing/duplicate event",
				slog.String("artistID", artistID),
				slog.String("title", s.Title),
				slog.String("venue", s.ListedVenueName),
				slog.String("date", s.LocalEventDate.Format("2006-01-02")),
			)
			continue
		}
		seen[key] = true

		venue, err := uc.venueRepo.GetByName(ctx, s.ListedVenueName)
		if err != nil {
			if !errors.Is(err, apperr.ErrNotFound) {
				uc.logger.Error(ctx, "failed to get venue by name", err, slog.String("name", s.ListedVenueName))
				continue
			}

			// Not found: create new venue
			// Use UUIDv7 for time-ordered keys
			venueID := newUUIDv7(ctx, "venue", uc.logger)
			if venueID == "" {
				continue
			}
			newVenue := &entity.Venue{
				ID:        venueID,
				Name:      s.ListedVenueName,
				AdminArea: s.AdminArea,
			}
			if err := uc.venueRepo.Create(ctx, newVenue); err != nil {
				if errors.Is(err, apperr.ErrAlreadyExists) {
					// Race condition: another request created it. Fetch again.
					v, getErr := uc.venueRepo.GetByName(ctx, s.ListedVenueName)
					if getErr != nil {
						uc.logger.Error(ctx, "failed to get venue after race", getErr, slog.String("name", s.ListedVenueName))
						continue
					}
					venue = v
				} else {
					uc.logger.Error(ctx, "failed to create venue", err, slog.String("name", s.ListedVenueName))
					continue
				}
			} else {
				venue = newVenue
			}
		}

		concertID := newUUIDv7(ctx, "concert", uc.logger)
		if concertID == "" {
			continue
		}
		discovered = append(discovered, &entity.Concert{
			Event: entity.Event{
				ID:              concertID,
				VenueID:         venue.ID,
				Title:           s.Title,
				ListedVenueName: s.ListedVenueName,
				LocalEventDate:  s.LocalEventDate,
				StartTime:       s.StartTime,
				OpenTime:        s.OpenTime,
				SourceURL:       s.SourceURL,
			},
			ArtistID: artistID,
		})
	}

	// 8. Bulk insert all discovered concerts
	if len(discovered) > 0 {
		if err := uc.concertRepo.Create(ctx, discovered...); err != nil {
			return nil, fmt.Errorf("failed to create concerts: %w", err)
		}
	}

	return discovered, nil
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

// newUUIDv7 generates a new UUIDv7.
// If it fails (extremely rare), it logs the error and returns an empty string.
func newUUIDv7(ctx context.Context, kind string, logger *logging.Logger) string {
	id, err := uuid.NewV7()
	if err != nil {
		logger.Error(ctx, "failed to generate ID", err, slog.String("kind", kind))
		return ""
	}
	return id.String()
}
