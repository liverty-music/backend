package usecase

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// DraftEventInput is the intermediate representation of an event being authored,
// carrying the venue name the member saw and the place id of the map place
// the member picked alongside the timing fields. The usecase finds or creates
// the Venue holding that place id for a new or DRAFT event; a PUBLISHED or
// CANCELLED event keeps its Venue.
type DraftEventInput struct {
	// EventID is the id of the series' Event this input edits; nil for a new
	// date, which is added as a DRAFT Event. Used by Update only.
	EventID *string
	// VenueName is the venue name as the member saw it for the picked place,
	// kept as the event's listed venue name.
	VenueName string
	// PlaceID is the map catalog place id of the place the member picked.
	// Required: an event without one is rejected.
	PlaceID *string
	// AdminArea is the code of the first-level Area the member saved for a
	// new place; ignored when a Venue already holds the place id.
	AdminArea *string
	// TimeZone is the IANA time zone the member saved for a new place;
	// ignored when a Venue already holds the place id.
	TimeZone *string
	// LocalDate is the calendar date of the performance (midnight UTC).
	LocalDate time.Time
	// StartTime and OpenTime are wall-clock times at the venue, read in its
	// time zone.
	StartTime *entity.TimeOfDay
	OpenTime  *entity.TimeOfDay
	// PerAccountLimit is the most tickets one account may hold for the event;
	// nil when none is given. A new or DRAFT event then has the limit
	// [entity.EventDefaultPerAccountLimit]; a PUBLISHED or CANCELLED event
	// keeps its own.
	PerAccountLimit *int
}

// SeriesUseCase defines the organizer-facing authoring surface for
// first-party concerts. All methods are scoped to a single calling organizer;
// authorization (ownership) is enforced before delegating to the repository.
type SeriesUseCase interface {
	// Create authors a new first-party series whose events are all DRAFT. Each
	// event's venue is the Venue holding the event's picked place id, or a new
	// Venue with the venue name, admin area and time zone the member saved and
	// the map catalog place's coordinates, created before the series is
	// stored. Nothing is announced. Returns the full authored concert.
	//
	// # Possible errors
	//
	//  - InvalidArgument: A required field is missing (including an event's
	//    place id, and a new place's venue name, admin area or time zone), the
	//    admin area is not a first-level Area or the time zone is not one a
	//    Venue in it may have, the event list is empty, a time is inconsistent
	//    (open > start, date in the past), or the map catalog has no place with
	//    an event's place id.
	//  - PermissionDenied: A performer is not among the caller's represented
	//    artists. Non-revealing.
	//  - Unavailable: The map catalog cannot be reached for a new place.
	Create(ctx context.Context, callerOrgID string, draft *entity.Series, eventInputs []*DraftEventInput, performerArtistIDs []string) (*entity.Series, []*entity.Concert, error)

	// Update edits a concert: everything while every event is DRAFT, and
	// corrections once it has PUBLISHED or CANCELLED events. An input with an
	// EventID edits that event; one without adds a DRAFT event; a DRAFT event
	// left out is removed. A PUBLISHED or CANCELLED event needs no place id,
	// but its venue must stay the same. Nothing is announced.
	//
	// # Possible errors
	//
	//  - PermissionDenied: The series does not exist or is not owned by the
	//    caller, or a performer is not represented. Non-revealing.
	//  - InvalidArgument, Unavailable: As Create for new and DRAFT events. A
	//    PUBLISHED or CANCELLED event needs no place id, and its date is not
	//    checked against the current day.
	//  - FailedPrecondition: A PUBLISHED or CANCELLED event is left out; an
	//    EventID is not an event of the series; a PUBLISHED event's venue or
	//    date differs; a CANCELLED event differs in any value; the type,
	//    source page or visibility of a series with a PUBLISHED or CANCELLED
	//    event differs; a PUBLISHED event's start time changes while a
	//    TicketSale offers it; or its new slot is blocked. Nothing changes.
	Update(ctx context.Context, callerOrgID, seriesID string, draft *entity.Series, eventInputs []*DraftEventInput, performerArtistIDs []string) (*entity.Series, []*entity.Concert, error)

	// Publish publishes the named DRAFT events of a series, or every DRAFT
	// event when none is named. For a PUBLIC series it announces the newly
	// added events once per performer. An UNLISTED series that has no share
	// token yet gets a new random one together with the publish
	// (Series.PublishEvents with that token); a series that already has one
	// keeps it. When the publish fails nothing is stored, the token included,
	// so publishing again gives the series its token together with its
	// events. Returns the authored concert.
	//
	// # Possible errors
	//
	//  - PermissionDenied: The series does not exist or is not owned by the
	//    caller. Non-revealing.
	//  - FailedPrecondition: The title is blank, there is no performer, an
	//    event to publish has no venue or date, a named event is not DRAFT,
	//    no event is named and none is DRAFT, or a slot is held by another
	//    organizer or by a CANCELLED event.
	//  - Any error of Series.PublishEvents, with its own code.
	Publish(ctx context.Context, callerOrgID, seriesID string, eventIDs []string) (*entity.Series, []*entity.Concert, error)

	// Cancel cancels the named events of a series, or every PUBLISHED and
	// DRAFT event when none is named: a PUBLISHED event becomes CANCELLED, a
	// DRAFT event is removed, and a series left with no event is removed. It
	// announces the events that became CANCELLED, and nothing when none did.
	//
	// # Possible errors
	//
	//  - PermissionDenied: The series does not exist or is not owned by the
	//    caller. Non-revealing.
	//  - FailedPrecondition: A named event is not an event of the series or is
	//    already CANCELLED, or no event is named and none is PUBLISHED or DRAFT.
	Cancel(ctx context.Context, callerOrgID, seriesID string, eventIDs []string) error

	// RegenerateShareURL issues a fresh share token for an UNLISTED series,
	// invalidating the previous URL. Returns the new share URL.
	//
	// # Possible errors
	//
	//  - NotFound: The series does not exist.
	//  - PermissionDenied: The series is not owned by the caller. Non-revealing.
	//  - FailedPrecondition: The series is not UNLISTED.
	RegenerateShareURL(ctx context.Context, callerOrgID, seriesID string) (string, error)

	// List returns all first-party series owned by the caller's organizer,
	// ordered newest first, and the Concerts of every one of them (one per
	// event, each with its own performers), series by series.
	//
	// # Possible errors
	//
	//  - InvalidArgument: The organizerID is empty.
	List(ctx context.Context, callerOrgID string) ([]*entity.Series, []*entity.Concert, error)

	// SetCoverImage verifies that the caller owns the target series and uploaded
	// the series image, inserts the series image row idempotently, and publishes
	// SERIES_IMAGE.uploaded{series_image_id, series_id}. It does NOT re-point
	// series_cover_images — the series-image-consumer does that after generating
	// variants. Repeating it announces again while the original is still
	// uploaded, and succeeds without announcing once the original was processed.
	//
	// # Possible errors
	//
	//  - InvalidArgument: if series_id or series_image_id is empty.
	//  - PermissionDenied: if the series is not owned by the caller (non-revealing),
	//    or the series image is recorded for another organizer.
	//  - FailedPrecondition: if no original was uploaded for the series image
	//    under the caller's organizer.
	//  - Unavailable: if SERIES_IMAGE.uploaded cannot be published.
	//  - Internal: if image storage is not configured, or the storage read or
	//    the DB insert fails.
	SetCoverImage(ctx context.Context, callerOrgID, seriesID, seriesImageID string) error
}

