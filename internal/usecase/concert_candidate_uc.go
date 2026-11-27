package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/pkg/analyticslog"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// ConcertCandidateReview pairs a concert candidate with its resolved performer
// for presentation in the admin review queue.
type ConcertCandidateReview struct {
	// ConcertCandidate is the concert candidate row awaiting approval.
	ConcertCandidate *entity.ConcertCandidate
	// Performer is the artist who will perform at the concert.
	Performer *entity.Artist
}

// ApproveResolution selects how Approve reconciles a concert candidate that
// duplicates an already-published event. It mirrors the admin Approve RPC's
// Resolution enum but is defined in the usecase layer so the business logic does
// not depend on the generated proto types.
type ApproveResolution int

const (
	// ApproveResolutionUnspecified makes Approve return a DuplicateConflict
	// (without mutating state) when it detects a duplicate; otherwise it publishes.
	ApproveResolutionUnspecified ApproveResolution = iota
	// ApproveResolutionKeepExisting logs the concert candidate to the rejection
	// log with a duplicate reason, keeps its listed venue name among the
	// existing event's other listed venue names, makes its Artist a performer
	// of the event (unless it is an Organizer's event) and clears it, leaving
	// the event's listed venue name, Venue and times unchanged.
	ApproveResolutionKeepExisting
	// ApproveResolutionAdoptCandidate replaces the existing event's listed
	// venue name with the concert candidate's, keeping the replaced one among
	// its other listed venue names, fills its unknown start and open time,
	// makes the candidate's Artist a performer of the event (unless it is an
	// Organizer's event) and clears the concert candidate.
	ApproveResolutionAdoptCandidate
)

// ApproveResult is the outcome of an approval attempt. A nil Conflict means the
// concert was published (or reconciled) and the concert candidate cleared. A non-nil
// Conflict means a duplicate was detected with no resolution chosen: no state was
// mutated and the caller must re-invoke Approve with a resolution.
type ApproveResult struct {
	// Conflict is set only on an unresolved duplicate; nil otherwise.
	Conflict *DuplicateConflict
}

// DuplicateConflict describes a concert candidate that maps onto an already-published
// event, carrying both records so the reviewer can choose a resolution.
type DuplicateConflict struct {
	// Existing is the already-published event's display-field preview.
	Existing *ExistingEventDisplay
	// ConcertCandidate is the concert candidate the reviewer proposed to publish.
	ConcertCandidate *entity.ConcertCandidate
	// ConcertCandidatePerformer is the artist the concert candidate was discovered for.
	ConcertCandidatePerformer *entity.Artist
}

// ExistingEventDisplay is the display-field preview of an already-published event
// that a concert candidate collides with. Venue identity is intentionally omitted —
// reconciliation never changes it.
type ExistingEventDisplay struct {
	// EventID is the unique identifier of the already-published event.
	EventID string
	// Title is the descriptive title of the existing event (from its series).
	Title string
	// ListedVenueName is the venue name currently stored on the existing event.
	ListedVenueName string
	// LocalDate is the local calendar date of the existing event.
	LocalDate time.Time
	// StartTime is the existing event's wall-clock start time at its venue; nil
	// when unknown.
	StartTime *entity.TimeOfDay
	// OpenTime is the existing event's wall-clock door-open time; nil when
	// unknown.
	OpenTime *entity.TimeOfDay
}

