package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/entity"
)

// TicketTypeView is a TicketType with where it stands at the time it was
// read.
type TicketTypeView struct {
	// TicketType is the ticket type, with its sold and held counts for a
	// FirstCome sale.
	TicketType *entity.TicketType
	// State is the TicketType's state at the time of the read. FirstCome
	// only; Unspecified for a Lottery sale.
	State entity.TicketTypeState
	// LowStock reports whether the TicketType is OnSale with at most a tenth
	// of its quantity left. FirstCome only.
	LowStock bool
	// Stats is the tally of the TicketType's entries. Set only for a Lottery
	// sale read by its Organizer ([TicketSaleUseCase.ListOwnByEvent]).
	Stats *entity.LotteryEntryStats
}

// TicketSaleView is a TicketSale with its TicketTypes as the reader sees them.
type TicketSaleView struct {
	// Sale is the sale. Its TicketTypes are the ones shown, in the same order
	// as TicketTypes.
	Sale *entity.TicketSale
	// TicketTypes are the shown TicketTypes with where they stand.
	TicketTypes []*TicketTypeView
}

// TicketTypeInput is what one TicketType of a new sale offers for one event.
type TicketTypeInput struct {
	// EventID is the event the TicketType admits to; an event of the sale's
	// Series.
	EventID string
	// Price is one ticket's tax-inclusive price in yen.
	Price int64
	// Quantity is the number of tickets offered.
	Quantity int
	// PerAccountLimit is optional; 0 means none was given, and the limit is
	// [entity.TicketTypeDefaultPerAccountLimit].
	PerAccountLimit int
}

// CreateTicketSaleInput is what an Organizer sets on a new sale.
type CreateTicketSaleInput struct {
	// SeriesID is the Series whose events the sale covers.
	SeriesID string
	// Name tells the sale apart from the event's other sales.
	Name string
	// Method is Lottery or FirstCome.
	Method entity.TicketSaleMethod
	// SaleStart is when the window opens.
	SaleStart time.Time
	// SaleEnd is when the window closes. Required for a Lottery sale; for a
	// FirstCome sale nil means the earliest start time of the events it
	// covers.
	SaleEnd *time.Time
	// VerificationRequirement is whether a fan must hold an Active IdentityVerification; the
	// zero value is None.
	VerificationRequirement entity.VerificationRequirement
	// TicketTypes are what the sale offers, one per event; at least one.
	TicketTypes []TicketTypeInput
}

// TicketSaleUseCase lets an Organizer open sales, by lottery or first come,
// on the published events of its Series and change them, and shows the
// sales of an event to fans and to its Organizer.
type TicketSaleUseCase interface {
	// Create stores a new sale and its TicketTypes for the Organizer's Series
	// and returns it, not drawn. Every event must be the Series', published
	// and timed; the sale must end before every event starts (a FirstCome
	// sale may end at the start); the Organizer must have complete business
	// details; and the sale must not overlap another sale of any of its
	// events.
	//
	// # Possible errors
	//
	//  - InvalidArgument: no TicketType, a Lottery sale without an end time,
	//    a TicketType's event outside the Series, or values that break the
	//    TicketSale or TicketType rules.
	//  - PermissionDenied: the Series does not exist or is another
	//    Organizer's.
	//  - FailedPrecondition: an event is not published or has no start time,
	//    the end time is too late for an event, the Organizer has no complete
	//    business details, or the window overlaps another sale of an event.
	Create(ctx context.Context, organizerID string, in CreateTicketSaleInput) (*TicketSaleView, error)

	// ListOwnByEvent returns every sale of one of the Organizer's events, in
	// start order, with all of their TicketTypes: a Lottery TicketType with its
	// entry tallies, a FirstCome TicketType with its counts and state now. It
	// returns an empty slice when the event has no sale.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the event does not exist or is another Organizer's.
	ListOwnByEvent(ctx context.Context, organizerID, eventID string) ([]*TicketSaleView, error)

	// ListByEvent returns, for anyone, the sales of a published event in start
	// order, each with only its TicketType for the event; a FirstCome
	// TicketType with its state and LowStock now. It returns an empty slice
	// when the event has no sale, is not published, or does not exist.
	ListByEvent(ctx context.Context, eventID string) ([]*TicketSaleView, error)

	// SetWindow moves the window of one of the Organizer's sales and returns
	// the updated sale. A Lottery sale's window moves only before it opens.
	//
	// # Possible errors
	//
	//  - NotFound: the sale does not exist.
	//  - PermissionDenied: the sale's Series is another Organizer's.
	//  - InvalidArgument: the window breaks the TicketSale rules.
	//  - FailedPrecondition: a Lottery sale has opened, the end time is too
	//    late for an event, or the window overlaps another sale of an event.
	SetWindow(ctx context.Context, organizerID string, saleID entity.TicketSaleID, saleStart, saleEnd time.Time) (*TicketSaleView, error)

	// SetVerificationRequirement changes whether fans taking part in one of
	// the Organizer's sales must hold an Active IdentityVerification, at any time, and returns
	// the updated sale. Entries already made are not re-checked.
	//
	// # Possible errors
	//
	//  - InvalidArgument: no sale is given.
	//  - NotFound: the sale does not exist.
	//  - PermissionDenied: the sale's Series is another Organizer's.
	SetVerificationRequirement(ctx context.Context, organizerID string, saleID entity.TicketSaleID, req entity.VerificationRequirement) (*TicketSaleView, error)
}

