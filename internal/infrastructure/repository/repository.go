// Package repository composes each entity's repository from its database
// part (package rdb) and its payment provider part (package payment), so a
// usecase sees one interface per entity whatever backs each operation.
package repository

import (
	"context"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
)

// OrderPayments is the payment provider side of the Order's operations.
type OrderPayments interface {
	// CreateRefund is [entity.OrderRepository.CreateRefund].
	CreateRefund(ctx context.Context, params entity.RefundParams) (string, error)
	// GetProviderChargeID is [entity.OrderRepository.GetProviderChargeID].
	GetProviderChargeID(ctx context.Context, providerPaymentID string) (string, error)
}

// OrderRepository is the Order's repository: its rows and its payment.
type OrderRepository struct {
	*rdb.OrderRepository
	OrderPayments
}

var _ entity.OrderRepository = (*OrderRepository)(nil)

// NewOrderRepository composes the Order's repository.
func NewOrderRepository(orders *rdb.OrderRepository, payments OrderPayments) *OrderRepository {
	return &OrderRepository{OrderRepository: orders, OrderPayments: payments}
}

// SettlementPayouts is the payment provider side of the Settlement's
// operations.
type SettlementPayouts interface {
	// CreatePayout is [entity.SettlementRepository.CreatePayout].
	CreatePayout(ctx context.Context, params entity.PayoutParams) (string, error)
	// ReversePayout is [entity.SettlementRepository.ReversePayout].
	ReversePayout(ctx context.Context, params entity.ReversePayoutParams) (string, error)
}

// SettlementRepository is the Settlement's repository: its rows and its
// payouts.
type SettlementRepository struct {
	*rdb.SettlementRepository
	SettlementPayouts
}

var _ entity.SettlementRepository = (*SettlementRepository)(nil)

// NewSettlementRepository composes the Settlement's repository.
func NewSettlementRepository(settlements *rdb.SettlementRepository, payouts SettlementPayouts) *SettlementRepository {
	return &SettlementRepository{SettlementRepository: settlements, SettlementPayouts: payouts}
}

// PayoutAccountProvider is the payment provider side of the PayoutAccount's
// operations.
type PayoutAccountProvider interface {
	// Create is [entity.PayoutAccountRepository.Create].
	Create(ctx context.Context, organizerID string, contactEmail string) (string, error)
	// GetStatus is [entity.PayoutAccountRepository.GetStatus].
	GetStatus(ctx context.Context, providerAccountID string) (entity.PayoutOnboardingStatus, error)
	// CreateOnboardingLink is [entity.PayoutAccountRepository.CreateOnboardingLink].
	CreateOnboardingLink(ctx context.Context, providerAccountID string, returnURL string) (string, error)
}

// PayoutAccountRepository is the PayoutAccount's repository: its row and its
// account at the payment provider.
type PayoutAccountRepository struct {
	*rdb.PayoutAccountRepository
	PayoutAccountProvider
}

var _ entity.PayoutAccountRepository = (*PayoutAccountRepository)(nil)

// NewPayoutAccountRepository composes the PayoutAccount's repository.
func NewPayoutAccountRepository(accounts *rdb.PayoutAccountRepository, provider PayoutAccountProvider) *PayoutAccountRepository {
	return &PayoutAccountRepository{PayoutAccountRepository: accounts, PayoutAccountProvider: provider}
}
