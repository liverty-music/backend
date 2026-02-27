package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
)

// VenueEnrichmentUseCase defines the interface for the venue normalization pipeline.
type VenueEnrichmentUseCase interface {
	// EnrichPendingVenues fetches all pending venues and attempts to resolve each
	// one against external place services. Per-venue errors are non-fatal: transient
	// errors are logged and the venue remains pending for the next run; only when all
	// searchers report NotFound is the venue permanently marked as failed.
	EnrichPendingVenues(ctx context.Context) error

	// EnrichOne enriches a single venue identified by its ID. It fetches the venue,
	// runs it through the enrichment pipeline, and handles the result (update, merge,
	// or mark as failed). Returns an error only on infrastructure failures; enrichment
	// outcomes (not-found, merged, enriched) are handled internally.
	EnrichOne(ctx context.Context, venueID string) error
}

// VenueNamedSearcher wraps a VenuePlaceSearcher with a label used to decide
// which entity field (MBID vs GooglePlaceID) the ExternalID is assigned to.
type VenueNamedSearcher struct {
	Searcher     entity.VenuePlaceSearcher
	AssignToMBID bool // true → MBID, false → GooglePlaceID
}

// venueEnrichmentUseCase implements VenueEnrichmentUseCase.
type venueEnrichmentUseCase struct {
	venueRepo entity.VenueEnrichmentRepository
	// venueByNameRepo is the subset of VenueRepository needed for duplicate detection.
	// In practice, *rdb.VenueRepository implements both interfaces.
	venueByNameRepo venueByNameRepository
	// venueGetRepo is the subset of VenueRepository needed to fetch a single venue by ID.
	venueGetRepo venueGetRepository
	searchers    []VenueNamedSearcher
	logger       *logging.Logger
}

// venueByNameRepository is the minimal read interface needed for duplicate detection.
type venueByNameRepository interface {
	GetByName(ctx context.Context, name string) (*entity.Venue, error)
}

// venueGetRepository is the minimal read interface needed to fetch a single venue by ID.
type venueGetRepository interface {
	Get(ctx context.Context, id string) (*entity.Venue, error)
}

// Compile-time interface compliance check.
var _ VenueEnrichmentUseCase = (*venueEnrichmentUseCase)(nil)

// NewVenueEnrichmentUseCase creates a new venue enrichment use case.
// venueGetRepo provides single-venue lookups for EnrichOne.
// searchers are tried in order; the first successful result wins.
func NewVenueEnrichmentUseCase(
	venueRepo entity.VenueEnrichmentRepository,
	venueByNameRepo venueByNameRepository,
	venueGetRepo venueGetRepository,
	logger *logging.Logger,
	searchers ...VenueNamedSearcher,
) VenueEnrichmentUseCase {
	return &venueEnrichmentUseCase{
		venueRepo:       venueRepo,
		venueByNameRepo: venueByNameRepo,
		venueGetRepo:    venueGetRepo,
		searchers:       searchers,
		logger:          logger,
	}
}

// errNoExternalMatch is the sentinel returned by enrichOne when all searchers
// report NotFound. It is the only condition that permanently marks a venue failed.
var errNoExternalMatch = apperr.ErrNotFound

// EnrichPendingVenues processes all pending venues through the enrichment pipeline.
func (uc *venueEnrichmentUseCase) EnrichPendingVenues(ctx context.Context) error {
	pending, err := uc.venueRepo.ListPending(ctx)
	if err != nil {
		return err
	}

	uc.logger.Info(ctx, "starting venue enrichment", slog.Int("count", len(pending)))

	for _, v := range pending {
		if err := uc.enrichOne(ctx, v); err != nil {
			if errors.Is(err, errNoExternalMatch) {
				// All searchers returned NotFound — permanently mark as failed.
				uc.logger.Warn(ctx, "no external match found for venue, marking as failed",
					slog.String("venue_id", v.ID),
					slog.String("raw_name", v.RawName),
				)
				if markErr := uc.venueRepo.MarkFailed(ctx, v.ID); markErr != nil {
					uc.logger.Error(ctx, "failed to mark venue as failed", markErr,
						slog.String("venue_id", v.ID),
					)
				}
			} else {
				// Transient error (network, rate-limit, etc.) — leave pending for retry.
				uc.logger.Warn(ctx, "transient error during venue enrichment, will retry next run",
					slog.String("venue_id", v.ID),
					slog.String("raw_name", v.RawName),
					slog.Any("error", err),
				)
			}
		}
	}

	return nil
}