// seriesUseCase implements SeriesUseCase.
type seriesUseCase struct {
	seriesRepo      entity.SeriesRepository
	seriesImageRepo entity.SeriesImageRepository
	venueRepo       entity.VenueRepository
	mapCatalog      entity.VenueMapCatalog
	organizerRepo   entity.OrganizerRepository
	ticketSaleRepo  entity.TicketSaleRepository
	imageStorer     ImageStorer
	storageBuckets  StorageBuckets
	logger          *logging.Logger
	now             func() time.Time
}

// Compile-time interface check.
var _ SeriesUseCase = (*seriesUseCase)(nil)

// NewSeriesUseCase creates a new SeriesUseCase. All fields are required, except
// that imageStorer may be nil in local development; SetCoverImage then fails
// with Internal. The Series image upload lives in SeriesImageUseCase.
func NewSeriesUseCase(
	seriesRepo entity.SeriesRepository,
	seriesImageRepo entity.SeriesImageRepository,
	venueRepo entity.VenueRepository,
	mapCatalog entity.VenueMapCatalog,
	organizerRepo entity.OrganizerRepository,
	ticketSaleRepo entity.TicketSaleRepository,
	imageStorer ImageStorer,
	storageBuckets StorageBuckets,
	logger *logging.Logger,
	now func() time.Time,
) SeriesUseCase {
	return &seriesUseCase{
		seriesRepo:      seriesRepo,
		seriesImageRepo: seriesImageRepo,
		venueRepo:       venueRepo,
		mapCatalog:      mapCatalog,
		organizerRepo:   organizerRepo,
		ticketSaleRepo:  ticketSaleRepo,
		imageStorer:     imageStorer,
		storageBuckets:  storageBuckets,
		logger:          logger,
		now:             now,
	}
}

// assertOwnsSeries verifies that the given series is owned by callerOrgID.
// It returns a non-revealing PermissionDenied error when the check fails.
func (uc *seriesUseCase) assertOwnsSeries(series *entity.Series, callerOrgID string) error {
	if series.OrganizerID == nil || *series.OrganizerID != callerOrgID {
		return apperr.New(codes.PermissionDenied, "permission denied")
	}
	return nil
}

