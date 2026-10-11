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
// carrying the venue name the operator saw and the place id of the map place
// the operator picked alongside the timing fields. The usecase finds or creates
// the Venue holding that place id and produces a DraftEvent before persisting.
type DraftEventInput struct {
	// VenueName is the venue name as the operator saw it for the picked place,
	// kept as the event's listed venue name.
	VenueName string
	// PlaceID is the map catalog place id of the place the operator picked.
	// Required: an event without one is rejected.
	PlaceID *string
	// LocalDate is the calendar date of the performance (midnight UTC).
	LocalDate time.Time
	StartTime *time.Time
	OpenTime  *time.Time
}

// SeriesUseCase defines the organizer-facing authoring surface for
// first-party concerts. All methods are scoped to a single calling organizer;
// authorization (ownership) is enforced before delegating to the repository.
type SeriesUseCase interface {
	// Create authors a new first-party series in the DRAFT state. Each
	// draft event's venue is the Venue holding the event's picked place id, or
	// a new Venue created from the map catalog's place, before the draft is
	// stored. Returns the full authored concert.
	//
	// # Possible errors
	//
	//  - InvalidArgument: A required field is missing (including an event's
	//    place id), the event list is empty, a time is inconsistent (open >
	//    start, date in the past), or the map catalog has no place with an
	//    event's place id.
	//  - PermissionDenied: A performer is not among the caller's represented
	//    artists. Non-revealing.
	//  - Unavailable: The map catalog cannot be reached for a new place.
	Create(ctx context.Context, callerOrgID string, draft *entity.Series, eventInputs []*DraftEventInput, performerArtistIDs []string) (*entity.Series, []*entity.Event, []*entity.Artist, error)

	// Update replaces the draft content of an existing series. Only
	// allowed while the series is DRAFT, where any field may change; PUBLISHED
	// and CANCELLED series are both rejected with FailedPrecondition.
	//
	// # Possible errors
	//
	//  - NotFound: The series does not exist.
	//  - PermissionDenied: The series is not owned by the caller. Non-revealing.
	//  - FailedPrecondition: The series is not DRAFT (CANCELLED is rejected here;
	//    PUBLISHED is rejected by the repository).
	//  - InvalidArgument, Unavailable: As CreateDraft, including the venue
	//    rules for each event's picked place.
	Update(ctx context.Context, callerOrgID, seriesID string, draft *entity.Series, eventInputs []*DraftEventInput, performerArtistIDs []string) (*entity.Series, []*entity.Event, []*entity.Artist, error)

	// Publish transitions a DRAFT series to PUBLISHED. For PUBLIC series this
	// emits exactly ONE CONCERT.created carrying the newly-published event IDs.
	// UNLISTED series publish without emitting. Returns the published concert.
	//
	// # Possible errors
	//
	//  - NotFound: The series does not exist.
	//  - PermissionDenied: The series is not owned by the caller. Non-revealing.
	//  - FailedPrecondition: A draft event slot is suppressed, or is owned by a
	//    different organizer.
	Publish(ctx context.Context, callerOrgID, seriesID string) (*entity.Series, []*entity.Event, []*entity.Artist, error)

	// Cancel marks a series CANCELLED. This is terminal: the series is dropped
	// from every fan surface and cannot be re-published. Emits CONCERT.cancelled.
	//
	// # Possible errors
	//
	//  - NotFound: The series does not exist.
	//  - PermissionDenied: The series is not owned by the caller. Non-revealing.
	//  - FailedPrecondition: The series is already CANCELLED.
	Cancel(ctx context.Context, callerOrgID, seriesID string) error

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
	// ordered newest first. Each returned series carries its events and artists.
	//
	// # Possible errors
	//
	//  - InvalidArgument: The organizerID is empty.
	List(ctx context.Context, callerOrgID string) ([]*entity.Series, []*[]*entity.Event, []*[]*entity.Artist, error)

	// SetCoverImage verifies that the caller owns the target series, inserts the
	// media row idempotently, and publishes MEDIA.uploaded{media_id, series_id}.
	// It does NOT re-point series_cover_images — the media-processor consumer does
	// that after generating variants. Idempotent on re-delivery.
	//
	// # Possible errors
	//
	//  - InvalidArgument: if series_id or media_id is empty.
	//  - PermissionDenied: if the series is not owned by the caller (non-revealing).
	//  - Internal: if the DB insert or event publish fails.
	SetCoverImage(ctx context.Context, callerOrgID, seriesID, seriesImageID string) error
}

