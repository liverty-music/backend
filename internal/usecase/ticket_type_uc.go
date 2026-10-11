package usecase

import (
	"context"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/entity"
)

// UpdateTicketTypeInput is what an Organizer sets on one TicketType.
type UpdateTicketTypeInput struct {
	// TicketTypeID is the TicketType to change.
	TicketTypeID entity.TicketTypeID
	// Price is one ticket's new tax-inclusive price in yen.
	Price int64
	// Quantity is the new number of tickets offered.
	Quantity int
	// PerAccountLimit is the new per-account limit.
	PerAccountLimit int
}

// TicketTypeUseCase lets an Organizer change what one of its sales offers for
// an event.
type TicketTypeUseCase interface {
	// Update changes the price, quantity and per-account limit of one of the
	// Organizer's TicketTypes and returns it with where it stands now. A
	// Lottery TicketType changes only before its sale opens; a FirstCome
	// TicketType keeps its price while tickets are held or sold and its
	// quantity at or above the sold count plus the tickets held.
	//
	// # Possible errors
	//
	//  - NotFound: the TicketType does not exist.
	//  - PermissionDenied: the sale's Series is another Organizer's.
	//  - FailedPrecondition: a Lottery sale has opened, or a FirstCome
	//    TicketType's price lock or quantity floor is broken.
	//  - InvalidArgument: the values break the TicketType rules.
	Update(ctx context.Context, organizerID string, in UpdateTicketTypeInput) (*TicketTypeView, error)
}

// ticketTypeUseCase implements [TicketTypeUseCase].
type ticketTypeUseCase struct {
	saleRepo   entity.TicketSaleRepository
	seriesRepo entity.SeriesRepository
	clock      Clock
}

// Compile-time interface compliance check.
var _ TicketTypeUseCase = (*ticketTypeUseCase)(nil)

// NewTicketTypeUseCase constructs a TicketTypeUseCase.
func NewTicketTypeUseCase(saleRepo entity.TicketSaleRepository, seriesRepo entity.SeriesRepository, clock Clock) TicketTypeUseCase {
	return &ticketTypeUseCase{saleRepo: saleRepo, seriesRepo: seriesRepo, clock: clock}
}

// Update implements [TicketTypeUseCase].
func (uc *ticketTypeUseCase) Update(ctx context.Context, organizerID string, in UpdateTicketTypeInput) (*TicketTypeView, error) {
	now := uc.clock()
	sale, current, err := uc.saleRepo.GetTicketType(ctx, in.TicketTypeID, now)
	if err != nil {
		return nil, err
	}
	if _, err := authoredEvents(ctx, uc.seriesRepo, organizerID, sale.SeriesID); err != nil {
		return nil, err
	}
	if sale.Method == entity.TicketSaleMethodLottery && !now.Before(sale.SaleStartTime) {
		return nil, apperr.New(codes.FailedPrecondition, "a lottery ticket type cannot change once its sale has opened")
	}
	changed := *current
	changed.Price = in.Price
	changed.Quantity = in.Quantity
	changed.PerAccountLimit = in.PerAccountLimit
	updatedSale, updated, err := uc.saleRepo.UpdateTicketType(ctx, &changed, now)
	if err != nil {
		return nil, err
	}
	return ticketTypeViewAt(updatedSale, updated, now), nil
}
