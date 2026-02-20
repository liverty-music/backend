package usecase

import (
	"context"
	"errors"
	"log/slog"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// VenueEnrichmentUseCase defines the interface for the venue normalization pipeline.
type VenueEnrichmentUseCase interface {
	// EnrichPendingVenues fetches all pending venues and attempts to resolve each
	// one against external place services. Per-venue errors are non-fatal: a failure
	// marks the venue as failed and processing continues with the next venue.
	EnrichPendingVenues(ctx context.Context) error
}

// VenueNamedSearcher wraps a VenuePlaceSearcher with a label used to decide
// which entity field (MBID vs GooglePlaceID) the ExternalID is assigned to.
type VenueNamedSearcher struct {
	Searcher      entity.VenuePlaceSearcher
	AssignToMBID  bool // true → MBID, false → GooglePlaceID
}

// venueEnrichmentUseCase implements VenueEnrichmentUseCase.
type venueEnrichmentUseCase struct {
	venueRepo entity.VenueEnrichmentRepository
	// venueByNameRepo is the subset of VenueRepository needed for duplicate detection.
	// In practice, *rdb.VenueRepository implements both interfaces.
	venueByNameRepo venueByNameRepository
	searchers       []VenueNamedSearcher
	logger          *logging.Logger
}

// venueByNameRepository is the minimal read interface needed for duplicate detection.
type venueByNameRepository interface {
	GetByName(ctx context.Context, name string) (*entity.Venue, error)
}

// Compile-time interface compliance check.
var _ VenueEnrichmentUseCase = (*venueEnrichmentUseCase)(nil)

// NewVenueEnrichmentUseCase creates a new venue enrichment use case.
// venueRepo must also implement venueByNameRepository (i.e., GetByName).
// searchers are tried in order; the first successful result wins.
func NewVenueEnrichmentUseCase(
	venueRepo entity.VenueEnrichmentRepository,
	venueByNameRepo venueByNameRepository,
	logger *logging.Logger,
	searchers ...VenueNamedSearcher,
) VenueEnrichmentUseCase {
	return &venueEnrichmentUseCase{
		venueRepo:       venueRepo,
		venueByNameRepo: venueByNameRepo,
		searchers:       searchers,
		logger:          logger,
	}
}

// EnrichPendingVenues processes all pending venues through the enrichment pipeline.
func (uc *venueEnrichmentUseCase) EnrichPendingVenues(ctx context.Context) error {
	pending, err := uc.venueRepo.ListPending(ctx)
	if err != nil {
		return err
	}

	uc.logger.Info(ctx, "starting venue enrichment", slog.Int("count", len(pending)))

	for _, v := range pending {
		if err := uc.enrichOne(ctx, v); err != nil {
			uc.logger.Warn(ctx, "venue enrichment failed, marking as failed",
				slog.String("venue_id", v.ID),
				slog.String("raw_name", v.RawName),
				slog.Any("error", err),
			)
			if markErr := uc.venueRepo.MarkFailed(ctx, v.ID); markErr != nil {
				uc.logger.Error(ctx, "failed to mark venue as failed", markErr,
					slog.String("venue_id", v.ID),
				)
			}
		}
	}

	return nil
}

// enrichOne attempts to enrich a single venue by searching external place services.
func (uc *venueEnrichmentUseCase) enrichOne(ctx context.Context, v *entity.Venue) error {
	adminArea := ""
	if v.AdminArea != nil {
		adminArea = *v.AdminArea
	}

	for _, ns := range uc.searchers {
		place, err := ns.Searcher.SearchPlace(ctx, v.RawName, adminArea)
		if err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				continue
			}
			return err
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
			if ns.AssignToMBID {
				enriched.MBID = place.ExternalID
			} else {
				enriched.GooglePlaceID = place.ExternalID
			}
			return uc.venueRepo.UpdateEnriched(ctx, enriched)
		}
	}

	return apperr.New(codes.NotFound, "no external match found for venue")
}