// checkPerformerOwnership returns PermissionDenied (non-revealing) when any of
// the supplied artist IDs is not represented by the caller's organizer.
func (uc *seriesUseCase) checkPerformerOwnership(ctx context.Context, callerOrgID string, artistIDs []string) error {
	ownedArtists, err := uc.organizerRepo.ListArtists(ctx, callerOrgID)
	if err != nil {
		return fmt.Errorf("list organizer artists: %w", err)
	}
	ownedSet := make(map[string]bool, len(ownedArtists))
	for _, a := range ownedArtists {
		ownedSet[a.ID] = true
	}
	for _, id := range artistIDs {
		if !ownedSet[id] {
			return apperr.New(codes.PermissionDenied, "permission denied")
		}
	}
	return nil
}

// generateUnlistedToken generates a cryptographically random 32-byte base64url
// token suitable as an unlisted share token.
func generateUnlistedToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// venuePlan is the Venue of one event's picked place: a held one, or a new
// one that is not stored yet. One plan is shared by the inputs that pick the
// same new place.
type venuePlan struct {
	venue  *entity.Venue
	stored bool
	// raced is set when another writer stored the place first, so venue is
	// theirs and the events were planned against other values.
	raced bool
}

// planVenue returns the Venue for one event's picked place without storing
// anything: the Venue holding the place id when one is held, otherwise a new
// Venue with the venue name, admin area and time zone the member saved and the
// map catalog place's coordinates with their fetch time. The catalog's own
// name, suggested area and zone are not stored. A new place without a saved
// name, area or zone is InvalidArgument; a catalog NotFound becomes
// InvalidArgument (the member sent a place id the catalog does not know);
// Unavailable passes through. newPlaces holds the new Venues planned so far,
// by place id.
//
// The new Venue carries no listed venue name: the member's typed name is
// kept on the event (Event.ListedVenueName), and a listed name on the
// Venue would let Create's (listed_venue_name, admin_area) dedupe merge this
// place into another same-named Venue in the same admin area.
func (uc *seriesUseCase) planVenue(ctx context.Context, inp *DraftEventInput, newPlaces map[string]*venuePlan) (*venuePlan, error) {
	placeID := *inp.PlaceID

	v, err := uc.venueRepo.GetByPlaceID(ctx, placeID)
	if err == nil {
		return &venuePlan{venue: v, stored: true}, nil
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		return nil, fmt.Errorf("get venue by place id: %w", err)
	}
	if plan, ok := newPlaces[placeID]; ok {
		return plan, nil
	}
	if err := newPlaceInputComplete(inp); err != nil {
		return nil, err
	}

	place, err := uc.mapCatalog.GetPlace(ctx, placeID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, apperr.Wrap(err, codes.InvalidArgument, "the picked venue is not a place in the map catalog")
		}
		return nil, fmt.Errorf("get place from map catalog: %w", err)
	}

	venue := &entity.Venue{
		ID:            entity.NewID(),
		Name:          strings.TrimSpace(inp.VenueName),
		AdminArea:     inp.AdminArea,
		TimeZone:      inp.TimeZone,
		GooglePlaceID: &placeID,
	}
	if place.Coordinates != nil {
		fetch := place.CoordinatesFetchTime
		venue.Coordinates = place.Coordinates
		venue.CoordinatesFetchTime = &fetch
	}
	if err := uc.venueRepo.CheckNew(ctx, venue); err != nil {
		return nil, fmt.Errorf("check new venue: %w", err)
	}
	plan := &venuePlan{venue: venue}
	newPlaces[placeID] = plan
	return plan, nil
}

// saveVenue stores a planned Venue that is not held yet and returns the id of
// the Venue the event is at. When another writer stored the place first, every
// event of the place is checked again against that Venue, whose time zone may
// differ.
func (uc *seriesUseCase) saveVenue(ctx context.Context, plan *venuePlan, inp *DraftEventInput) (string, error) {
	if !plan.stored {
		venueID, err := uc.venueRepo.Create(ctx, plan.venue)
		if err != nil {
			return "", fmt.Errorf("create venue: %w", err)
		}
		if venueID != plan.venue.ID {
			held, err := uc.venueRepo.Get(ctx, venueID)
			if err != nil {
				return "", fmt.Errorf("get venue %s: %w", venueID, err)
			}
			plan.venue, plan.raced = held, true
		}
		plan.stored = true
	}
	if plan.raced {
		if err := uc.checkTimesAtVenue(plan.venue, inp); err != nil {
			return "", err
		}
	}
	return plan.venue.ID, nil
}

