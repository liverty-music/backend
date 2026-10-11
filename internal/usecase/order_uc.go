package usecase

import (
	"context"
	"errors"

	"github.com/pannpers/go-apperr/apperr"

	"github.com/liverty-music/backend/internal/entity"
)

// OrderUseCase reads Orders for an admin who supports a fan or reconciles a
// payout.
type OrderUseCase interface {
	// GetWithSettlement returns the Order and its Settlement with the
	// Settlement's splits. The Settlement is nil when the Order has none; that
	// is not an error.
	//
	// # Possible errors
	//
	//  - NotFound: no Order has the id.
	//  - Errors of reading the Order or the Settlement, unchanged.
	GetWithSettlement(ctx context.Context, orderID entity.OrderID) (*entity.Order, *entity.Settlement, error)
}

// orderUseCase implements [OrderUseCase].
type orderUseCase struct {
	orderRepo      entity.OrderRepository
	settlementRepo entity.SettlementRepository
}

// Compile-time interface compliance check.
var _ OrderUseCase = (*orderUseCase)(nil)

// NewOrderUseCase constructs an OrderUseCase.
func NewOrderUseCase(orderRepo entity.OrderRepository, settlementRepo entity.SettlementRepository) OrderUseCase {
	return &orderUseCase{orderRepo: orderRepo, settlementRepo: settlementRepo}
}

// GetWithSettlement implements [OrderUseCase].
func (uc *orderUseCase) GetWithSettlement(ctx context.Context, orderID entity.OrderID) (*entity.Order, *entity.Settlement, error) {
	order, err := uc.orderRepo.Get(ctx, orderID)
	if err != nil {
		return nil, nil, err
	}
	settlement, err := uc.settlementRepo.GetByOrderID(ctx, orderID)
	if errors.Is(err, apperr.ErrNotFound) {
		return order, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	return order, settlement, nil
}
