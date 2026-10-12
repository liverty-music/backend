package usecase_test

import (
	"context"

	"github.com/liverty-music/backend/internal/entity"
)

// paymentProviderStub is the payment provider side of the Order,
// Settlement and PayoutAccount repositories, as a test stub implements it.
type paymentProviderStub interface {
	GetProviderChargeID(ctx context.Context, providerPaymentID string) (string, error)
	CreateRefund(ctx context.Context, params entity.RefundParams) (string, error)
	CreatePayout(ctx context.Context, params entity.PayoutParams) (string, error)
	ReversePayout(ctx context.Context, params entity.ReversePayoutParams) (string, error)
	Create(ctx context.Context, organizerID string, contactEmail string) (string, error)
	GetStatus(ctx context.Context, providerAccountID string) (entity.PayoutOnboardingStatus, error)
	CreateOnboardingLink(ctx context.Context, providerAccountID string, returnURL string) (string, error)
}

// orderRepoWithPayments answers the Order's payment operations from p and
// the rest from the stub, as the infrastructure composite does.
type orderRepoWithPayments struct {
	entity.OrderRepository
	p paymentProviderStub
}

func withOrderPayments(o entity.OrderRepository, p paymentProviderStub) entity.OrderRepository {
	return orderRepoWithPayments{OrderRepository: o, p: p}
}

func (o orderRepoWithPayments) CreateRefund(ctx context.Context, params entity.RefundParams) (string, error) {
	return o.p.CreateRefund(ctx, params)
}

func (o orderRepoWithPayments) GetProviderChargeID(ctx context.Context, providerPaymentID string) (string, error) {
	return o.p.GetProviderChargeID(ctx, providerPaymentID)
}

// settlementRepoWithPayouts answers the Settlement's payouts from p.
type settlementRepoWithPayouts struct {
	entity.SettlementRepository
	p paymentProviderStub
}

func withSettlementPayouts(s entity.SettlementRepository, p paymentProviderStub) entity.SettlementRepository {
	return settlementRepoWithPayouts{SettlementRepository: s, p: p}
}

func (s settlementRepoWithPayouts) CreatePayout(ctx context.Context, params entity.PayoutParams) (string, error) {
	return s.p.CreatePayout(ctx, params)
}

func (s settlementRepoWithPayouts) ReversePayout(ctx context.Context, params entity.ReversePayoutParams) (string, error) {
	return s.p.ReversePayout(ctx, params)
}

// payoutAccountRepoWithProvider answers the PayoutAccount's provider
// operations from p.
type payoutAccountRepoWithProvider struct {
	entity.PayoutAccountRepository
	p paymentProviderStub
}

func withPayoutAccountProvider(a entity.PayoutAccountRepository, p paymentProviderStub) entity.PayoutAccountRepository {
	return payoutAccountRepoWithProvider{PayoutAccountRepository: a, p: p}
}

func (a payoutAccountRepoWithProvider) Create(ctx context.Context, organizerID string, contactEmail string) (string, error) {
	return a.p.Create(ctx, organizerID, contactEmail)
}

func (a payoutAccountRepoWithProvider) GetStatus(ctx context.Context, providerAccountID string) (entity.PayoutOnboardingStatus, error) {
	return a.p.GetStatus(ctx, providerAccountID)
}

func (a payoutAccountRepoWithProvider) CreateOnboardingLink(ctx context.Context, providerAccountID string, returnURL string) (string, error) {
	return a.p.CreateOnboardingLink(ctx, providerAccountID, returnURL)
}