// ConcertCandidateUseCase is the admin-console approval gate over
// AI-discovered concerts: the review queue of ConcertCandidates, approving one
// into a published event, or rejecting it into the RejectedConcert record.
type ConcertCandidateUseCase interface {
	// List returns all concert candidates currently awaiting review, each
	// paired with the performing artist resolved per row. Results are ordered
	// oldest-first (review-queue order).
	//
	// # Possible errors
	//
	//  - Internal: If the repository query or a per-row artist lookup fails.
	List(ctx context.Context) ([]*ConcertCandidateReview, error)

	// Approve promotes a pending concert candidate to a published event. It resolves
	// or creates the venues row, and — when the concert candidate does not duplicate
	// an existing event — builds the Concert/Series/Event entities, inserts them
	// (Concert.Create announces CONCERT.created), and deletes the concert
	// candidate. The operation is
	// idempotent: if the concert candidate is already gone (e.g. double-click) the method
	// returns a non-conflict success without duplicating.
	//
	// Collision is decided with the Events at the resolved Venue and date and
	// the Artist's Events on the date (decideFoundEvent, shared with
	// discovery): the candidate collides with an Event there with the same
	// start time (or both unknown), with any timed Event there when it has no
	// start time, and with its Artist's Event on the date with the same start
	// time at another Venue. A non-colliding candidate fills its Artist's
	// unknown-start Event there, or is published under its Series. A
	// colliding one depends on resolution:
	//   - ApproveResolutionUnspecified: returns an ApproveResult whose Conflict is
	//     set to the existing event's display fields plus the candidate preview; NO
	//     state is mutated.
	//   - ApproveResolutionKeepExisting: records the concert candidate in the
	//     rejection log (attributed to reviewerID) with a duplicate reason,
	//     keeps its listed venue name among the event's other listed venue
	//     names, makes its Artist a performer of the event and clears it.
	//   - ApproveResolutionAdoptCandidate: replaces the event's listed venue
	//     name with the candidate's, keeping the replaced one, fills its start
	//     and open time only where unknown, makes the Artist a performer of
	//     the event, leaving venue identity and the shared series title
	//     unchanged, then clears the concert candidate.
	//
	// Making the Artist a performer of another Artist's Event merges the
	// Artist's unknown-start Event at the Venue and date into it when there is
	// one (Concert.Merge, which tells nobody again), and stores the Artist's
	// concert in its slot otherwise (Concert.Create, which tells the Artist's
	// followers). An Organizer's Event keeps its performers.
	//
	// reviewerID is the calling reviewer's identity (used only by KeepExisting).
	//
	// Before any change, Approve decides the Venue's first-level Area and time
	// zone: the Area that area (a code or a name the admin typed) resolves to,
	// else the one the candidate's admin area resolves to; the time zone
	// timeZone when given, else the Area's own, else the candidate's when it
	// is one of the Area's country's zones. A Venue that must be created for
	// a resolved place reads the place's coordinates from the map catalog.
	//
	// # Possible errors
	//
	//  - InvalidArgument: If no Area or no time zone can be decided, or the
	//    given time zone is not one a Venue in that Area may have.
	//  - Unavailable: If the map catalog cannot be reached for a new Venue's
	//    coordinates.
	//  - Internal: If the venue, series, or event mutation fails.
	Approve(ctx context.Context, concertCandidateID string, resolution ApproveResolution, area, timeZone *string, reviewerID string) (*ApproveResult, error)

	// Reject records the concert candidate in the rejection log and deletes the
	// concert candidate. It is idempotent: if the concert candidate is already gone, the
	// method returns success without creating a duplicate log entry.
	//
	// # Possible errors
	//
	//  - NotFound: If the concert candidate does not exist (idempotent — treated as
	//    success internally).
	//  - Internal: If the log append or delete fails.
	Reject(ctx context.Context, concertCandidateID string, reason string, reviewedBy string) error
}

// concertCandidateUseCase implements [ConcertCandidateUseCase] over the
// concert use case's repositories and venue/duplicate helpers.
type concertCandidateUseCase struct {
	*concertUseCase
	// places reads a new Venue's coordinates by its place id.
	places entity.VenueMapCatalog
}

var _ ConcertCandidateUseCase = (*concertCandidateUseCase)(nil)

// NewConcertCandidateUseCase creates the ConcertCandidate review use case on
// the dependencies of concerts and the map catalog that gives a new Venue
// its coordinates.
func NewConcertCandidateUseCase(concerts *concertUseCase, places entity.VenueMapCatalog) ConcertCandidateUseCase {
	return &concertCandidateUseCase{concertUseCase: concerts, places: places}
}

