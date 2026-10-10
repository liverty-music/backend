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

const (
	// officialSiteRefreshAge is how old an official site check must be for the
	// artist to be due again.
	officialSiteRefreshAge = 7 * 24 * time.Hour
	// officialSiteRefreshCycleDays is the number of daily runs over which every
	// followed artist is refreshed once; it sizes each run's batch.
	officialSiteRefreshCycleDays = 7
	// officialSiteRefreshMaxConsecutiveFailures stops a run after this many
	// artists fail in a row, treating it as a systemic catalog failure.
	officialSiteRefreshMaxConsecutiveFailures = 3
)

// OfficialSiteRefreshSummary reports what one daily refresh run did.
type OfficialSiteRefreshSummary struct {
	// Followed is the number of distinct followed artists the batch was sized from.
	Followed int
	// BatchSize is the maximum number of artists the run could refresh.
	BatchSize int
	// Due is the number of due artists returned for this run.
	Due int
	// Attempted is the number of artists the run tried to refresh.
	Attempted int
	// Failed is the number of attempted artists whose refresh failed.
	Failed int
	// CircuitBroken reports whether the run stopped after consecutive failures.
	CircuitBroken bool
}

// OfficialSiteRefreshUseCase keeps artists' official sites in line with the
// music catalog (MusicBrainz).
type OfficialSiteRefreshUseCase interface {
	// RefreshOfficialSite resolves the artist's homepage from the catalog,
	// creates or replaces the stored official site when the catalog resolves
	// to a different URL, keeps it otherwise, and then records the check time.
	// No check is recorded when a step before it fails, so the artist stays due.
	//
	// # Possible errors:
	//
	//   - Unavailable: the catalog is down or rate-limited.
	//   - InvalidArgument: the catalog URL is not a valid official site URL.
	//   - NotFound: the artist does not exist.
	//   - Internal: unexpected failure.
	RefreshOfficialSite(ctx context.Context, artistID, mbid string) error

	// RefreshDueOfficialSites runs one daily refresh: it refreshes, one at a
	// time, up to ceil(followed artists / 7) followed artists whose site was
	// never checked or was checked more than 7 days ago. A failed artist does
	// not stop the run, except that the run stops after 3 consecutive failures;
	// it also stops when ctx is done.
	//
	// # Possible errors:
	//
	//   - Internal: listing followed or due artists failed.
	RefreshDueOfficialSites(ctx context.Context) (*OfficialSiteRefreshSummary, error)
}

// officialSiteRefreshUseCase implements OfficialSiteRefreshUseCase.
type officialSiteRefreshUseCase struct {
	artistRepo   entity.ArtistRepository
	followRepo   entity.FollowRepository
	siteResolver entity.OfficialSiteResolver
	logger       *logging.Logger
}

// Compile-time interface compliance check.
var _ OfficialSiteRefreshUseCase = (*officialSiteRefreshUseCase)(nil)

// NewOfficialSiteRefreshUseCase creates a new official site refresh use case.
func NewOfficialSiteRefreshUseCase(
	artistRepo entity.ArtistRepository,
	followRepo entity.FollowRepository,
	siteResolver entity.OfficialSiteResolver,
	logger *logging.Logger,
) OfficialSiteRefreshUseCase {
	return &officialSiteRefreshUseCase{
		artistRepo:   artistRepo,
		followRepo:   followRepo,
		siteResolver: siteResolver,
		logger:       logger,
	}
}

// officialSiteRefreshBatchSize returns how many artists one daily run refreshes
// for the given number of followed artists: the count divided by the cycle
// length, rounded up, so the whole set is refreshed in about one cycle.
func officialSiteRefreshBatchSize(followed int) int {
	if followed <= 0 {
		return 0
	}
	return (followed + officialSiteRefreshCycleDays - 1) / officialSiteRefreshCycleDays
}

