package usecase

import (
	"context"
	"errors"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/entity"
)

// SeatTypeUseCase lets an Organizer define the seat types (席種) of its Series.
type SeatTypeUseCase interface {
	// Create adds a SeatType with a name and a price to one of the
	// Organizer's Series and returns it.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the Series does not exist or is another
	//    Organizer's; it is not revealed which.
	//  - InvalidArgument: the name or price breaks the SeatType rules.
	//  - AlreadyExists: the Series already has a SeatType of the name.
	Create(ctx context.Context, organizerID, seriesID, name string, price int64) (*entity.SeatType, error)

	// Delete removes one of the Organizer's SeatTypes while no TicketStock
	// uses it.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the SeatType does not exist or its Series is
	//    another Organizer's; it is not revealed which.
	//  - FailedPrecondition: a TicketStock uses the SeatType.
	Delete(ctx context.Context, organizerID string, id entity.SeatTypeID) error

	// ListBySeries returns the SeatTypes of one of the Organizer's Series,
	// highest price first; an empty slice when it has none.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the Series does not exist or is another
	//    Organizer's; it is not revealed which.
	ListBySeries(ctx context.Context, organizerID, seriesID string) ([]*entity.SeatType, error)
}

// seatTypeUseCase implements [SeatTypeUseCase].
type seatTypeUseCase struct {
	seatTypeRepo entity.SeatTypeRepository
	seriesRepo   entity.SeriesRepository
}

// Compile-time interface compliance check.
var _ SeatTypeUseCase = (*seatTypeUseCase)(nil)

// NewSeatTypeUseCase constructs a SeatTypeUseCase.
func NewSeatTypeUseCase(seatTypeRepo entity.SeatTypeRepository, seriesRepo entity.SeriesRepository) SeatTypeUseCase {
	return &seatTypeUseCase{seatTypeRepo: seatTypeRepo, seriesRepo: seriesRepo}
}

// Create implements [SeatTypeUseCase].
func (uc *seatTypeUseCase) Create(ctx context.Context, organizerID, seriesID, name string, price int64) (*entity.SeatType, error) {
	if _, err := authoredEvents(ctx, uc.seriesRepo, organizerID, seriesID); err != nil {
		return nil, err
	}
	seatType := entity.NewSeatType(seriesID, name, price)
	if err := seatType.Validate(); err != nil {
		return nil, apperr.Wrap(err, codes.InvalidArgument, "invalid seat type")
	}
	return uc.seatTypeRepo.Create(ctx, seatType)
}

// Delete implements [SeatTypeUseCase].
func (uc *seatTypeUseCase) Delete(ctx context.Context, organizerID string, id entity.SeatTypeID) error {
	seatType, err := uc.seatTypeRepo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return errNotOwnSeries()
		}
		return err
	}
	if _, err := authoredEvents(ctx, uc.seriesRepo, organizerID, seatType.SeriesID); err != nil {
		return err
	}
	return uc.seatTypeRepo.Delete(ctx, id)
}

// ListBySeries implements [SeatTypeUseCase].
func (uc *seatTypeUseCase) ListBySeries(ctx context.Context, organizerID, seriesID string) ([]*entity.SeatType, error) {
	if _, err := authoredEvents(ctx, uc.seriesRepo, organizerID, seriesID); err != nil {
		return nil, err
	}
	return uc.seatTypeRepo.ListBySeries(ctx, seriesID)
}