// EnrichOne enriches a single venue by ID. It fetches the venue, runs the enrichment
// pipeline, and handles the outcome (enriched, merged, or marked as failed).
func (uc *venueEnrichmentUseCase) EnrichOne(ctx context.Context, venueID string) error {
	v, err := uc.venueGetRepo.Get(ctx, venueID)
	if err != nil {
		return fmt.Errorf("get venue %s: %w", venueID, err)
	}

	if err := uc.enrichOne(ctx, v); err != nil {
		if errors.Is(err, errNoExternalMatch) {
			uc.logger.Warn(ctx, "no external match found for venue, marking as failed",
				slog.String("venue_id", v.ID),
				slog.String("raw_name", v.RawName),
			)
			if markErr := uc.venueRepo.MarkFailed(ctx, v.ID); markErr != nil {
				uc.logger.Error(ctx, "failed to mark venue as failed", markErr,
					slog.String("venue_id", v.ID),
				)
			}
			return nil
		}
		return err
	}

	return nil
}

// enrichOne attempts to enrich a single venue by searching external place services.
// It returns errNoExternalMatch when all searchers report NotFound (permanent failure).
// Transient errors from individual searchers cause a log + skip to the next searcher;
// if all searchers return transient errors the last transient error is returned so the
// caller does NOT permanently mark the venue as failed.
func (uc *venueEnrichmentUseCase) enrichOne(ctx context.Context, v *entity.Venue) error {
	adminArea := ""
	if v.AdminArea != nil {
		adminArea = *v.AdminArea
	}

	var lastTransientErr error

	for _, ns := range uc.searchers {
		place, err := ns.Searcher.SearchPlace(ctx, v.RawName, adminArea)
		if err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				// This searcher found nothing — try the next one.
				continue
			}
			// Transient error (Unavailable, DeadlineExceeded, etc.) — skip this
			// searcher but continue to the next so that a fallback can still succeed.
			uc.logger.Warn(ctx, "searcher returned transient error, trying next searcher",
				slog.String("venue_id", v.ID),
				slog.Any("error", err),
			)
			lastTransientErr = err
			continue
		}

		// Check for an existing venue with the same canonical name to detect duplicates.
		existing, err := uc.venueByNameRepo.GetByName(ctx, place.Name)
		switch {
		case err != nil && !errors.Is(err, apperr.ErrNotFound):
			return err

		case err == nil && existing.ID != v.ID:
			// Canonical venue already exists — merge the duplicate into it.
			uc.logger.Info(ctx, "merging duplicate venue",
				slog.String("canonical_id", existing.ID),
				slog.String("duplicate_id", v.ID),
				slog.String("canonical_name", place.Name),
			)
			return uc.venueRepo.MergeVenues(ctx, existing.ID, v.ID)

		default:
			// No duplicate — update this venue as enriched.
			enriched := &entity.Venue{
				ID:      v.ID,
				Name:    place.Name,
				RawName: v.RawName,
			}
			id := place.ExternalID
			if ns.AssignToMBID {
				enriched.MBID = &id
			} else {
				enriched.GooglePlaceID = &id
			}
			return uc.venueRepo.UpdateEnriched(ctx, enriched)
		}
	}

	// If any searcher had a transient error, propagate it so the caller does NOT
	// permanently mark the venue as failed — the next run will retry.
	if lastTransientErr != nil {
		return lastTransientErr
	}
	return errNoExternalMatch
}
