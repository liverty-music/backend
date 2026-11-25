package usecase

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"slices"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/entity"
)

// VenueAreaBackfillColumns are the columns of the backfill's CSV.
var VenueAreaBackfillColumns = []string{"venue_id", "listed_venue_name", "answer_area", "resolved_code", "answer_zone", "valid_zone"}

// VenueAreaBackfill asks an external model where each Venue without an
// admin area or a time zone is, checks the answers against the Area
// reference data and writes them as CSV for the owner's review. It writes
// nothing to the database; the reviewed CSV becomes a migration.
type VenueAreaBackfill struct {
	venues entity.VenueRepository
	areas  entity.AreaRepository
	asker  entity.VenueAreaAsker
	logger *logging.Logger
}

// NewVenueAreaBackfill creates a VenueAreaBackfill.
func NewVenueAreaBackfill(venues entity.VenueRepository, areas entity.AreaRepository, asker entity.VenueAreaAsker, logger *logging.Logger) *VenueAreaBackfill {
	return &VenueAreaBackfill{venues: venues, areas: areas, asker: asker, logger: logger}
}

// Run writes the CSV header and one row per Venue without an area or a
// zone:
//
//   - answer_area and answer_zone: what the model answered (empty for a venue
//     with no known source page, or when the call failed);
//   - resolved_code: the first-level Area code the answered area resolves
//     to (Area.Resolve), empty when it resolves to none;
//   - valid_zone: the answered zone when the resolved Area allows it (the
//     Area's own zone, or one of its country's zones for an Area without
//     one), empty otherwise.
//
// A failed call leaves its row's answers empty and the run goes on; Run
// returns the number of failed calls with the first error.
//
// # Possible errors
//
//   - Listing the Venues or reading the Area reference data failed.
//   - Writing the CSV failed.
//   - One or more model calls failed (after every row is written).
func (uc *VenueAreaBackfill) Run(ctx context.Context, out io.Writer) error {
	venues, err := uc.venues.ListWithoutAreaOrZone(ctx)
	if err != nil {
		return fmt.Errorf("list venues without an area or a zone: %w", err)
	}
	w := csv.NewWriter(out)
	if err := w.Write(VenueAreaBackfillColumns); err != nil {
		return fmt.Errorf("write the CSV header: %w", err)
	}
	var failed []error
	for _, v := range venues {
		row, err := uc.row(ctx, v)
		if err != nil && !errors.Is(err, errAskFailed) {
			return err
		}
		if err != nil {
			failed = append(failed, err)
		}
		if err := w.Write(row); err != nil {
			return fmt.Errorf("write the CSV row of venue %s: %w", v.VenueID, err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return fmt.Errorf("write the CSV: %w", err)
	}
	if len(failed) > 0 {
		return fmt.Errorf("%d of %d venues could not be asked: %w", len(failed), len(venues), failed[0])
	}
	return nil
}

// errAskFailed marks a failed model call, which leaves the row's answers
// empty without stopping the run.
var errAskFailed = errors.New("asking the model failed")

func (uc *VenueAreaBackfill) row(ctx context.Context, v *entity.VenueToBackfill) ([]string, error) {
	empty := []string{v.VenueID, v.ListedName, "", "", "", ""}
	if v.SourceURL == "" {
		return empty, nil
	}
	answer, err := uc.asker.AskVenueArea(ctx, v.ListedName, v.SourceURL)
	if err != nil {
		uc.logger.Warn(ctx, "venue area backfill: the model call failed")
		return empty, fmt.Errorf("%w for venue %s: %w", errAskFailed, v.VenueID, err)
	}
	code, zone, err := uc.check(ctx, answer)
	if err != nil {
		return nil, err
	}
	return []string{v.VenueID, v.ListedName, answer.AdminArea, code, answer.TimeZone, zone}, nil
}

// check resolves the answered area and returns its first-level code and the
// answered zone when that Area allows it.
func (uc *VenueAreaBackfill) check(ctx context.Context, answer *entity.VenueAreaAnswer) (string, string, error) {
	if answer.AdminArea == "" {
		return "", "", nil
	}
	area, err := uc.areas.Resolve(ctx, answer.AdminArea, nil)
	if errors.Is(err, apperr.ErrNotFound) || errors.Is(err, apperr.ErrInvalidArgument) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("resolve the answered area %q: %w", answer.AdminArea, err)
	}
	if answer.TimeZone == "" {
		return area.Code, "", nil
	}
	if area.TimeZone != nil {
		if *area.TimeZone == answer.TimeZone {
			return area.Code, answer.TimeZone, nil
		}
		return area.Code, "", nil
	}
	country, err := uc.areas.ListByCountry(ctx, area.CountryCode)
	if err != nil {
		return "", "", fmt.Errorf("list the zones of %s: %w", area.CountryCode, err)
	}
	if slices.ContainsFunc(country.TimeZones, func(z *entity.AreaTimeZone) bool { return z.Name == answer.TimeZone }) {
		return area.Code, answer.TimeZone, nil
	}
	return area.Code, "", nil
}