// newPlaceInputComplete checks that an event for a place no Venue holds gives
// the venue name, admin area and time zone the member saved.
func newPlaceInputComplete(inp *DraftEventInput) error {
	if strings.TrimSpace(inp.VenueName) == "" {
		return apperr.New(codes.InvalidArgument, "an event at a new place must give the venue name")
	}
	if inp.AdminArea == nil || *inp.AdminArea == "" {
		return apperr.New(codes.InvalidArgument, "an event at a new place must give the venue's admin area")
	}
	if inp.TimeZone == nil || *inp.TimeZone == "" {
		return apperr.New(codes.InvalidArgument, "an event at a new place must give the venue's time zone")
	}
	return nil
}

// planDraftEvent checks a new or DRAFT event input and plans its Venue. It
// stores nothing, so a save refused for any of its events creates no Venue.
func (uc *seriesUseCase) planDraftEvent(ctx context.Context, inp *DraftEventInput, newPlaces map[string]*venuePlan) (*venuePlan, error) {
	if inp.PerAccountLimit != nil {
		if err := (&entity.Event{PerAccountLimit: inp.PerAccountLimit}).ValidatePerAccountLimit(); err != nil {
			return nil, err
		}
	}
	plan, err := uc.planVenue(ctx, inp, newPlaces)
	if err != nil {
		return nil, fmt.Errorf("resolve venue for event on %s: %w", inp.LocalDate.Format("2006-01-02"), err)
	}
	if err := uc.checkTimesAtVenue(plan.venue, inp); err != nil {
		return nil, err
	}
	return plan, nil
}

// newDraftEvent stores the planned Venue of a new or DRAFT event input and
// returns the event to store. The id is left as given: empty for a new date.
func (uc *seriesUseCase) newDraftEvent(ctx context.Context, seriesID, eventID string, inp *DraftEventInput, plan *venuePlan) (*entity.Event, error) {
	limit := entity.EventDefaultPerAccountLimit
	if inp.PerAccountLimit != nil {
		limit = *inp.PerAccountLimit
	}
	venueID, err := uc.saveVenue(ctx, plan, inp)
	if err != nil {
		return nil, fmt.Errorf("resolve venue for event on %s: %w", inp.LocalDate.Format("2006-01-02"), err)
	}
	lvn := inp.VenueName
	draft := entity.PublishStateDraft
	return &entity.Event{
		ID:              eventID,
		SeriesID:        seriesID,
		VenueID:         venueID,
		ListedVenueName: &lvn,
		LocalDate:       inp.LocalDate,
		StartTime:       inp.StartTime,
		OpenTime:        inp.OpenTime,
		PublishState:    &draft,
		PerAccountLimit: &limit,
	}, nil
}

// Create authors a new first-party series whose events are all DRAFT.
func (uc *seriesUseCase) Create(
	ctx context.Context,
	callerOrgID string,
	draft *entity.Series,
	eventInputs []*DraftEventInput,
	performerArtistIDs []string,
) (*entity.Series, []*entity.Concert, error) {
	if err := draft.ValidateDraft(len(eventInputs)); err != nil {
		return nil, nil, err
	}

	// Performer ownership check.
	if err := uc.checkPerformerOwnership(ctx, callerOrgID, performerArtistIDs); err != nil {
		return nil, nil, err
	}

	// Validate event timing.
	for _, inp := range eventInputs {
		if err := uc.validateEventInput(inp, true, true); err != nil {
			return nil, nil, err
		}
	}

	// Set authoring fields.
	draft.ID = entity.NewID()
	orgID := callerOrgID
	draft.OrganizerID = &orgID

	// Check every event against its Venue before any Venue is stored.
	newPlaces := make(map[string]*venuePlan)
	plans := make([]*venuePlan, len(eventInputs))
	for i, inp := range eventInputs {
		plan, err := uc.planDraftEvent(ctx, inp, newPlaces)
		if err != nil {
			return nil, nil, err
		}
		plans[i] = plan
	}

	// Store the new venues and build the DRAFT events.
	events := make([]*entity.Event, 0, len(eventInputs))
	for i, inp := range eventInputs {
		e, err := uc.newDraftEvent(ctx, draft.ID, entity.NewID(), inp, plans[i])
		if err != nil {
			return nil, nil, err
		}
		events = append(events, e)
	}

	if err := uc.seriesRepo.CreateDraft(ctx, draft, events, performerArtistIDs); err != nil {
		return nil, nil, fmt.Errorf("create draft: %w", err)
	}

	series, concerts, err := uc.seriesRepo.GetAuthored(ctx, draft.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("get authored after create: %w", err)
	}
	return series, concerts, nil
}

// checkGivenEvents validates each event input of an update: one that names an
// event must name an event of the series, and a new or DRAFT event is checked
// as Create checks it. A PUBLISHED or CANCELLED event keeps its Venue, so its
// place id is not required, and its date is not checked against the current
// day: a past published date can still be corrected.
func (uc *seriesUseCase) checkGivenEvents(eventInputs []*DraftEventInput, stored map[string]*entity.Event) error {
	for _, inp := range eventInputs {
		var current *entity.Event
		if inp.EventID != nil {
			current = stored[*inp.EventID]
			if current == nil {
				return apperr.New(codes.FailedPrecondition, "a given event is not an event of the series")
			}
		}
		isDraft := current == nil || current.HasPublishState(entity.PublishStateDraft)
		if err := uc.validateEventInput(inp, isDraft, isDraft); err != nil {
			return err
		}
	}
	return nil
}