// List returns all concert candidates awaiting review, each paired with
// the resolved performing artist.
func (uc *concertCandidateUseCase) List(ctx context.Context) ([]*ConcertCandidateReview, error) {
	candidate, err := uc.concertCandidateRepo.ListPending(ctx)
	if err != nil {
		return nil, fmt.Errorf("list pending concert candidates: %w", err)
	}

	reviews := make([]*ConcertCandidateReview, 0, len(candidate))
	for _, sc := range candidate {
		artist, err := uc.artistRepo.Get(ctx, sc.ArtistID)
		if err != nil {
			return nil, fmt.Errorf("get artist %q for concert candidate %q: %w", sc.ArtistID, sc.ID, err)
		}
		reviews = append(reviews, &ConcertCandidateReview{
			ConcertCandidate: sc,
			Performer:        artist,
		})
	}

	return reviews, nil
}

// dropSkippedTimes returns the candidate without a start or open time that
// does not exist on its date at the Venue it is approved at, because the
// clocks skip it there: such a discovered time is unknown. The zone the
// candidate was discovered with may be missing or another one, so this is
// checked once the Venue is decided.
func (uc *concertCandidateUseCase) dropSkippedTimes(ctx context.Context, sc *entity.ConcertCandidate, venueID string) (*entity.ConcertCandidate, error) {
	if sc.StartTime == nil && sc.OpenTime == nil {
		return sc, nil
	}
	venue, err := uc.venueRepo.Get(ctx, venueID)
	if err != nil {
		return nil, fmt.Errorf("get venue %s: %w", venueID, err)
	}
	loc := venue.Location()
	if loc == nil {
		return sc, nil
	}
	kept := *sc
	for _, t := range []**entity.TimeOfDay{&kept.StartTime, &kept.OpenTime} {
		if *t == nil {
			continue
		}
		if _, err := entity.LocalInstant(kept.LocalDate, **t, loc); err != nil {
			uc.logger.Warn(ctx, "approve: a time does not exist at the venue, publishing it as unknown",
				slog.String("concert_candidate_id", kept.ID), slog.String("time", (*t).String()),
				slog.String("time_zone", loc.String()))
			*t = nil
		}
	}
	return &kept, nil
}

// Approve promotes a pending concert candidate to a published event, reconciling a
// duplicate existing event per the resolution when one is detected.
func (uc *concertCandidateUseCase) Approve(ctx context.Context, concertCandidateID string, resolution ApproveResolution, area, timeZone *string, reviewerID string) (*ApproveResult, error) {
	sc, err := uc.concertCandidateRepo.GetByID(ctx, concertCandidateID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// Idempotent: concert candidate already gone (already approved or rejected).
			// This also covers the second phase of a two-call reconcile where the
			// first call already cleared the row.
			uc.logger.Info(ctx, "approve: concert candidate already gone — treating as success",
				slog.String("concert_candidate_id", concertCandidateID),
			)
			return &ApproveResult{}, nil
		}
		return nil, fmt.Errorf("get concert candidate: %w", err)
	}

	spec, err := uc.decideVenue(ctx, sc, area, timeZone)
	if err != nil {
		return nil, err
	}
	venueID, err := findOrCreateVenue(ctx, spec, uc.venueRepo, uc.logger)
	if err != nil {
		return nil, fmt.Errorf("resolve or create venue for concert candidate %q: %w", concertCandidateID, err)
	}

	sc, err = uc.dropSkippedTimes(ctx, sc, venueID)
	if err != nil {
		return nil, err
	}

	venueEvents, err := uc.concertRepo.FindEventsByVenueAndDate(ctx, []string{venueID}, []time.Time{sc.LocalDate})
	if err != nil {
		return nil, fmt.Errorf("find events at the venue of concert candidate %q: %w", concertCandidateID, err)
	}
	artistEvents, err := uc.concertRepo.FindEventsByArtistAndDate(ctx, sc.ArtistID, []time.Time{sc.LocalDate})
	if err != nil {
		return nil, fmt.Errorf("find events of the artist of concert candidate %q: %w", concertCandidateID, err)
	}

	// Decided on every call, so a two-phase reconcile re-validates. Nothing
	// changes before a conflict is returned.
	found := foundEvent{artistID: sc.ArtistID, listedVenueName: sc.ListedVenueName, venueID: venueID, localDate: sc.LocalDate, startTime: sc.StartTime}
	d := decideFoundEvent(found, artistEvents, venueEvents, map[string]bool{})
	switch d.outcome {
	case outcomeFill, outcomePublish:
		return uc.publishCandidate(ctx, sc, venueID, d)
	default:
		return uc.reconcileDuplicate(ctx, sc, d.event, venueEvents, resolution, reviewerID)
	}
}

