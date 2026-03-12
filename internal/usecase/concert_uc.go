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

	// ListByFollowerGrouped returns concerts for followed artists, grouped by date
	// and classified into home/nearby/away lanes based on proximity to the user's home.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the external user ID is empty.
	//  - NotFound: If the user does not exist.
	ListByFollowerGrouped(ctx context.Context, externalUserID string) ([]*entity.ProximityGroup, error)

	// AsyncSearchNewConcerts enqueues an asynchronous concert discovery job for the
	// given artist. It returns immediately after marking the search log as pending.
	// The actual Gemini API call runs in a background goroutine.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If the artist ID is empty.
	AsyncSearchNewConcerts(ctx context.Context, artistID string) error

	// SearchNewConcerts discovers new concerts synchronously. This is intended
	// for use by the CronJob, which manages its own timeout and circuit breaker.
	SearchNewConcerts(ctx context.Context, artistID string) error

	// ListSearchStatuses returns the search status for the given artist IDs.
	// Artists without a search log entry are returned with StatusUnspecified.
	// Entries pending for more than 3 minutes are treated as failed (self-healing).
	ListSearchStatuses(ctx context.Context, artistIDs []string) ([]*SearchStatus, error)
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

// searchCacheTTL is the duration for which a completed search log is considered fresh.
const searchCacheTTL = 24 * time.Hour

// pendingTimeout is the maximum age of a pending search log before it is
// considered stale and treated as failed (self-healing for crashed workers).
const pendingTimeout = 3 * time.Minute

// backgroundSearchTimeout is the context timeout for background Gemini API calls.
const backgroundSearchTimeout = 120 * time.Second

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

// ListByFollowerGrouped returns concerts for followed artists, grouped by date
// and classified into home/nearby/away lanes based on proximity to the user's home.
func (uc *concertUseCase) ListByFollowerGrouped(ctx context.Context, externalUserID string) ([]*entity.ProximityGroup, error) {
	if externalUserID == "" {
		return nil, apperr.New(codes.InvalidArgument, "user ID is required")
	}

	user, err := uc.userRepo.GetByExternalID(ctx, externalUserID)
	if err != nil {
		return nil, fmt.Errorf("resolve user by external ID: %w", err)
	}

	concerts, err := uc.concertRepo.ListByFollower(ctx, user.ID)
	if err != nil {
		return nil, err
	}

	return entity.GroupByDateAndProximity(concerts, user.Home), nil
}

// resolveUserID maps an external identity (Zitadel sub claim) to the internal user UUID.
func (uc *concertUseCase) resolveUserID(ctx context.Context, externalID string) (string, error) {
	user, err := uc.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return "", fmt.Errorf("resolve user by external ID: %w", err)
	}
	return user.ID, nil
}

// AsyncSearchNewConcerts is a thin async wrapper around SearchNewConcerts.
// It spawns a background goroutine with a detached context so the RPC returns
// immediately. All guard logic lives in SearchNewConcerts.
// Callers poll ListSearchStatuses for completion.
func (uc *concertUseCase) AsyncSearchNewConcerts(ctx context.Context, artistID string) error {
	// Spawn background goroutine with a detached context and 120s timeout.
	// Validation and guard logic are handled by SearchNewConcerts.
	bgCtx, bgCancel := context.WithTimeout(context.WithoutCancel(ctx), backgroundSearchTimeout)
	go func() {
		defer bgCancel()
		if err := uc.SearchNewConcerts(bgCtx, artistID); err != nil {
			uc.logger.Error(bgCtx, "background concert search failed",
				err, slog.String("artist_id", artistID),
			)
		}
	}()

	return nil
}

// SearchNewConcerts discovers new concerts synchronously. It is the single
// entry point for concert search logic, used by both AsyncSearchNewConcerts
// (via background goroutine) and the CronJob.
func (uc *concertUseCase) SearchNewConcerts(ctx context.Context, artistID string) error {
	if artistID == "" {
		return apperr.New(codes.InvalidArgument, "artist ID is required")
	}

	// Check search log — skip if recently completed or currently pending.
	searchLog, err := uc.searchLogRepo.GetByArtistID(ctx, artistID)
	if err != nil && !errors.Is(err, apperr.ErrNotFound) {
		return fmt.Errorf("failed to get search log: %w", err)
	}
	if searchLog != nil {
		if searchLog.Status == entity.SearchLogStatusCompleted && time.Since(searchLog.SearchTime) < searchCacheTTL {
			uc.logger.Debug(ctx, "skipping external search, recently searched",
				slog.String("artist_id", artistID),
				slog.Time("search_time", searchLog.SearchTime),
			)
			return nil
		}
		if searchLog.Status == entity.SearchLogStatusPending && time.Since(searchLog.SearchTime) < pendingTimeout {
			uc.logger.Debug(ctx, "skipping external search, already pending",
				slog.String("artist_id", artistID),
			)
			return nil
		}
	}

	// Mark as pending. If this fails, abort — without a pending row,
	// downstream UpdateStatus calls silently no-op (0 rows affected).
	if err := uc.searchLogRepo.Upsert(ctx, artistID, entity.SearchLogStatusPending); err != nil {
		return fmt.Errorf("failed to mark search as pending: %w", err)
	}

	return uc.executeSearch(ctx, artistID)
}