// ticketSaleUseCase implements [TicketSaleUseCase].
type ticketSaleUseCase struct {
	saleRepo      entity.TicketSaleRepository
	entryRepo     entity.LotteryEntryRepository
	seriesRepo    entity.SeriesRepository
	organizerRepo entity.OrganizerRepository
	eventState    EventPublishStatePort
	eventStart    EventStartTimeRepository
	clock         Clock
}

// Compile-time interface compliance check.
var _ TicketSaleUseCase = (*ticketSaleUseCase)(nil)

// NewTicketSaleUseCase constructs a TicketSaleUseCase.
func NewTicketSaleUseCase(
	saleRepo entity.TicketSaleRepository,
	entryRepo entity.LotteryEntryRepository,
	seriesRepo entity.SeriesRepository,
	organizerRepo entity.OrganizerRepository,
	eventState EventPublishStatePort,
	eventStart EventStartTimeRepository,
	clock Clock,
) TicketSaleUseCase {
	return &ticketSaleUseCase{
		saleRepo:      saleRepo,
		entryRepo:     entryRepo,
		seriesRepo:    seriesRepo,
		organizerRepo: organizerRepo,
		eventState:    eventState,
		eventStart:    eventStart,
		clock:         clock,
	}
}

// errNotOwnSeries is returned, without revealing whether the Series exists,
// when it is not the calling Organizer's.
func errNotOwnSeries() error {
	return apperr.New(codes.PermissionDenied, "the series is not the organizer's")
}

// authoredEvents reads the Series with Series.GetAuthored and returns its
// events, failing with PermissionDenied when the Series does not exist or is
// not the Organizer's.
func authoredEvents(ctx context.Context, seriesRepo entity.SeriesRepository, organizerID, seriesID string) ([]*entity.Event, error) {
	series, events, _, err := seriesRepo.GetAuthored(ctx, seriesID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, errNotOwnSeries()
		}
		return nil, err
	}
	if series.OrganizerID == nil || *series.OrganizerID != organizerID {
		return nil, errNotOwnSeries()
	}
	return events, nil
}

// eventIDsOf returns the events the sale's TicketTypes admit to, in order.
func eventIDsOf(sale *entity.TicketSale) []string {
	ids := make([]string, 0, len(sale.TicketTypes))
	for _, tt := range sale.TicketTypes {
		ids = append(ids, tt.EventID)
	}
	return ids
}

// eventStartTimes reads the start time of each event, failing with
// FailedPrecondition when one has none.
func (uc *ticketSaleUseCase) eventStartTimes(ctx context.Context, eventIDs []string) ([]time.Time, error) {
	starts := make([]time.Time, 0, len(eventIDs))
	for _, id := range eventIDs {
		start, err := uc.eventStart.GetEventStartTime(ctx, id)
		if err != nil {
			return nil, err
		}
		if start == nil {
			return nil, apperr.New(codes.FailedPrecondition, "an event of the sale has no start time")
		}
		starts = append(starts, *start)
	}
	return starts, nil
}

// checkEndBeforeEvents fails with FailedPrecondition when a Lottery sale does
// not end before every event start, or a FirstCome sale ends after any.
func checkEndBeforeEvents(method entity.TicketSaleMethod, end time.Time, starts []time.Time) error {
	for _, start := range starts {
		if method == entity.TicketSaleMethodLottery && !end.Before(start) {
			return apperr.New(codes.FailedPrecondition, "a lottery sale must end before every event starts")
		}
		if method == entity.TicketSaleMethodFirstCome && end.After(start) {
			return apperr.New(codes.FailedPrecondition, "a first-come sale must end no later than every event's start")
		}
	}
	return nil
}