// publishCandidate stores a candidate that collides with nothing: it fills
// the Artist's unknown-start event at the Venue and date (outcomeFill), or
// adds the candidate as a Concert under its Series (Concert.Create, which
// announces it), never creating a Series. It then removes the candidate and,
// when nothing was added, its Series if that is left empty.
func (uc *concertCandidateUseCase) publishCandidate(ctx context.Context, sc *entity.ConcertCandidate, venueID string, d sameConcertDecision) (*ApproveResult, error) {
	var insertedIDs []string
	if d.outcome == outcomeFill {
		if err := uc.concertRepo.FillEventStartTimes(ctx, []string{d.event.ID}, []*entity.TimeOfDay{sc.StartTime}, []*entity.TimeOfDay{sc.OpenTime}); err != nil {
			return nil, fmt.Errorf("fill event start times for concert candidate %q: %w", sc.ID, err)
		}
	} else {
		ids, err := uc.concertRepo.Create(ctx, candidateToEvent(sc).ToConcertUnderSeries(sc.ArtistID, sc.SeriesID, entity.NewID(), venueID))
		if err != nil {
			return nil, fmt.Errorf("create concert for concert candidate %q: %w", sc.ID, err)
		}
		insertedIDs = ids
	}

	// Delete the concert candidate only after a successful publish/fill.
	if err := uc.concertCandidateRepo.Delete(ctx, sc.ID); err != nil {
		return nil, fmt.Errorf("delete concert candidate after approval: %w", err)
	}
	// When the approval only filled an existing row and this concert
	// candidate was the last reference to a discovery-minted series, that
	// series is now orphaned. A series that gained an event is not, so the
	// sweep is a no-op there.
	if len(insertedIDs) == 0 {
		uc.sweepSeries(ctx, sc.SeriesID, sc.ID)
	}
	uc.logger.Info(ctx, "concert candidate approved and published", analyticslog.Attrs(analyticslog.TopicCatalogReview,
		slog.String("artist_id", sc.ArtistID),
		slog.String("series_id", sc.SeriesID),
		slog.String("concert_candidate_id", sc.ID),
		slog.Int("inserted", len(insertedIDs)),
	)...)
	return &ApproveResult{}, nil
}

// sweepSeries removes the Series if it is left with no event and no
// concert candidate. A failure is logged, not returned: an orphaned series
// row is harmless and swept on the next run.
func (uc *concertCandidateUseCase) sweepSeries(ctx context.Context, seriesID, concertCandidateID string) {
	if _, err := uc.seriesRepo.DeleteOrphaned(ctx, []string{seriesID}); err != nil {
		uc.logger.Error(ctx, "failed to clean up orphaned series after approval", err,
			slog.String("concert_candidate_id", concertCandidateID),
			slog.String("series_id", seriesID),
		)
	}
}