// executeSearch performs the actual Gemini search, deduplication, and event publishing.
// It updates the search log status to completed or failed on exit.
//
// Deduplication uses the natural key (local_event_date, listed_venue_name, start_at_utc)
// with two lookup sets:
//   - seen: full key for exact UTC-normalized match (handles TZ mismatch).
//   - seenDateVenue: date+venue only, for nil start_at wildcard matching.
//
// Nil start_at semantics:
//   - Scraped nil: "time unknown" — matches any existing concert at the same date+venue.
//   - Existing nil + scraped non-nil: "new info discovered" — published for UPSERT update.
func (uc *concertUseCase) executeSearch(ctx context.Context, artistID string) error {
	// Get Artist
	artist, err := uc.artistRepo.Get(ctx, artistID)
	if err != nil {
		uc.markSearchFailed(ctx, artistID)
		return fmt.Errorf("failed to get artist: %w", err)
	}

	// Get Official Site — missing site is not an error; search continues with nil
	site, err := uc.artistRepo.GetOfficialSite(ctx, artistID)
	if err != nil && !errors.Is(err, apperr.ErrNotFound) {
		uc.markSearchFailed(ctx, artistID)
		return fmt.Errorf("failed to get official site: %w", err)
	}
	if errors.Is(err, apperr.ErrNotFound) {
		site = nil
	}

	// Get existing upcoming concerts for deduplication
	existing, err := uc.concertRepo.ListByArtist(ctx, artistID, true)
	if err != nil {
		uc.markSearchFailed(ctx, artistID)
		return fmt.Errorf("failed to list existing concerts: %w", err)
	}

	// Search new concerts via external API
	scraped, err := uc.concertSearcher.Search(ctx, artist, site, time.Now())
	if err != nil {
		uc.markSearchFailed(ctx, artistID)
		return fmt.Errorf("failed to search concerts via external API: %w", err)
	}

	// Deduplicate against existing concerts using dual lookup sets.
	// - seen: full key (date|venue|start_at_utc) for exact match
	// - seenDateVenue: (date|venue) for nil-match and "existing has nil" detection
	seen := make(map[string]bool)
	seenDateVenue := make(map[string]bool)
	existingHasNilStart := make(map[string]bool) // date|venue keys where existing start_at is nil

	for _, ex := range existing {
		if ex.ListedVenueName == nil {
			// Legacy rows without listed_venue_name cannot participate in dedup.
			continue
		}
		venue := *ex.ListedVenueName
		dvKey := ex.LocalDate.Format("2006-01-02") + "|" + venue
		seenDateVenue[dvKey] = true
		if ex.StartTime == nil {
			existingHasNilStart[dvKey] = true
		} else {
			seen[dvKey+"|"+ex.StartTime.UTC().Format("15:04:05Z")] = true
		}
	}

	var newConcerts []entity.ScrapedConcertData
	for _, s := range scraped {
		dvKey := s.DateVenueKey()

		if s.StartTime == nil {
			// Scraped nil start_at → "I don't know the time".
			// Match any existing or already-seen concert at same date+venue.
			if seenDateVenue[dvKey] {
				uc.logger.Debug(ctx, "filtered existing/duplicate event (nil start_at wildcard)",
					slog.String("artist_id", artistID),
					slog.String("title", s.Title),
					slog.String("venue", s.ListedVenueName),
					slog.String("date", s.LocalDate.Format("2006-01-02")),
				)
				continue
			}
			seenDateVenue[dvKey] = true
		} else {
			// Scraped non-nil start_at.
			fullKey := s.DedupeKey()
			if seen[fullKey] {
				// Exact match — same instant after UTC normalization (handles TZ mismatch).
				uc.logger.Debug(ctx, "filtered existing/duplicate event (exact key match)",
					slog.String("artist_id", artistID),
					slog.String("title", s.Title),
					slog.String("venue", s.ListedVenueName),
					slog.String("date", s.LocalDate.Format("2006-01-02")),
				)
				continue
			}
			seen[fullKey] = true
			seenDateVenue[dvKey] = true
		}

		newConcerts = append(newConcerts, entity.ScrapedConcertData{
			Title:           s.Title,
			ListedVenueName: s.ListedVenueName,
			AdminArea:       s.AdminArea,
			LocalDate:       s.LocalDate,
			StartTime:       s.StartTime,
			OpenTime:        s.OpenTime,
			SourceURL:       s.SourceURL,
		})
	}

	// Publish concert.discovered.v1 event (artist-level batch)
	if len(newConcerts) == 0 {
		uc.logger.Debug(ctx, "no new concerts after deduplication",
			slog.String("artist_id", artistID),
		)
		uc.markSearchCompleted(ctx, artistID)
		return nil
	}

	eventData := entity.ConcertDiscoveredData{
		ArtistID:   artistID,
		ArtistName: artist.Name,
		Concerts:   newConcerts,
	}

	msg, err := messaging.NewEvent(ctx, eventData)
	if err != nil {
		uc.markSearchFailed(ctx, artistID)
		return fmt.Errorf("failed to create concert.discovered event: %w", err)
	}

	if err := uc.publisher.Publish(entity.SubjectConcertDiscovered, msg); err != nil {
		uc.logger.Error(ctx, "failed to publish concert.discovered event", err,
			slog.String("artist_id", artistID),
			slog.Int("concert_count", len(newConcerts)),
		)
		// Non-fatal: CronJob will re-discover on next run.
		uc.markSearchFailed(ctx, artistID)
		return nil
	}

	uc.logger.Info(ctx, "published concert.discovered event",
		slog.String("artist_id", artistID),
		slog.String("artist_name", artist.Name),
		slog.Int("concert_count", len(newConcerts)),
	)

	uc.markSearchCompleted(ctx, artistID)
	return nil
}