// seriesUseCase implements SeriesUseCase.
type seriesUseCase struct {
	seriesRepo      entity.SeriesRepository
	seriesImageRepo entity.SeriesImageRepository
	venueRepo       entity.VenueRepository
	mapCatalog      entity.VenueMapCatalog
	organizerRepo   entity.OrganizerRepository
	publisher       EventPublisher
	logger          *logging.Logger
}

// Compile-time interface check.
var _ SeriesUseCase = (*seriesUseCase)(nil)

// NewSeriesUseCase creates a new SeriesUseCase. All fields
// are required. The Series image upload lives in SeriesImageUseCase.
func NewSeriesUseCase(
	seriesRepo entity.SeriesRepository,
	seriesImageRepo entity.SeriesImageRepository,
	venueRepo entity.VenueRepository,
	mapCatalog entity.VenueMapCatalog,
	organizerRepo entity.OrganizerRepository,
	publisher EventPublisher,
	logger *logging.Logger,
) SeriesUseCase {
	return &seriesUseCase{
		seriesRepo:      seriesRepo,
		seriesImageRepo: seriesImageRepo,
		venueRepo:       venueRepo,
		mapCatalog:      mapCatalog,
		organizerRepo:   organizerRepo,
		publisher:       publisher,
		logger:          logger,
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

// resolveVenueForInput returns the id of the Venue for one event's picked
// place: the Venue holding the place id when one is held, otherwise a new Venue
// created from the map catalog's place (its name, coordinates and admin area).
// A catalog NotFound becomes InvalidArgument (the operator sent a place id the
// catalog does not know); Unavailable passes through.
//
// The new Venue carries no listed venue name: the operator's typed name is
// kept on the event (DraftEvent.ListedVenueName), and a listed name on the
// Venue would let Create's (listed_venue_name, admin_area) dedupe merge this
// place into another same-named Venue in the same admin area.
func (uc *seriesUseCase) resolveVenueForInput(ctx context.Context, inp *DraftEventInput) (string, error) {
	placeID := *inp.PlaceID

	v, err := uc.venueRepo.GetByPlaceID(ctx, placeID)
	if err == nil {
		return v.ID, nil
	}
	if !errors.Is(err, apperr.ErrNotFound) {
		return "", fmt.Errorf("get venue by place id: %w", err)
	}

	place, err := uc.mapCatalog.GetPlace(ctx, placeID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return "", apperr.Wrap(err, codes.InvalidArgument, "the picked venue is not a place in the map catalog")
		}
		return "", fmt.Errorf("get place from map catalog: %w", err)
	}

	venueID, err := uc.venueRepo.Create(ctx, &entity.Venue{
		ID:            entity.NewID(),
		Name:          place.Name,
		AdminArea:     place.AdminArea,
		GooglePlaceID: &placeID,
		Coordinates:   place.Coordinates,
	})
	if err != nil {
		return "", fmt.Errorf("create venue: %w", err)
	}
	return venueID, nil
}

// resolveDraftEvents converts DraftEventInputs into persisted-ready DraftEvents
// by resolving each venue and assigning new IDs.
func (uc *seriesUseCase) resolveDraftEvents(ctx context.Context, seriesID string, inputs []*DraftEventInput) ([]*entity.DraftEvent, error) {
	out := make([]*entity.DraftEvent, 0, len(inputs))
	for _, inp := range inputs {
		venueID, err := uc.resolveVenueForInput(ctx, inp)
		if err != nil {
			return nil, fmt.Errorf("resolve venue for event on %s: %w", inp.LocalDate.Format("2006-01-02"), err)
		}
		lvn := inp.VenueName
		de := &entity.DraftEvent{
			ID:              entity.NewID(),
			SeriesID:        seriesID,
			VenueID:         venueID,
			ListedVenueName: &lvn,
			LocalDate:       inp.LocalDate,
			StartTime:       inp.StartTime,
			OpenTime:        inp.OpenTime,
		}
		out = append(out, de)
	}
	return out, nil
}

// Create authors a new first-party series in the DRAFT state.
func (uc *seriesUseCase) Create(
	ctx context.Context,
	callerOrgID string,
	draft *entity.Series,
	eventInputs []*DraftEventInput,
	performerArtistIDs []string,
) (*entity.Series, []*entity.Event, []*entity.Artist, error) {
	if draft.Title == "" {
		return nil, nil, nil, apperr.New(codes.InvalidArgument, "title is required")
	}
	if len(eventInputs) == 0 {
		return nil, nil, nil, apperr.New(codes.InvalidArgument, "at least one event is required")
	}

	// Performer ownership check.
	if err := uc.checkPerformerOwnership(ctx, callerOrgID, performerArtistIDs); err != nil {
		return nil, nil, nil, err
	}

	// Validate event timing.
	if err := validateDraftEventInputs(eventInputs); err != nil {
		return nil, nil, nil, err
	}

	// Set authoring fields.
	draft.ID = entity.NewID()
	orgID := callerOrgID
	draft.OrganizerID = &orgID
	ps := entity.SeriesPublishStateDraft
	draft.PublishState = &ps

	// Resolve venues and build DraftEvents.
	draftEvents, err := uc.resolveDraftEvents(ctx, draft.ID, eventInputs)
	if err != nil {
		return nil, nil, nil, err
	}

	if err := uc.seriesRepo.CreateDraft(ctx, draft, draftEvents, performerArtistIDs); err != nil {
		return nil, nil, nil, fmt.Errorf("create draft: %w", err)
	}

	series, events, artists, err := uc.seriesRepo.GetAuthored(ctx, draft.ID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get authored after create: %w", err)
	}
	return series, events, artists, nil
}

// Update replaces the draft content of an existing series.
func (uc *seriesUseCase) Update(
	ctx context.Context,
	callerOrgID, seriesID string,
	draft *entity.Series,
	eventInputs []*DraftEventInput,
	performerArtistIDs []string,
) (*entity.Series, []*entity.Event, []*entity.Artist, error) {
	existing, err := uc.seriesRepo.Get(ctx, seriesID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, nil, nil, apperr.New(codes.PermissionDenied, "permission denied")
		}
		return nil, nil, nil, err
	}
	if err := uc.assertOwnsSeries(existing, callerOrgID); err != nil {
		return nil, nil, nil, err
	}
	if existing.PublishState != nil && *existing.PublishState == entity.SeriesPublishStateCancelled {
		return nil, nil, nil, apperr.New(codes.FailedPrecondition, "series is cancelled and cannot be updated")
	}

	// Performer ownership check.
	if err := uc.checkPerformerOwnership(ctx, callerOrgID, performerArtistIDs); err != nil {
		return nil, nil, nil, err
	}

	// Basic validation.
	if draft.Title == "" {
		return nil, nil, nil, apperr.New(codes.InvalidArgument, "title is required")
	}
	if len(eventInputs) == 0 {
		return nil, nil, nil, apperr.New(codes.InvalidArgument, "at least one event is required")
	}
	if err := validateDraftEventInputs(eventInputs); err != nil {
		return nil, nil, nil, err
	}

	// Preserve the series ID and organizer fields.
	draft.ID = seriesID
	draft.OrganizerID = existing.OrganizerID

	// Resolve venues and build DraftEvents.
	draftEvents, err := uc.resolveDraftEvents(ctx, seriesID, eventInputs)
	if err != nil {
		return nil, nil, nil, err
	}

	if err := uc.seriesRepo.UpdateDraft(ctx, draft, draftEvents, performerArtistIDs); err != nil {
		return nil, nil, nil, fmt.Errorf("update draft: %w", err)
	}

	series, events, artists, err := uc.seriesRepo.GetAuthored(ctx, seriesID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get authored after update: %w", err)
	}
	return series, events, artists, nil
}