// decideVenue decides, before any change, the first-level Area and time zone
// of the candidate's Venue (see Approve) and returns what the Venue is found
// by or created with.
func (uc *concertCandidateUseCase) decideVenue(ctx context.Context, sc *entity.ConcertCandidate, area, timeZone *string) (venueSpec, error) {
	text := sc.AdminArea
	if area != nil {
		text = area
	}
	if text == nil {
		return venueSpec{}, apperr.New(codes.InvalidArgument, "the concert candidate has no admin area; give one",
			slog.String("concert_candidate_id", sc.ID))
	}
	resolved, err := uc.areas.Resolve(ctx, *text, nil)
	if errors.Is(err, apperr.ErrNotFound) || errors.Is(err, apperr.ErrInvalidArgument) {
		return venueSpec{}, apperr.New(codes.InvalidArgument, "the admin area is no first-level Area",
			slog.String("concert_candidate_id", sc.ID), slog.String("admin_area", *text))
	}
	if err != nil {
		return venueSpec{}, fmt.Errorf("resolve the admin area: %w", err)
	}
	country, err := uc.areas.ListByCountry(ctx, resolved.CountryCode)
	if err != nil {
		return venueSpec{}, fmt.Errorf("list the time zones of %s: %w", resolved.CountryCode, err)
	}
	zones := make([]string, 0, len(country.TimeZones))
	for _, z := range country.TimeZones {
		zones = append(zones, z.Name)
	}

	zone := decideZone(timeZone, resolved, sc.TimeZone, zones)
	if zone == nil {
		return venueSpec{}, apperr.New(codes.InvalidArgument, "the venue's time zone is unknown; give one",
			slog.String("concert_candidate_id", sc.ID), slog.String("admin_area", resolved.Code))
	}
	venue := &entity.Venue{AdminArea: &resolved.Code, TimeZone: zone}
	if err := venue.CheckArea(resolved, zones); err != nil {
		return venueSpec{}, err
	}

	spec := venueSpec{listedName: sc.ListedVenueName, adminArea: resolved.Code, timeZone: *zone, placeID: sc.ResolvedPlaceID}
	if sc.ResolvedPlaceID != nil {
		spec.coordinates = uc.placeCoordinates(*sc.ResolvedPlaceID)
	}
	return spec, nil
}

// decideZone is the admin's zone when given, else the Area's own, else the
// candidate's when it is one of the country's zones; nil when none applies.
func decideZone(given *string, area *entity.Area, discovered *string, zones []string) *string {
	switch {
	case given != nil:
		return given
	case area.TimeZone != nil:
		return area.TimeZone
	case discovered != nil && slices.Contains(zones, *discovered):
		return discovered
	}
	return nil
}

// placeCoordinates reads a place's coordinates from the map catalog; a place
// the catalog no longer has gives none.
func (uc *concertCandidateUseCase) placeCoordinates(placeID string) func(context.Context) (*entity.PlaceCoordinates, error) {
	return func(ctx context.Context) (*entity.PlaceCoordinates, error) {
		c, err := uc.places.GetPlaceCoordinates(ctx, placeID)
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, nil
		}
		return c, err
	}
}