// markSearchCompleted updates the search log status to completed.
func (uc *concertUseCase) markSearchCompleted(ctx context.Context, artistID string) {
	if err := uc.searchLogRepo.UpdateStatus(ctx, artistID, entity.SearchLogStatusCompleted); err != nil {
		uc.logger.Error(ctx, "failed to mark search as completed", err, slog.String("artist_id", artistID))
	}
}

// markSearchFailed updates the search log status to failed.
func (uc *concertUseCase) markSearchFailed(ctx context.Context, artistID string) {
	if err := uc.searchLogRepo.UpdateStatus(ctx, artistID, entity.SearchLogStatusFailed); err != nil {
		uc.logger.Error(ctx, "failed to mark search as failed", err, slog.String("artist_id", artistID))
	}
}

// ListSearchStatuses returns the search status for the given artist IDs.
func (uc *concertUseCase) ListSearchStatuses(ctx context.Context, artistIDs []string) ([]*SearchStatus, error) {
	if len(artistIDs) == 0 {
		return nil, apperr.New(codes.InvalidArgument, "at least one artist ID is required")
	}

	logs, err := uc.searchLogRepo.ListByArtistIDs(ctx, artistIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to list search logs: %w", err)
	}

	// Build a map for quick lookup.
	logMap := make(map[string]*entity.SearchLog, len(logs))
	for _, l := range logs {
		logMap[l.ArtistID] = l
	}

	statuses := make([]*SearchStatus, 0, len(artistIDs))
	for _, id := range artistIDs {
		s := &SearchStatus{ArtistID: id, Status: SearchStatusUnspecified}
		if log, ok := logMap[id]; ok {
			s.Status = toSearchStatusValue(log)
		}
		statuses = append(statuses, s)
	}

	return statuses, nil
}

// toSearchStatusValue converts a search log entity to a SearchStatusValue,
// applying stale-pending detection.
func toSearchStatusValue(log *entity.SearchLog) SearchStatusValue {
	switch log.Status {
	case entity.SearchLogStatusPending:
		if time.Since(log.SearchTime) > pendingTimeout {
			return SearchStatusFailed
		}
		return SearchStatusPending
	case entity.SearchLogStatusCompleted:
		return SearchStatusCompleted
	case entity.SearchLogStatusFailed:
		return SearchStatusFailed
	default:
		return SearchStatusUnspecified
	}
}
