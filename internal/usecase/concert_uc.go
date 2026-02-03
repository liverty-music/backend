package usecase

import (
	"context"
	"errors"
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
	venueRepo entity.VenueRepository,
	concertSearcher entity.ConcertSearcher,
	logger *logging.Logger,
) ConcertUseCase {
	return &concertUseCase{
		artistRepo:      artistRepo,
		concertRepo:     concertRepo,
		venueRepo:       venueRepo,
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

		venue, err := uc.venueRepo.GetByName(ctx, s.VenueName)
		if err != nil {
			if !errors.Is(err, apperr.ErrNotFound) {
				uc.logger.Error(ctx, "failed to get venue by name", err, slog.String("name", s.VenueName))
				continue
			}

			// Not found: create new venue
			now := time.Now()
			// Use UUIDv7 for time-ordered keys
			venueID := uc.generateID(ctx, "venue")
			if venueID == "" {
				continue
			}
			newVenue := &entity.Venue{
				ID:         venueID,
				Name:       s.VenueName,
				CreateTime: now,
				UpdateTime: now,
			}
			if err := uc.venueRepo.Create(ctx, newVenue); err != nil {
				uc.logger.Error(ctx, "failed to create venue", err, slog.String("name", s.VenueName))
				continue
			}
			venue = newVenue
		}

		// Create Concert
		now := time.Now()
		concertID := uc.generateID(ctx, "concert")
		if concertID == "" {
			continue
		}
		concert := &entity.Concert{
			ID:             concertID,
			ArtistID:       artistID,
			VenueID:        venue.ID,
			Title:          s.Title,
			LocalEventDate: s.LocalEventDate,
			StartTime:      s.StartTime,
			OpenTime:       s.OpenTime,
			SourceURL:      s.SourceURL,
			CreateTime:     now,
			UpdateTime:     now,
		}

		if err := uc.concertRepo.Create(ctx, concert); err != nil {
			uc.logger.Error(ctx, "failed to create concert", err, slog.String("title", s.Title))
			continue
		}

		discovered = append(discovered, concert)
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

// generateID generates a new UUIDv7.
// If it fails (extremely rare), it logs the error and returns an empty string.
func (uc *concertUseCase) generateID(ctx context.Context, kind string) string {
	id, err := uuid.NewV7()
	if err != nil {
		uc.logger.Error(ctx, "failed to generate ID", err, slog.String("kind", kind))
		return ""
	}
	return id.String()
}