// checkNoOverlap fails with FailedPrecondition when sale's window overlaps
// the window of another sale of any of its events. The sale's own stored
// window is not compared.
func (uc *ticketSaleUseCase) checkNoOverlap(ctx context.Context, sale *entity.TicketSale, now time.Time) error {
	for _, eventID := range eventIDsOf(sale) {
		others, err := uc.saleRepo.ListByEvent(ctx, eventID, now)
		if err != nil {
			return err
		}
		for _, other := range others {
			if other.ID != sale.ID && sale.Overlaps(other) {
				return apperr.New(codes.FailedPrecondition, "the window overlaps another sale of an event")
			}
		}
	}
	return nil
}

// Create implements [TicketSaleUseCase].
func (uc *ticketSaleUseCase) Create(ctx context.Context, organizerID string, in CreateTicketSaleInput) (*TicketSaleView, error) {
	if len(in.TicketTypes) == 0 {
		return nil, apperr.New(codes.InvalidArgument, "a sale needs at least one ticket type")
	}
	if in.Method == entity.TicketSaleMethodLottery && in.SaleEnd == nil {
		return nil, apperr.New(codes.InvalidArgument, "a lottery sale needs an end time")
	}
	sale := entity.NewTicketSale(in.SeriesID, in.Name, in.Method, in.SaleStart, time.Time{})
	sale.VerificationRequirement = in.VerificationRequirement
	for _, t := range in.TicketTypes {
		entity.NewTicketType(sale, t.EventID, t.Price, t.Quantity, t.PerAccountLimit)
	}
	// Without an end time the window is checked once the events' start
	// times are read.
	if in.SaleEnd != nil {
		sale.SaleEndTime = *in.SaleEnd
		if err := sale.Validate(); err != nil {
			return nil, apperr.New(codes.InvalidArgument, err.Error())
		}
	}

	events, err := authoredEvents(ctx, uc.seriesRepo, organizerID, in.SeriesID)
	if err != nil {
		return nil, err
	}
	ofSeries := make(map[string]struct{}, len(events))
	for _, e := range events {
		ofSeries[e.ID] = struct{}{}
	}
	eventIDs := eventIDsOf(sale)
	for _, id := range eventIDs {
		if _, ok := ofSeries[id]; !ok {
			return nil, apperr.New(codes.InvalidArgument, "a ticket type's event is not an event of the series")
		}
	}
	for _, id := range eventIDs {
		published, err := uc.eventState.IsEventPublished(ctx, id)
		if err != nil {
			return nil, err
		}
		if !published {
			return nil, apperr.New(codes.FailedPrecondition, "an event of the sale is not published")
		}
	}
	starts, err := uc.eventStartTimes(ctx, eventIDs)
	if err != nil {
		return nil, err
	}
	if in.SaleEnd == nil {
		sale.SaleEndTime = starts[0]
		for _, s := range starts[1:] {
			if s.Before(sale.SaleEndTime) {
				sale.SaleEndTime = s
			}
		}
		if err := sale.Validate(); err != nil {
			return nil, apperr.New(codes.InvalidArgument, err.Error())
		}
	}
	if err := checkEndBeforeEvents(sale.Method, sale.SaleEndTime, starts); err != nil {
		return nil, err
	}

	organizer, err := uc.organizerRepo.Get(ctx, organizerID)
	if err != nil {
		return nil, err
	}
	if !organizer.HasCompleteBusinessDetails() {
		return nil, apperr.New(codes.FailedPrecondition, "the organizer has no complete business details")
	}

	now := uc.clock()
	if err := uc.checkNoOverlap(ctx, sale, now); err != nil {
		return nil, err
	}
	created, err := uc.saleRepo.Create(ctx, sale)
	if err != nil {
		return nil, err
	}
	return saleViewAt(created, created.TicketTypes, now), nil
}

// saleViewAt returns the sale showing ticketTypes, with the state and
// LowStock of each FirstCome TicketType at now.
func saleViewAt(sale *entity.TicketSale, ticketTypes []*entity.TicketType, now time.Time) *TicketSaleView {
	shown := *sale
	shown.TicketTypes = ticketTypes
	view := &TicketSaleView{Sale: &shown, TicketTypes: make([]*TicketTypeView, 0, len(ticketTypes))}
	for _, tt := range ticketTypes {
		view.TicketTypes = append(view.TicketTypes, ticketTypeViewAt(sale, tt, now))
	}
	return view
}

// ticketTypeViewAt returns tt with, for a FirstCome sale, its state and
// LowStock at now.
func ticketTypeViewAt(sale *entity.TicketSale, tt *entity.TicketType, now time.Time) *TicketTypeView {
	view := &TicketTypeView{TicketType: tt}
	if sale.Method == entity.TicketSaleMethodFirstCome {
		view.State = tt.StateAt(sale, now)
		view.LowStock = tt.IsLowStockAt(sale, now)
	}
	return view
}