// Publish transitions a DRAFT series to PUBLISHED.
func (uc *seriesUseCase) Publish(
	ctx context.Context,
	callerOrgID, seriesID string,
) (*entity.Series, []*entity.Event, []*entity.Artist, error) {
	// Load the current draft (series + events + performers) up front: this both
	// resolves ownership/existence and feeds the final publish gate below.
	series, draftEvents, draftArtists, err := uc.seriesRepo.GetAuthored(ctx, seriesID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, nil, nil, apperr.New(codes.PermissionDenied, "permission denied")
		}
		return nil, nil, nil, err
	}
	if err := uc.assertOwnsSeries(series, callerOrgID); err != nil {
		return nil, nil, nil, err
	}

	// Final server-side gate: re-validate the complete required-field set so an
	// incomplete draft (missing performer, venue, or date) can never go live,
	// regardless of how it reached this state (author, edit, or partial data).
	if err := validatePublishReadiness(series, draftEvents, draftArtists); err != nil {
		return nil, nil, nil, err
	}

	now := time.Now().UTC()
	newEventIDs, err := uc.seriesRepo.PublishDraft(ctx, seriesID, now)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("publish draft: %w", err)
	}

	// For UNLISTED series, generate and store the share token.
	if series.Visibility != nil && *series.Visibility == entity.SeriesVisibilityUnlisted {
		token, err := generateUnlistedToken()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("generate unlisted token: %w", err)
		}
		if err := uc.seriesRepo.SetUnlistedToken(ctx, seriesID, token); err != nil {
			return nil, nil, nil, fmt.Errorf("set unlisted token: %w", err)
		}
	}

	published, events, artists, err := uc.seriesRepo.GetAuthored(ctx, seriesID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("get authored after publish: %w", err)
	}

	// Emit CONCERT.created ONLY for PUBLIC series with newly inserted events.
	// The notification consumer (NotifyNewConcerts) is keyed on a single
	// performing artist, so emit one event per performer carrying the same new
	// event ids — every event features every series-level performer, so each
	// passes the consumer's membership check. Followers of every performer are
	// notified, and a single-performer series (the common case) emits exactly
	// once. This mirrors the per-artist emission of the discovery pipeline.
	if series.Visibility != nil && *series.Visibility == entity.SeriesVisibilityPublic && len(newEventIDs) > 0 {
		for _, artist := range artists {
			created := entity.ConcertCreatedData{
				ArtistID:   artist.ID,
				ConcertIDs: newEventIDs,
			}
			if err := uc.publisher.PublishEvent(ctx, entity.SubjectConcertCreated, created); err != nil {
				uc.logger.Error(ctx, "failed to publish CONCERT.created after organizer publish", err,
					slog.String("series_id", seriesID),
					slog.String("artist_id", artist.ID),
					slog.Int("new_event_count", len(newEventIDs)),
				)
				// Non-fatal: events are persisted; notification retries next cycle.
			}
		}
	}

	// Always emit the organizer.concert.published analytics event.
	if err := uc.publisher.PublishEvent(ctx, entity.SubjectOrganizerConcertPublished, entity.OrganizerConcertPublishedData{
		OrganizerID:   callerOrgID,
		SeriesID:      seriesID,
		NewEventCount: len(newEventIDs),
	}); err != nil {
		uc.logger.Warn(ctx, "failed to publish ORGANIZER.concert_published event",
			slog.String("series_id", seriesID),
			slog.Any("error", err),
		)
		// Non-fatal: the series is already published.
	}

	return published, events, artists, nil
}