// reconcileDuplicate applies the reviewer's resolution to a candidate that
// collides with existing; venueEvents are the events at the candidate's
// Venue and date.
func (uc *concertCandidateUseCase) reconcileDuplicate(ctx context.Context, sc *entity.ConcertCandidate, existing *entity.Concert, venueEvents []*entity.Concert, resolution ApproveResolution, reviewerID string) (*ApproveResult, error) {
	switch resolution {
	case ApproveResolutionKeepExisting:
		reason := fmt.Sprintf("duplicate of existing event %s", existing.ID)
		if err := uc.appendRejectionLog(ctx, sc, reason, reviewerID); err != nil {
			return nil, fmt.Errorf("keep-existing: append rejection log: %w", err)
		}
		if err := uc.concertRepo.AddOtherListedVenueName(ctx, existing.ID, sc.ListedVenueName); err != nil {
			return nil, fmt.Errorf("keep-existing: add other listed venue name: %w", err)
		}

	case ApproveResolutionAdoptCandidate:
		// Replace the display venue name (keeping the replaced one) and fill
		// start/open only where unknown, leaving venue identity and the shared
		// series title untouched.
		if err := uc.concertRepo.UpdateEventListedVenueName(ctx, existing.ID, sc.ListedVenueName); err != nil {
			return nil, fmt.Errorf("adopt-candidate: update listed venue name: %w", err)
		}
		if err := uc.concertRepo.FillEventStartTimes(ctx,
			[]string{existing.ID},
			[]*entity.TimeOfDay{sc.StartTime},
			[]*entity.TimeOfDay{sc.OpenTime},
		); err != nil {
			return nil, fmt.Errorf("adopt-candidate: fill start/open times: %w", err)
		}

	default:
		// Unspecified: surface the conflict for reviewer choice; do not mutate.
		conflict, err := uc.buildDuplicateConflict(ctx, sc, &existing.Event)
		if err != nil {
			return nil, err
		}
		uc.logger.Info(ctx, "approve: duplicate event detected — returning conflict for reviewer choice",
			slog.String("concert_candidate_id", sc.ID),
			slog.String("existing_event_id", existing.ID),
		)
		return &ApproveResult{Conflict: conflict}, nil
	}

	if err := uc.shareBill(ctx, sc, existing, venueEvents); err != nil {
		return nil, err
	}
	if err := uc.concertCandidateRepo.Delete(ctx, sc.ID); err != nil {
		return nil, fmt.Errorf("reconcile: delete concert candidate: %w", err)
	}
	uc.logger.Info(ctx, "duplicate reconciled", analyticslog.Attrs(analyticslog.TopicCatalogReview,
		slog.String("concert_candidate_id", sc.ID),
		slog.String("existing_event_id", existing.ID),
		slog.Int("resolution", int(resolution)),
	)...)
	return &ApproveResult{}, nil
}

// shareBill makes the candidate's Artist a performer of existing when the
// Artist does not perform there and it has no publish state ("Another
// Artist's Event becomes a shared bill"): it merges the Artist's
// unknown-start event at the Venue and date into it when there is one,
// removing that event's Series if it is left empty, and otherwise stores the
// Artist's concert in its slot, which tells the Artist's followers.
func (uc *concertCandidateUseCase) shareBill(ctx context.Context, sc *entity.ConcertCandidate, existing *entity.Concert, venueEvents []*entity.Concert) error {
	if existing.HasPerformer(sc.ArtistID) || existing.PublishState != nil {
		return nil
	}
	own := firstOf(venueEvents, func(e *entity.Concert) bool {
		return e.ID != existing.ID && e.HasPerformer(sc.ArtistID) && e.StartTime == nil && e.PublishState == nil
	})
	if own != nil {
		if err := uc.concertRepo.Merge(ctx, own.ID, existing.ID); err != nil {
			return fmt.Errorf("shared bill: merge event %s into %s: %w", own.ID, existing.ID, err)
		}
		uc.sweepSeries(ctx, own.SeriesID, sc.ID)
		return nil
	}
	// The Concert takes the Event's own times, so storing it in the slot
	// changes neither of them.
	c := candidateToEvent(sc).ToConcertUnderSeries(sc.ArtistID, existing.SeriesID, entity.NewID(), existing.VenueID)
	c.StartTime, c.OpenTime = existing.StartTime, existing.OpenTime
	if _, err := uc.concertRepo.Create(ctx, c); err != nil {
		return fmt.Errorf("shared bill: add the artist to event %s: %w", existing.ID, err)
	}
	return nil
}