// checkEveryDateGiven fails with FailedPrecondition when an update leaves out
// a stored published or cancelled date: such a date is cancelled, not removed.
func checkEveryDateGiven(storedEvents []*entity.Event, eventInputs []*DraftEventInput) error {
	given := make(map[string]bool, len(eventInputs))
	for _, inp := range eventInputs {
		if inp.EventID != nil {
			given[*inp.EventID] = true
		}
	}
	for _, e := range storedEvents {
		if !given[e.ID] && !e.HasPublishState(entity.PublishStateDraft) {
			return apperr.New(codes.FailedPrecondition,
				"every published or cancelled date must be given; a published date is cancelled, not removed")
		}
	}
	return nil
}

// updatedEvents builds the events of an update from its inputs: a new draft
// for an input that names no event or a DRAFT one, and the correction of a
// published or cancelled event otherwise. Every input is checked before any
// Venue is stored.
func (uc *seriesUseCase) updatedEvents(ctx context.Context, seriesID string, eventInputs []*DraftEventInput, stored map[string]*entity.Event) ([]*entity.Event, error) {
	newPlaces := make(map[string]*venuePlan)
	plans := make([]*venuePlan, len(eventInputs))
	events := make([]*entity.Event, len(eventInputs))
	for i, inp := range eventInputs {
		var err error
		if current := eventNamedBy(inp, stored); current == nil || current.HasPublishState(entity.PublishStateDraft) {
			plans[i], err = uc.planDraftEvent(ctx, inp, newPlaces)
		} else {
			events[i], err = uc.correctedEvent(ctx, current, inp)
		}
		if err != nil {
			return nil, err
		}
	}
	for i, inp := range eventInputs {
		if plans[i] == nil {
			continue
		}
		eventID := ""
		if current := eventNamedBy(inp, stored); current != nil {
			eventID = current.ID
		}
		e, err := uc.newDraftEvent(ctx, seriesID, eventID, inp, plans[i])
		if err != nil {
			return nil, err
		}
		events[i] = e
	}
	return events, nil
}

// eventNamedBy returns the stored event the input names, or nil for a new one.
func eventNamedBy(inp *DraftEventInput, stored map[string]*entity.Event) *entity.Event {
	if inp.EventID == nil {
		return nil
	}
	return stored[*inp.EventID]
}

// Update edits a concert, published or not (design D9).
func (uc *seriesUseCase) Update(
	ctx context.Context,
	callerOrgID, seriesID string,
	draft *entity.Series,
	eventInputs []*DraftEventInput,
	performerArtistIDs []string,
) (*entity.Series, []*entity.Concert, error) {
	existing, storedConcerts, err := uc.seriesRepo.GetAuthored(ctx, seriesID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, nil, apperr.New(codes.PermissionDenied, "permission denied")
		}
		return nil, nil, err
	}
	if err := uc.assertOwnsSeries(existing, callerOrgID); err != nil {
		return nil, nil, err
	}
	storedEvents := entity.EventsOf(storedConcerts)

	// Performer ownership check.
	if err := uc.checkPerformerOwnership(ctx, callerOrgID, performerArtistIDs); err != nil {
		return nil, nil, err
	}

	// Basic validation.
	if err := draft.ValidateDraft(len(eventInputs)); err != nil {
		return nil, nil, err
	}
	stored := make(map[string]*entity.Event, len(storedEvents))
	for _, e := range storedEvents {
		stored[e.ID] = e
	}
	if err := uc.checkGivenEvents(eventInputs, stored); err != nil {
		return nil, nil, err
	}

	// Corrections keep what publish fixed; checked before any write, and
	// before any venue is resolved, so a refused save creates nothing.
	draft.ID = seriesID
	draft.OrganizerID = existing.OrganizerID
	if err := existing.CheckChange(draft, storedEvents); err != nil {
		return nil, nil, err
	}
	if err := checkEveryDateGiven(storedEvents, eventInputs); err != nil {
		return nil, nil, err
	}

	events, err := uc.updatedEvents(ctx, seriesID, eventInputs, stored)
	if err != nil {
		return nil, nil, err
	}

	if err := uc.seriesRepo.Update(ctx, draft, events, performerArtistIDs); err != nil {
		return nil, nil, fmt.Errorf("update series: %w", err)
	}

	series, concerts, err := uc.seriesRepo.GetAuthored(ctx, seriesID)
	if err != nil {
		return nil, nil, fmt.Errorf("get authored after update: %w", err)
	}
	return series, concerts, nil
}