// Cancel marks a series CANCELLED and emits CONCERT.cancelled.
func (uc *seriesUseCase) Cancel(ctx context.Context, callerOrgID, seriesID string) error {
	series, err := uc.seriesRepo.Get(ctx, seriesID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return apperr.New(codes.PermissionDenied, "permission denied")
		}
		return err
	}
	if err := uc.assertOwnsSeries(series, callerOrgID); err != nil {
		return err
	}
	if series.PublishState != nil && *series.PublishState == entity.SeriesPublishStateCancelled {
		return apperr.New(codes.FailedPrecondition, "series is already cancelled")
	}

	now := time.Now().UTC()
	if err := uc.seriesRepo.MarkCancelled(ctx, seriesID, now); err != nil {
		return fmt.Errorf("mark cancelled: %w", err)
	}

	// Gather the event IDs for the CONCERT.cancelled payload.
	_, events, _, err := uc.seriesRepo.GetAuthored(ctx, seriesID)
	if err != nil {
		uc.logger.Warn(ctx, "failed to load events after cancel (payload incomplete)", slog.Any("error", err))
	}
	eventIDs := make([]string, 0, len(events))
	for _, ev := range events {
		eventIDs = append(eventIDs, ev.ID)
	}

	if err := uc.publisher.PublishEvent(ctx, entity.SubjectConcertCancelled, entity.ConcertCancelledData{
		SeriesID:   seriesID,
		ConcertIDs: eventIDs,
	}); err != nil {
		uc.logger.Error(ctx, "failed to publish CONCERT.cancelled event", err,
			slog.String("series_id", seriesID),
		)
		// Non-fatal: the series is already cancelled in the DB.
	}

	// Cancel keeps the series row (guard-hidden) and its series_cover_images, so the DB
	// and object storage stay consistent. SeriesImage is reclaimed only when the series
	// is hard-deleted (series_cover_images cascades; a future sweep GCs the objects).
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