// buildDuplicateConflict assembles the conflict DTO: the existing event's display
// fields (its title resolved from the shared series) plus the candidate preview and
// its performer.
func (uc *concertCandidateUseCase) buildDuplicateConflict(ctx context.Context, sc *entity.ConcertCandidate, existing *entity.Event) (*DuplicateConflict, error) {
	series, err := uc.seriesRepo.Get(ctx, existing.SeriesID)
	if err != nil {
		return nil, fmt.Errorf("get series for duplicate conflict: %w", err)
	}
	performer, err := uc.artistRepo.Get(ctx, sc.ArtistID)
	if err != nil {
		return nil, fmt.Errorf("get performer for duplicate conflict: %w", err)
	}

	listedName := ""
	if existing.ListedVenueName != nil {
		listedName = *existing.ListedVenueName
	}

	return &DuplicateConflict{
		Existing: &ExistingEventDisplay{
			EventID:         existing.ID,
			Title:           series.Title,
			ListedVenueName: listedName,
			LocalDate:       existing.LocalDate,
			StartTime:       existing.StartTime,
			OpenTime:        existing.OpenTime,
		},
		ConcertCandidate:          sc,
		ConcertCandidatePerformer: performer,
	}, nil
}

// Reject records the concert candidate in the rejection log and deletes the
// concert candidate.
func (uc *concertCandidateUseCase) Reject(ctx context.Context, concertCandidateID string, reason string, reviewedBy string) error {
	sc, err := uc.concertCandidateRepo.GetByID(ctx, concertCandidateID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			// Idempotent: concert candidate already gone.
			uc.logger.Info(ctx, "reject: concert candidate already gone — treating as success",
				slog.String("concert_candidate_id", concertCandidateID),
			)
			return nil
		}
		return fmt.Errorf("get concert candidate: %w", err)
	}

	if err := uc.appendRejectionLog(ctx, sc, reason, reviewedBy); err != nil {
		return err
	}

	if err := uc.concertCandidateRepo.Delete(ctx, concertCandidateID); err != nil {
		return fmt.Errorf("delete concert candidate after rejection: %w", err)
	}

	// Sweep the parent series if this rejection left it with no events and no
	// other pending concert candidates (a discovered series whose every event was staged
	// and then rejected). Scoped to just this concert candidate's series.
	if _, err := uc.seriesRepo.DeleteOrphaned(ctx, []string{sc.SeriesID}); err != nil {
		uc.logger.Error(ctx, "failed to clean up orphaned series after rejection", err,
			slog.String("concert_candidate_id", concertCandidateID),
		)
		// Non-fatal: an orphaned series row is harmless and swept on the next run.
	}

	uc.logger.Info(ctx, "concert candidate rejected and logged", analyticslog.Attrs(analyticslog.TopicCatalogReview,
		slog.String("artist_id", sc.ArtistID),
		slog.String("concert_candidate_id", concertCandidateID),
		slog.String("reason", reason),
	)...)

	return nil
}

// appendRejectionLog captures a concert candidate in the rejection log with the
// given reason and reviewer identity. The artist name is snapshotted at write
// time for readability even if the artist is later deleted. Shared by Reject and
// the approval keep-existing reconciliation.
func (uc *concertCandidateUseCase) appendRejectionLog(ctx context.Context, sc *entity.ConcertCandidate, reason, reviewedBy string) error {
	artist, err := uc.artistRepo.Get(ctx, sc.ArtistID)
	if err != nil {
		return fmt.Errorf("get artist for rejection log: %w", err)
	}

	logID := entity.NewID()

	var rbPtr *string
	if reviewedBy != "" {
		rb := reviewedBy
		rbPtr = &rb
	}

	logEntry := &entity.RejectedConcert{
		ID:              logID,
		ArtistID:        sc.ArtistID,
		ArtistName:      artist.Name,
		Title:           sc.Title,
		LocalDate:       sc.LocalDate,
		StartTime:       sc.StartTime,
		OpenTime:        sc.OpenTime,
		ListedVenueName: sc.ListedVenueName,
		AdminArea:       sc.AdminArea,
		SourceURL:       sc.SourceURL,
		ResolvedPlaceID: sc.ResolvedPlaceID,
		TimeZone:        sc.TimeZone,
		Reason:          reason,
		ReviewedBy:      rbPtr,
	}

	if err := uc.rejectedConcertRepo.Append(ctx, logEntry); err != nil {
		return fmt.Errorf("append rejection log: %w", err)
	}
	return nil
}