// RefreshOfficialSite brings the artist's stored official site in line with the catalog.
// An MBID the catalog does not know counts as no URL found, so the check is
// still recorded and the artist is not due again on every run.
func (uc *officialSiteRefreshUseCase) RefreshOfficialSite(ctx context.Context, artistID, mbid string) error {
	url, err := uc.siteResolver.ResolveOfficialSiteURL(ctx, mbid)
	switch {
	case errors.Is(err, apperr.ErrNotFound):
		uc.logger.Warn(ctx, "catalog has no artist for the MBID; recording the official site check",
			slog.String("artist_id", artistID),
			slog.String("mbid", mbid),
		)
		url = ""
	case err != nil:
		return fmt.Errorf("resolve official site url for artist %s: %w", artistID, err)
	}

	if url != "" {
		if err := uc.applyResolvedURL(ctx, artistID, url); err != nil {
			return err
		}
	}

	if err := uc.artistRepo.MarkOfficialSiteChecked(ctx, artistID, time.Now()); err != nil {
		return fmt.Errorf("mark official site checked for artist %s: %w", artistID, err)
	}
	return nil
}

// applyResolvedURL creates the artist's official site when none is stored, and
// replaces its URL when it differs from url.
func (uc *officialSiteRefreshUseCase) applyResolvedURL(ctx context.Context, artistID, url string) error {
	site, err := uc.artistRepo.GetOfficialSite(ctx, artistID)
	switch {
	case errors.Is(err, apperr.ErrNotFound):
		if err := uc.artistRepo.CreateOfficialSite(ctx, entity.NewOfficialSite(artistID, url)); err != nil {
			return fmt.Errorf("create official site for artist %s: %w", artistID, err)
		}
		uc.logger.Info(ctx, "official site created from catalog",
			slog.String("artist_id", artistID),
			slog.String("old_url", ""),
			slog.String("new_url", url),
		)
		return nil
	case err != nil:
		return fmt.Errorf("get official site for artist %s: %w", artistID, err)
	case site.URL == url:
		return nil
	}

	if err := uc.artistRepo.UpdateOfficialSiteURL(ctx, artistID, url); err != nil {
		return fmt.Errorf("update official site url for artist %s: %w", artistID, err)
	}
	uc.logger.Info(ctx, "official site replaced from catalog",
		slog.String("artist_id", artistID),
		slog.String("old_url", site.URL),
		slog.String("new_url", url),
	)
	return nil
}

// RefreshDueOfficialSites refreshes one daily batch of due followed artists.
func (uc *officialSiteRefreshUseCase) RefreshDueOfficialSites(ctx context.Context) (*OfficialSiteRefreshSummary, error) {
	followed, err := uc.followRepo.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list followed artists: %w", err)
	}

	summary := &OfficialSiteRefreshSummary{
		Followed:  len(followed),
		BatchSize: officialSiteRefreshBatchSize(len(followed)),
	}
	if summary.BatchSize == 0 {
		return summary, nil
	}

	artists, err := uc.artistRepo.ListStaleOfficialSite(ctx, officialSiteRefreshAge, summary.BatchSize)
	if err != nil {
		return nil, fmt.Errorf("list artists due for official site refresh: %w", err)
	}
	summary.Due = len(artists)

	var consecutiveFailures int
	for _, artist := range artists {
		if ctx.Err() != nil {
			break
		}

		summary.Attempted++
		if err := uc.RefreshOfficialSite(ctx, artist.ID, artist.MBID); err != nil {
			summary.Failed++
			consecutiveFailures++
			uc.logger.Error(ctx, "failed to refresh official site for artist", err,
				slog.String("artist_id", artist.ID),
				slog.String("artist_name", artist.Name),
			)
			if consecutiveFailures >= officialSiteRefreshMaxConsecutiveFailures {
				summary.CircuitBroken = true
				uc.logger.Error(ctx, "circuit breaker activated: stopping after consecutive failures", nil,
					slog.Int("consecutive_failures", consecutiveFailures),
				)
				break
			}
			continue
		}
		consecutiveFailures = 0
	}

	return summary, nil
}
