package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/entity"
)

// TicketSaleView is a TicketSale with where it stands at the time it was read.
type TicketSaleView struct {
	// Sale is the sale, with its sold and held counts.
	Sale *entity.TicketSale
	// State is the sale's state at the time of the read.
	State entity.TicketSaleState
	// LowStock reports whether the sale is OnSale with at most a tenth of its
	// quantity left.
	LowStock bool
	// SellerDetails are the event's Organizer's 特商法 seller details, set by
	// Get for the checkout's final confirmation; nil from GetOwn.
	SellerDetails *entity.SellerDetails
}

// ConfigureTicketSaleInput is what an Organizer sets on its event's sale.
type ConfigureTicketSaleInput struct {
	EventID   string
	SaleStart time.Time
	// SaleEnd is optional; the event's start time when nil.
	SaleEnd  *time.Time
	Price    int64
	Quantity int
	// PerAccountLimit is optional; 4 for a new sale, or the sale's current
	// limit for a change, when nil.
	PerAccountLimit *int
}

// TicketSaleUseCase puts an Organizer's published events on sale first come,
// first served, and shows the sales to fans and Organizers.
type TicketSaleUseCase interface {
	// Configure creates the event's TicketSale, or changes it. The event must
	// be the Organizer's, published and timed, and the Organizer must have
	// complete seller details.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the event does not exist or is another Organizer's.
	//  - FailedPrecondition: the event is not published or has no start time,
	//    the Organizer has no complete seller details, the price changes after
	//    a checkout started, or the quantity is below sold and held.
	//  - InvalidArgument: the sale end is after the event's start time, or the
	//    sale breaks the TicketSale rules.
	Configure(ctx context.Context, organizerID string, in ConfigureTicketSaleInput) (*entity.TicketSale, error)

	// Get returns an event's sale for anyone, with its state now, whether stock
	// is low, and the event's Organizer's seller details for the checkout's
	// 特商法 final confirmation. The view's counts are not for display.
	//
	// # Possible errors
	//
	//  - NotFound: the event has no TicketSale, or the event is not published.
	Get(ctx context.Context, eventID string) (*TicketSaleView, error)

	// GetOwn returns the sale of one of the Organizer's events with its counts.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the event does not exist or is another Organizer's.
	//  - NotFound: the event has no TicketSale.
	GetOwn(ctx context.Context, organizerID, eventID string) (*TicketSaleView, error)
}

// ticketSaleUseCase implements [TicketSaleUseCase].
type ticketSaleUseCase struct {
	saleRepo      entity.TicketSaleRepository
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
	organizerRepo entity.OrganizerRepository,
	eventState EventPublishStatePort,
	eventStart EventStartTimeRepository,
	clock Clock,
) TicketSaleUseCase {
	return &ticketSaleUseCase{
		saleRepo:      saleRepo,
		organizerRepo: organizerRepo,
		eventState:    eventState,
		eventStart:    eventStart,
		clock:         clock,
	}
}

// checkOwner fails with PermissionDenied, without revealing whether the event
// exists, unless the event is the Organizer's.
func (uc *ticketSaleUseCase) checkOwner(ctx context.Context, organizerID, eventID string) error {
	owner, err := uc.eventState.GetEventOrganizerID(ctx, eventID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return apperr.New(codes.PermissionDenied, "the event is not the organizer's")
		}
		return err
	}
	if owner != organizerID {
		return apperr.New(codes.PermissionDenied, "the event is not the organizer's")
	}
	return nil
}

// Configure implements [TicketSaleUseCase].
func (uc *ticketSaleUseCase) Configure(ctx context.Context, organizerID string, in ConfigureTicketSaleInput) (*entity.TicketSale, error) {
	if err := uc.checkOwner(ctx, organizerID, in.EventID); err != nil {
		return nil, err
	}
	published, err := uc.eventState.IsEventPublished(ctx, in.EventID)
	if err != nil {
		return nil, err
	}
	if !published {
		return nil, apperr.New(codes.FailedPrecondition, "the event is not published")
	}
	eventStart, err := uc.eventStart.GetEventStartTime(ctx, in.EventID)
	if err != nil {
		return nil, err
	}
	if eventStart == nil {
		return nil, apperr.New(codes.FailedPrecondition, "the event has no start time")
	}
	organizer, err := uc.organizerRepo.Get(ctx, organizerID)
	if err != nil {
		return nil, err
	}
	if !organizer.HasCompleteSellerDetails() {
		return nil, apperr.New(codes.FailedPrecondition, "the organizer has no complete seller details")
	}

	saleEnd := *eventStart
	if in.SaleEnd != nil {
		saleEnd = *in.SaleEnd
	}
	if saleEnd.After(*eventStart) {
		return nil, apperr.New(codes.InvalidArgument, "the sale must end no later than the event's start time")
	}

	now := uc.clock()
	current, err := uc.saleRepo.GetByEvent(ctx, in.EventID, now)
	if err != nil && !errors.Is(err, apperr.ErrNotFound) {
		return nil, err
	}
	if current == nil {
		limit := 0
		if in.PerAccountLimit != nil {
			limit = *in.PerAccountLimit
		}
		return uc.saleRepo.Create(ctx, entity.NewTicketSale(in.EventID, in.SaleStart, saleEnd, in.Price, in.Quantity, limit))
	}

	changed := *current
	changed.SaleStartTime = in.SaleStart
	changed.SaleEndTime = saleEnd
	changed.Price = in.Price
	changed.Quantity = in.Quantity
	if in.PerAccountLimit != nil {
		changed.PerAccountLimit = *in.PerAccountLimit
	}
	return uc.saleRepo.Update(ctx, &changed, now)
}

// Get implements [TicketSaleUseCase].
func (uc *ticketSaleUseCase) Get(ctx context.Context, eventID string) (*TicketSaleView, error) {
	now := uc.clock()
	sale, err := uc.saleRepo.GetByEvent(ctx, eventID, now)
	if err != nil {
		return nil, err
	}
	published, err := uc.eventState.IsEventPublished(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if !published {
		return nil, apperr.New(codes.NotFound, "the event's sale is not shown")
	}
	organizerID, err := uc.eventState.GetEventOrganizerID(ctx, eventID)
	if err != nil {
		return nil, err
	}
	organizer, err := uc.organizerRepo.Get(ctx, organizerID)
	if err != nil {
		return nil, err
	}
	return &TicketSaleView{
		Sale:          sale,
		State:         sale.StateAt(now),
		LowStock:      sale.IsLowStockAt(now),
		SellerDetails: organizer.SellerDetails,
	}, nil
}

// GetOwn implements [TicketSaleUseCase].
func (uc *ticketSaleUseCase) GetOwn(ctx context.Context, organizerID, eventID string) (*TicketSaleView, error) {
	if err := uc.checkOwner(ctx, organizerID, eventID); err != nil {
		return nil, err
	}
	now := uc.clock()
	sale, err := uc.saleRepo.GetByEvent(ctx, eventID, now)
	if err != nil {
		return nil, err
	}
	return &TicketSaleView{Sale: sale, State: sale.StateAt(now), LowStock: sale.IsLowStockAt(now)}, nil
}