// venueSpec is what a Venue is found by, or created with when none is
// found: its listed venue name, its first-level Area and time zone, and the
// map place it was resolved to.
type venueSpec struct {
	listedName string
	adminArea  string
	timeZone   string
	placeID    *string
	// coordinates reads the place's coordinates; it runs only when the
	// Venue is created, and is nil for a venue without a place.
	coordinates func(ctx context.Context) (*entity.PlaceCoordinates, error)
}

// findOrCreateVenue is an idempotent get-or-create over the venues row,
// place_id-authoritative: by place id, then by (listed venue name, admin
// area), giving that Venue the place id when it has none, and only creating
// when neither matches. A created Venue is named by its listed venue name,
// never by a name read from the map catalog. A Venue that is found keeps its
// own name, area and time zone.
func findOrCreateVenue(ctx context.Context, spec venueSpec, venueRepo entity.VenueRepository, logger *logging.Logger) (string, error) {
	if spec.placeID != nil {
		existing, err := venueRepo.GetByPlaceID(ctx, *spec.placeID)
		if err == nil {
			return existing.ID, nil
		}
		if !errors.Is(err, apperr.ErrNotFound) {
			return "", fmt.Errorf("get venue by place ID: %w", err)
		}
		// A place id miss falls through to the listed-name lookup: the catalog
		// can return a different place id for a venue that already exists
		// under (listed_venue_name, admin_area).
	}

	existing, err := venueRepo.GetByListedName(ctx, spec.listedName, spec.adminArea)
	if err == nil {
		if spec.placeID != nil && existing.GooglePlaceID == nil {
			if err := venueRepo.BackfillPlaceID(ctx, existing.ID, *spec.placeID); err != nil {
				return "", fmt.Errorf("backfill venue place ID: %w", err)
			}
		}
		return existing.ID, nil
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		return "", fmt.Errorf("get venue by listed name: %w", err)
	}

	listed, area, zone := spec.listedName, spec.adminArea, spec.timeZone
	venue := &entity.Venue{
		ID:              entity.NewID(),
		Name:            listed,
		AdminArea:       &area,
		TimeZone:        &zone,
		GooglePlaceID:   spec.placeID,
		ListedVenueName: &listed,
	}
	if spec.coordinates != nil {
		c, err := spec.coordinates(ctx)
		if err != nil {
			return "", fmt.Errorf("read the place's coordinates: %w", err)
		}
		if c != nil && c.Coordinates != nil {
			fetch := c.FetchTime
			venue.Coordinates, venue.CoordinatesFetchTime = c.Coordinates, &fetch
		}
	}
	// Create returns the surviving row id: the one generated here on a real
	// insert, or an existing row's id when a concurrent create won the race.
	venueID, err := venueRepo.Create(ctx, venue)
	if err != nil {
		return "", fmt.Errorf("create venue: %w", err)
	}
	logger.Info(ctx, "created venue",
		slog.String("venue_id", venueID),
		slog.String("venue_name", venue.Name),
		slog.String("admin_area", area),
		slog.String("time_zone", zone),
	)
	return venueID, nil
}

// candidateToEvent converts a ConcertCandidate into a DiscoveredEvent so an
// approved concert candidate is turned into a Concert the way discovery turns
// a found event into one. The series identity is NOT reconstructed here — it
// is carried separately via ConcertCandidate.SeriesID.
func candidateToEvent(sc *entity.ConcertCandidate) *entity.DiscoveredEvent {
	ev := &entity.DiscoveredEvent{
		ListedVenueName: sc.ListedVenueName,
		LocalDate:       sc.LocalDate,
	}
	ev.StartTime = sc.StartTime
	ev.OpenTime = sc.OpenTime
	if sc.AdminArea != nil {
		ev.AdminArea = sc.AdminArea
	}
	return ev
}