// correctedEvent returns the PUBLISHED or CANCELLED event as the input
// corrects it, or FailedPrecondition when the input changes what publish
// fixed. The venue counts as unchanged when the entered venue name equals the
// event's listed venue name and the place id, when given and when the Venue
// has one, equals the Venue's; a date published before venues were picked
// (no place id) is therefore corrected without one. The venue is never
// resolved, so a refused save creates no Venue.
func (uc *seriesUseCase) correctedEvent(ctx context.Context, current *entity.Event, inp *DraftEventInput) (*entity.Event, error) {
	next := *current
	next.LocalDate = inp.LocalDate
	next.StartTime = inp.StartTime
	next.OpenTime = inp.OpenTime
	if inp.PerAccountLimit != nil {
		next.PerAccountLimit = inp.PerAccountLimit
	}
	if !current.NamesVenue(inp.VenueName, inp.PlaceID) {
		next.VenueID = ""
	}

	onSale := false
	if current.HasPublishState(entity.PublishStatePublished) && !current.HasStartTime(inp.StartTime) {
		sales, err := uc.ticketSaleRepo.ListByEvent(ctx, current.ID)
		if err != nil {
			return nil, fmt.Errorf("list ticket sales of event %s: %w", current.ID, err)
		}
		onSale = len(sales) > 0
	}
	if err := current.CheckChange(&next, onSale); err != nil {
		return nil, err
	}
	// A change that passes keeps the Venue, so the corrected times must exist
	// in its zone.
	if err := checkTimesExist(current.Location(), inp); err != nil {
		return nil, err
	}
	return &next, nil
}

// Publish publishes the named DRAFT events of a series, or all of them.
func (uc *seriesUseCase) Publish(
	ctx context.Context,
	callerOrgID, seriesID string,
	eventIDs []string,
) (*entity.Series, []*entity.Concert, error) {
	series, concerts, err := uc.seriesRepo.GetAuthored(ctx, seriesID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, nil, apperr.New(codes.PermissionDenied, "permission denied")
		}
		return nil, nil, err
	}
	if err := uc.assertOwnsSeries(series, callerOrgID); err != nil {
		return nil, nil, err
	}

	targets, err := entity.EventsToPublish(entity.EventsOf(concerts), eventIDs)
	if err != nil {
		return nil, nil, err
	}

	// Final server-side gate: re-validate the complete required-field set so an
	// incomplete draft (missing performer, venue, or date) can never go live.
	if err := validatePublishReadiness(series, targets, entity.Performers(concerts)); err != nil {
		return nil, nil, err
	}

	targetIDs := make([]string, len(targets))
	for i, e := range targets {
		targetIDs[i] = e.ID
	}
	// An UNLISTED series gets its share token with its first publish and
	// keeps it afterwards, so links already shared keep working. The token
	// travels with Series.PublishEvents: a failed publish stores neither, and
	// publishing again gives the series its token together with its events.
	var token *string
	if series.Visibility != nil && *series.Visibility == entity.SeriesVisibilityUnlisted && series.UnlistedToken == nil {
		t, err := generateUnlistedToken()
		if err != nil {
			return nil, nil, fmt.Errorf("generate unlisted token: %w", err)
		}
		token = &t
	}
	// Series.PublishEvents announces the new concerts of a PUBLIC series.
	if _, err := uc.seriesRepo.PublishEvents(ctx, seriesID, targetIDs, token); err != nil {
		return nil, nil, fmt.Errorf("publish events: %w", err)
	}

	published, publishedConcerts, err := uc.seriesRepo.GetAuthored(ctx, seriesID)
	if err != nil {
		return nil, nil, fmt.Errorf("get authored after publish: %w", err)
	}

	return published, publishedConcerts, nil
}

// Cancel cancels the named events of a series, or all PUBLISHED and DRAFT ones.
func (uc *seriesUseCase) Cancel(ctx context.Context, callerOrgID, seriesID string, eventIDs []string) error {
	series, concerts, err := uc.seriesRepo.GetAuthored(ctx, seriesID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return apperr.New(codes.PermissionDenied, "permission denied")
		}
		return err
	}
	events := entity.EventsOf(concerts)
	if err := uc.assertOwnsSeries(series, callerOrgID); err != nil {
		return err
	}

	targets := eventIDs
	if len(targets) == 0 {
		for _, e := range events {
			if e.HasPublishState(entity.PublishStatePublished) || e.HasPublishState(entity.PublishStateDraft) {
				targets = append(targets, e.ID)
			}
		}
		if len(targets) == 0 {
			return apperr.New(codes.FailedPrecondition, "the concert has no published or draft date to cancel")
		}
	}

	if _, err := uc.seriesRepo.CancelEvents(ctx, seriesID, targets); err != nil {
		return fmt.Errorf("cancel events: %w", err)
	}
	return nil
}