// List returns all series authored by the caller's organizer.
func (uc *seriesUseCase) List(
	ctx context.Context,
	callerOrgID string,
) ([]*entity.Series, []*[]*entity.Event, []*[]*entity.Artist, error) {
	allSeries, err := uc.seriesRepo.ListByOrganizer(ctx, callerOrgID)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("list by organizer: %w", err)
	}

	allEvents := make([]*[]*entity.Event, len(allSeries))
	allArtists := make([]*[]*entity.Artist, len(allSeries))
	for i, s := range allSeries {
		_, events, artists, err := uc.seriesRepo.GetAuthored(ctx, s.ID)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("get authored for series %s: %w", s.ID, err)
		}
		ev := events
		ar := artists
		allEvents[i] = &ev
		allArtists[i] = &ar
	}
	return allSeries, allEvents, allArtists, nil
}

// validateDraftEventInputs checks that each event input carries a picked
// place id and a date, that open_time <= start_time when both are set, and
// that the date is not in the past relative to the current UTC day. It runs
// before any venue is read or created, so a rejected draft stores nothing.
func validateDraftEventInputs(inputs []*DraftEventInput) error {
	today := time.Now().UTC().Truncate(24 * time.Hour)
	for _, inp := range inputs {
		if inp.PlaceID == nil || strings.TrimSpace(*inp.PlaceID) == "" {
			return apperr.New(codes.InvalidArgument, "each event must give the place id of a venue picked from the map catalog")
		}
		if inp.LocalDate.IsZero() {
			return apperr.New(codes.InvalidArgument, "each event must have a local date")
		}
		localDateUTC := inp.LocalDate.UTC().Truncate(24 * time.Hour)
		if localDateUTC.Before(today) {
			return apperr.New(codes.InvalidArgument,
				fmt.Sprintf("event date %s is in the past", inp.LocalDate.Format("2006-01-02")),
			)
		}
		if inp.OpenTime != nil && inp.StartTime != nil {
			if inp.OpenTime.After(*inp.StartTime) {
				return apperr.New(codes.InvalidArgument, "open time must not be after start time")
			}
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
// non-empty venue and a non-zero local date. `description` and `media` are
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

// SetCoverImage verifies ownership, inserts the media row idempotently, and
// publishes MEDIA.uploaded so the processor can generate variants and cut over
// series_cover_images. The series_cover_images re-point is intentionally deferred to the
// consumer so the existing cover continues to serve until variants are ready
// (no 404 window).
func (uc *seriesUseCase) SetCoverImage(ctx context.Context, callerOrgID, seriesID, mediaID string) error {
	if seriesID == "" {
		return apperr.New(codes.InvalidArgument, "series_id must not be empty")
	}
	if mediaID == "" {
		return apperr.New(codes.InvalidArgument, "media_id must not be empty")
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

	// Insert the media row idempotently. The organizer_id comes from the
	// verified caller so we never store a media row for a different tenant.
	media := &entity.SeriesImage{
		ID:          mediaID,
		OrganizerID: callerOrgID,
		// content_type is not known here (the client declared it to the
		// signed-URL endpoint, not to SetCoverImage). Store an empty attributes
		// map; the processor will not need it and we avoid a round-trip.
		Attributes: map[string]string{},
	}
	if err := uc.seriesImageRepo.Create(ctx, media); err != nil {
		return fmt.Errorf("insert media row: %w", err)
	}

	// Publish MEDIA.uploaded so the processor can pick it up. Non-fatal on
	// publish failure after a successful DB insert: the row persists and the
	// event can be re-published via a retry or manual trigger.
	if err := uc.publisher.PublishEvent(ctx, entity.SubjectMediaUploaded, entity.MediaUploadedData{
		MediaID:  mediaID,
		SeriesID: seriesID,
	}); err != nil {
		uc.logger.Warn(ctx, "failed to publish MEDIA.uploaded event",
			slog.String("media_id", mediaID),
			slog.String("series_id", seriesID),
			slog.Any("error", err),
		)
		// Non-fatal: the media row is persisted; the processor picks it up on
		// re-delivery. We do NOT return an error so the caller does not retry
		// the entire RPC (which would insert a duplicate-idempotent row again
		// and then try to publish again — a harmless but noisy loop).
	}

	uc.logger.Info(ctx, "media attached to series",
		slog.String("media_id", mediaID),
		slog.String("series_id", seriesID),
		slog.String("organizer_id", callerOrgID),
	)
	return nil
}
