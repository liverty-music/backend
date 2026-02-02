package usecase

import (
	"context"
	"fmt"
	"log/slog"
	"time"

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
	concertSearcher entity.ConcertSearcher
	logger          *logging.Logger
}

// Compile-time interface compliance check
var _ ConcertUseCase = (*concertUseCase)(nil)

// NewConcertUseCase creates a new concert use case.
// It orchestrates concert searching, retrieval, and persistence.
func NewConcertUseCase(
	artistRepo entity.ArtistRepository,
	concertRepo entity.ConcertRepository,
	concertSearcher entity.ConcertSearcher,
	logger *logging.Logger,
) ConcertUseCase {
	return &concertUseCase{
		artistRepo:      artistRepo,
		concertRepo:     concertRepo,
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
func (uc *concertUseCase) SearchNewConcerts(ctx context.Context, artistID string) ([]*entity.Concert, error) {
	if artistID == "" {
		return nil, apperr.New(codes.InvalidArgument, "artist ID is required")
	}

	// 1. Get Artist
	artist, err := uc.artistRepo.Get(ctx, artistID)
	if err != nil {
		return nil, err
	}

	// 2. Get Official Site
	site, err := uc.artistRepo.GetOfficialSite(ctx, artistID)
	if err != nil {
		return nil, err
	}

	// 3. Get existing upcoming concerts
	existing, err := uc.concertRepo.ListByArtist(ctx, artistID, true)
	if err != nil {
		return nil, err
	}

	// 4. Search new concerts
	scraped, err := uc.concertSearcher.Search(ctx, artist, site, time.Now())
	if err != nil {
		return nil, err
	}

	// 5. Deduplicate and map to entities
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
				slog.String("venue", s.VenueName),
				slog.String("date", s.LocalEventDate.Format("2006-01-02")),
			)
			continue
		}
		seen[key] = true

		discovered = append(discovered, &entity.Concert{
			ArtistID:       artistID,
			Title:          s.Title,
			LocalEventDate: s.LocalEventDate,
			StartTime:      s.StartTime,
			OpenTime:       s.OpenTime,
			SourceURL:      s.SourceURL,
		})
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
	return fmt.Sprintf("%s|%s", date.Format("2006-01-02"), stStr)
}