// RegenerateShareURL issues a fresh share token for an UNLISTED series.
func (uc *seriesUseCase) RegenerateShareURL(ctx context.Context, callerOrgID, seriesID string) (string, error) {
	series, err := uc.seriesRepo.Get(ctx, seriesID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return "", apperr.New(codes.PermissionDenied, "permission denied")
		}
		return "", err
	}
	if err := uc.assertOwnsSeries(series, callerOrgID); err != nil {
		return "", err
	}
	if series.Visibility == nil || *series.Visibility != entity.SeriesVisibilityUnlisted {
		return "", apperr.New(codes.FailedPrecondition, "series is not UNLISTED; no share token applies")
	}

	token, err := generateUnlistedToken()
	if err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	if err := uc.seriesRepo.SetUnlistedToken(ctx, seriesID, token); err != nil {
		return "", fmt.Errorf("set unlisted token: %w", err)
	}

	// The share URL is the token itself — the frontend constructs the full URL.
	// Return the token directly; the handler converts to a share URL.
	return token, nil
}

// List returns all series authored by the caller's organizer with their
// Concerts.
func (uc *seriesUseCase) List(ctx context.Context, callerOrgID string) ([]*entity.Series, []*entity.Concert, error) {
	allSeries, err := uc.seriesRepo.ListByOrganizer(ctx, callerOrgID)
	if err != nil {
		return nil, nil, fmt.Errorf("list by organizer: %w", err)
	}

	var concerts []*entity.Concert
	for _, s := range allSeries {
		_, ofSeries, err := uc.seriesRepo.GetAuthored(ctx, s.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("get authored for series %s: %w", s.ID, err)
		}
		concerts = append(concerts, ofSeries...)
	}
	return allSeries, concerts, nil
}

// validateEventInput checks that an event input carries a date, that
// open_time <= start_time when both are set, and, for new and DRAFT events
// (requirePlace, checkDate), that it carries a picked place id and its date has
// not passed everywhere on earth. It runs before any venue is read or created,
// so a rejected save stores nothing.
func (uc *seriesUseCase) validateEventInput(inp *DraftEventInput, requirePlace, checkDate bool) error {
	if requirePlace && (inp.PlaceID == nil || strings.TrimSpace(*inp.PlaceID) == "") {
		return apperr.New(codes.InvalidArgument, "each event must give the place id of a venue picked from the map catalog")
	}
	if inp.LocalDate.IsZero() {
		return apperr.New(codes.InvalidArgument, "each event must have a local date")
	}
	if checkDate {
		// The Venue is not resolved yet, and a held Venue keeps its own zone
		// whatever the input says, so only a date that is past in UTC-12 is
		// refused here. checkTimesAtVenue repeats the check in the resolved
		// Venue's zone.
		if inp.LocalDate.Before(entity.CurrentDateAtVenue(uc.now(), nil)) {
			return apperr.New(codes.InvalidArgument,
				fmt.Sprintf("event date %s is in the past", inp.LocalDate.Format("2006-01-02")),
			)
		}
	}
	if inp.OpenTime != nil && inp.StartTime != nil && inp.StartTime.Before(*inp.OpenTime) {
		return apperr.New(codes.InvalidArgument, "open time must not be after start time")
	}
	return nil
}

// checkTimesAtVenue checks an event input against its Venue's time zone: its
// date is not before the current date at the Venue, and a start or open time
// is a wall-clock time that exists there (the clocks skip one hour when they
// go forward). It fails with InvalidArgument.
func (uc *seriesUseCase) checkTimesAtVenue(venue *entity.Venue, inp *DraftEventInput) error {
	if inp.LocalDate.Before(entity.CurrentDateAtVenue(uc.now(), venue)) {
		return apperr.New(codes.InvalidArgument,
			fmt.Sprintf("event date %s is in the past", inp.LocalDate.Format("2006-01-02")),
		)
	}
	return checkTimesExist(venue.Location(), inp)
}

// checkTimesExist fails with InvalidArgument when a start or open time of inp
// is not a wall-clock time on its date in loc, because the clocks skip it. A
// nil loc is an unknown zone and checks nothing.
func checkTimesExist(loc *time.Location, inp *DraftEventInput) error {
	if loc == nil {
		return nil
	}
	for _, t := range []*entity.TimeOfDay{inp.StartTime, inp.OpenTime} {
		if t == nil {
			continue
		}
		if _, err := entity.LocalInstant(inp.LocalDate, *t, loc); err != nil {
			return err
		}
	}
	return nil
}