// ListOwnByEvent implements [TicketSaleUseCase].
func (uc *ticketSaleUseCase) ListOwnByEvent(ctx context.Context, organizerID, eventID string) ([]*TicketSaleView, error) {
	owner, err := uc.eventState.GetEventOrganizerID(ctx, eventID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, apperr.New(codes.PermissionDenied, "the event is not the organizer's")
		}
		return nil, err
	}
	if owner != organizerID {
		return nil, apperr.New(codes.PermissionDenied, "the event is not the organizer's")
	}
	now := uc.clock()
	sales, err := uc.saleRepo.ListByEvent(ctx, eventID, now)
	if err != nil {
		return nil, err
	}
	views := make([]*TicketSaleView, 0, len(sales))
	for _, sale := range sales {
		view := saleViewAt(sale, sale.TicketTypes, now)
		if sale.Method == entity.TicketSaleMethodLottery {
			for _, ttv := range view.TicketTypes {
				stats, err := uc.entryRepo.GetStatsByTicketType(ctx, ttv.TicketType.ID)
				if err != nil {
					return nil, err
				}
				ttv.Stats = &stats
			}
		}
		views = append(views, view)
	}
	return views, nil
}

// ListByEvent implements [TicketSaleUseCase].
func (uc *ticketSaleUseCase) ListByEvent(ctx context.Context, eventID string) ([]*TicketSaleView, error) {
	published, err := uc.eventState.IsEventPublished(ctx, eventID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return []*TicketSaleView{}, nil
		}
		return nil, err
	}
	if !published {
		return []*TicketSaleView{}, nil
	}
	now := uc.clock()
	sales, err := uc.saleRepo.ListByEvent(ctx, eventID, now)
	if err != nil {
		return nil, err
	}
	views := make([]*TicketSaleView, 0, len(sales))
	for _, sale := range sales {
		var forEvent []*entity.TicketType
		for _, tt := range sale.TicketTypes {
			if tt.EventID == eventID {
				forEvent = append(forEvent, tt)
			}
		}
		views = append(views, saleViewAt(sale, forEvent, now))
	}
	return views, nil
}

// ownSale reads the sale and fails with PermissionDenied unless its Series is
// the Organizer's.
func (uc *ticketSaleUseCase) ownSale(ctx context.Context, organizerID string, saleID entity.TicketSaleID, now time.Time) (*entity.TicketSale, error) {
	sale, err := uc.saleRepo.Get(ctx, saleID, now)
	if err != nil {
		return nil, err
	}
	if _, err := authoredEvents(ctx, uc.seriesRepo, organizerID, sale.SeriesID); err != nil {
		return nil, err
	}
	return sale, nil
}

// SetWindow implements [TicketSaleUseCase].
func (uc *ticketSaleUseCase) SetWindow(ctx context.Context, organizerID string, saleID entity.TicketSaleID, saleStart, saleEnd time.Time) (*TicketSaleView, error) {
	now := uc.clock()
	sale, err := uc.ownSale(ctx, organizerID, saleID, now)
	if err != nil {
		return nil, err
	}
	if sale.Method == entity.TicketSaleMethodLottery && !now.Before(sale.SaleStartTime) {
		return nil, apperr.New(codes.FailedPrecondition, "a lottery sale's window cannot change once it has opened")
	}
	if err := sale.ValidateWindow(saleStart, saleEnd); err != nil {
		return nil, apperr.New(codes.InvalidArgument, err.Error())
	}
	starts, err := uc.eventStartTimes(ctx, eventIDsOf(sale))
	if err != nil {
		return nil, err
	}
	if err := checkEndBeforeEvents(sale.Method, saleEnd, starts); err != nil {
		return nil, err
	}
	moved := *sale
	moved.SaleStartTime, moved.SaleEndTime = saleStart, saleEnd
	if err := uc.checkNoOverlap(ctx, &moved, now); err != nil {
		return nil, err
	}
	updated, err := uc.saleRepo.SetWindow(ctx, saleID, saleStart, saleEnd, now)
	if err != nil {
		return nil, err
	}
	return saleViewAt(updated, updated.TicketTypes, now), nil
}

// SetVerificationRequirement implements [TicketSaleUseCase].
func (uc *ticketSaleUseCase) SetVerificationRequirement(ctx context.Context, organizerID string, saleID entity.TicketSaleID, req entity.VerificationRequirement) (*TicketSaleView, error) {
	if saleID == "" {
		return nil, apperr.New(codes.InvalidArgument, "ticket_sale_id is required")
	}
	now := uc.clock()
	if _, err := uc.ownSale(ctx, organizerID, saleID, now); err != nil {
		return nil, err
	}
	updated, err := uc.saleRepo.SetVerificationRequirement(ctx, saleID, req, now)
	if err != nil {
		return nil, err
	}
	return saleViewAt(updated, updated.TicketTypes, now), nil
}
