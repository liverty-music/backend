package usecase

import (
	"context"
	"errors"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/entity"
)

// TicketStockUseCase lets an Organizer stock its events with tickets of its
// Series' seat types and see how many are sold and held.
type TicketStockUseCase interface {
	// Create stocks one of the Organizer's events with quantity tickets of a
	// SeatType of the event's Series and returns the TicketStock.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the event does not exist or its Series is another
	//    Organizer's; it is not revealed which.
	//  - InvalidArgument: the SeatType is not one of the Series' SeatTypes,
	//    or the quantity breaks the TicketStock rules.
	//  - FailedPrecondition: the event is CANCELLED.
	//  - AlreadyExists: the event already has a TicketStock of the SeatType.
	Create(ctx context.Context, organizerID, eventID string, seatTypeID entity.SeatTypeID, quantity int) (*entity.TicketStock, error)

	// Delete removes one of the Organizer's TicketStocks while no Order other
	// than a Void one names it.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the stock does not exist or its event's Series is
	//    another Organizer's; it is not revealed which.
	//  - FailedPrecondition: an Order that is not Void names the stock, or a
	//    Void Order's card authorization is not yet released.
	Delete(ctx context.Context, organizerID string, id entity.TicketStockID) error

	// ListByEvent returns the TicketStocks of one of the Organizer's events,
	// each with its SeatType, its quantity, and its sold count, held count and
	// state now; an empty slice when the event has none.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the event does not exist or is another
	//    Organizer's; it is not revealed which.
	ListByEvent(ctx context.Context, organizerID, eventID string) ([]*entity.TicketStock, error)
}

// ticketStockUseCase implements [TicketStockUseCase].
type ticketStockUseCase struct {
	stockRepo      entity.TicketStockRepository
	seatTypeRepo   entity.SeatTypeRepository
	eventRepo      entity.EventRepository
	seriesRepo     entity.SeriesRepository
	eventOrganizer EventOrganizerRepository
	clock          Clock
}

// Compile-time interface compliance check.
var _ TicketStockUseCase = (*ticketStockUseCase)(nil)

// NewTicketStockUseCase constructs a TicketStockUseCase.
func NewTicketStockUseCase(
	stockRepo entity.TicketStockRepository,
	seatTypeRepo entity.SeatTypeRepository,
	eventRepo entity.EventRepository,
	seriesRepo entity.SeriesRepository,
	eventOrganizer EventOrganizerRepository,
	clock Clock,
) TicketStockUseCase {
	return &ticketStockUseCase{
		stockRepo:      stockRepo,
		seatTypeRepo:   seatTypeRepo,
		eventRepo:      eventRepo,
		seriesRepo:     seriesRepo,
		eventOrganizer: eventOrganizer,
		clock:          clock,
	}
}

// errNotOwnEvent is the PermissionDenied returned for an event that does not
// exist or is another Organizer's, without revealing which.
func errNotOwnEvent() error {
	return apperr.New(codes.PermissionDenied, "the event is not the organizer's")
}

// ownEvent reads the event and fails with PermissionDenied, without revealing
// whether it exists, unless its Series is the Organizer's.
func (uc *ticketStockUseCase) ownEvent(ctx context.Context, organizerID, eventID string) (*entity.Event, error) {
	event, err := uc.eventRepo.Get(ctx, eventID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, errNotOwnEvent()
		}
		return nil, err
	}
	if _, err := authoredEvents(ctx, uc.seriesRepo, organizerID, event.SeriesID); err != nil {
		return nil, err
	}
	return event, nil
}

// Create implements [TicketStockUseCase].
func (uc *ticketStockUseCase) Create(ctx context.Context, organizerID, eventID string, seatTypeID entity.SeatTypeID, quantity int) (*entity.TicketStock, error) {
	event, err := uc.ownEvent(ctx, organizerID, eventID)
	if err != nil {
		return nil, err
	}
	seatTypes, err := uc.seatTypeRepo.ListBySeries(ctx, event.SeriesID)
	if err != nil {
		return nil, err
	}
	if !containsSeatType(seatTypes, seatTypeID) {
		return nil, apperr.New(codes.InvalidArgument, "the seat type is not one of the series' seat types")
	}
	stock := entity.NewTicketStock(eventID, seatTypeID, quantity)
	if err := stock.Validate(); err != nil {
		return nil, apperr.Wrap(err, codes.InvalidArgument, "invalid ticket stock")
	}
	if event.HasPublishState(entity.PublishStateCancelled) {
		return nil, apperr.New(codes.FailedPrecondition, "a cancelled event cannot be stocked")
	}
	return uc.stockRepo.Create(ctx, stock)
}

// containsSeatType reports whether id is among seatTypes.
func containsSeatType(seatTypes []*entity.SeatType, id entity.SeatTypeID) bool {
	for _, s := range seatTypes {
		if s.ID == id {
			return true
		}
	}
	return false
}

// Delete implements [TicketStockUseCase].
func (uc *ticketStockUseCase) Delete(ctx context.Context, organizerID string, id entity.TicketStockID) error {
	stock, err := uc.stockRepo.Get(ctx, id, uc.clock())
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return errNotOwnEvent()
		}
		return err
	}
	if _, err := uc.ownEvent(ctx, organizerID, stock.EventID); err != nil {
		return err
	}
	return uc.stockRepo.Delete(ctx, id)
}

// ListByEvent implements [TicketStockUseCase].
func (uc *ticketStockUseCase) ListByEvent(ctx context.Context, organizerID, eventID string) ([]*entity.TicketStock, error) {
	owner, err := uc.eventOrganizer.GetOrganizerID(ctx, eventID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, errNotOwnEvent()
		}
		return nil, err
	}
	if owner != organizerID {
		return nil, errNotOwnEvent()
	}
	return uc.stockRepo.ListByEvent(ctx, eventID, uc.clock())
}