// validatePublishReadiness is the final server-side gate before a concert goes
// live. Creation validates a subset for early draft feedback, but an edited or
// partial draft may still be missing later-required data, so publish
// re-validates the whole required set to guarantee no PUBLISHED concert is ever
// surfaced with a missing required field. The required set is: a non-empty
// title, at least one performing artist, and at least one event that each has a
// non-empty venue and a non-zero local date. `description` and the cover image are
// optional and never block publish. Failures are FailedPrecondition (the draft
// is in a not-yet-publishable state), leaving the series DRAFT.
func validatePublishReadiness(series *entity.Series, events []*entity.Event, artists []*entity.Artist) error {
	if strings.TrimSpace(series.Title) == "" {
		return apperr.New(codes.FailedPrecondition, "cannot publish: a title is required")
	}
	if len(artists) == 0 {
		return apperr.New(codes.FailedPrecondition, "cannot publish: at least one performing artist is required")
	}
	if len(events) == 0 {
		return apperr.New(codes.FailedPrecondition, "cannot publish: at least one event is required")
	}
	for _, ev := range events {
		if eventVenueName(ev) == "" {
			return apperr.New(codes.FailedPrecondition, "cannot publish: every event must have a venue")
		}
		if ev.LocalDate.IsZero() {
			return apperr.New(codes.FailedPrecondition, "cannot publish: every event must have a date")
		}
	}
	return nil
}

// eventVenueName resolves an event's display venue name, preferring the resolved
// canonical venue and falling back to the authored listed name. A draft event
// (loaded via LoadDraft) carries only the listed name; a read event carries the
// resolved Venue. Returns "" when neither yields a non-blank name.
func eventVenueName(ev *entity.Event) string {
	if ev.Venue != nil && strings.TrimSpace(ev.Venue.Name) != "" {
		return strings.TrimSpace(ev.Venue.Name)
	}
	if ev.ListedVenueName != nil && strings.TrimSpace(*ev.ListedVenueName) != "" {
		return strings.TrimSpace(*ev.ListedVenueName)
	}
	return ""
}

// SetCoverImage verifies ownership of the series and of the upload, inserts the
// series image row idempotently, and SeriesImage.Create records the
// SERIES_IMAGE.uploaded request with it so the processor can generate variants and cut over series_cover_images. The
// series_cover_images re-point is intentionally deferred to the consumer so the
// existing cover continues to serve until variants are ready (no 404 window).
func (uc *seriesUseCase) SetCoverImage(ctx context.Context, callerOrgID, seriesID, seriesImageID string) error {
	if seriesID == "" {
		return apperr.New(codes.InvalidArgument, "series_id must not be empty")
	}
	if seriesImageID == "" {
		return apperr.New(codes.InvalidArgument, "series_image_id must not be empty")
	}

	// Verify caller owns the series. Non-revealing: NOT_FOUND → PermissionDenied.
	series, err := uc.seriesRepo.Get(ctx, seriesID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return apperr.New(codes.PermissionDenied, "permission denied")
		}
		return err
	}
	if series.OrganizerID == nil || *series.OrganizerID != callerOrgID {
		return apperr.New(codes.PermissionDenied, "permission denied")
	}

	if uc.imageStorer == nil || uc.storageBuckets.Internal == "" {
		return apperr.New(codes.Internal, "image storage is not configured")
	}

	// The upload authorization binds the original's object key to the
	// Organizer the series image id was minted for, so an original under the
	// caller's Organizer proves the caller uploaded it.
	uploaded, err := uc.imageStorer.OriginalExists(ctx, uc.storageBuckets.Internal, callerOrgID, seriesImageID)
	if err != nil {
		return fmt.Errorf("check uploaded original: %w", err)
	}
	if !uploaded {
		// The original is gone once processed, so a recorded series image
		// decides: the caller's own is a repeated attach with nothing left to
		// announce; another Organizer's is not the caller's to attach.
		recorded, err := uc.seriesImageRepo.Get(ctx, seriesImageID)
		if err != nil {
			if errors.Is(err, apperr.ErrNotFound) {
				return apperr.New(codes.FailedPrecondition, "no image was uploaded for this series image id",
					slog.String("series_image_id", seriesImageID))
			}
			return fmt.Errorf("get series image: %w", err)
		}
		if recorded.OrganizerID != callerOrgID {
			return apperr.New(codes.PermissionDenied, "permission denied")
		}
		return nil
	}

	// Insert the series image row idempotently. The organizer_id comes from the
	// verified caller so we never store a series image row for a different Organizer.
	seriesImage := &entity.SeriesImage{
		ID:          seriesImageID,
		OrganizerID: callerOrgID,
		// content_type is not known here (the client declared it to the
		// signed-URL endpoint, not to SetCoverImage). Store an empty attributes
		// map; the processor will not need it and we avoid a round-trip.
		Attributes: map[string]string{},
	}
	// SeriesImage.Create records the request to process the image for the
	// Series together with it; a repeated attach records nothing new.
	if err := uc.seriesImageRepo.Create(ctx, seriesImage, seriesID); err != nil {
		return fmt.Errorf("insert series image row: %w", err)
	}

	uc.logger.Info(ctx, "series image attached to series",
		slog.String("series_image_id", seriesImageID),
		slog.String("series_id", seriesID),
		slog.String("organizer_id", callerOrgID),
	)
	return nil
}
