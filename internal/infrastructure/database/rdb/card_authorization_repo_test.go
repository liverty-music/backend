package rdb_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/liverty-music/backend/internal/infrastructure/payment"
)

// retriedCards is a payment provider whose Create keeps returning the card
// authorization of its first call, as a retried Checkout's Create does.
type retriedCards struct {
	*fakeCards
	first *entity.CardAuthorization
}

func (c *retriedCards) Create(ctx context.Context, purchase entity.CardAuthorizationPurchase, amountJPY int64) (*entity.CardAuthorization, error) {
	if c.first == nil {
		first, err := c.fakeCards.Create(ctx, purchase, amountJPY)
		if err != nil {
			return nil, err
		}
		c.first = first
	}
	copied := *c.first
	return &copied, nil
}

func TestCardAuthorizationRepository(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	ctx := context.Background()
	entry := entity.CardAuthorizationPurchase{
		Purpose:       entity.CardAuthorizationPurposeLotteryEntry,
		UserID:        entity.UserID(entity.NewID()),
		TicketSaleID:  entity.TicketSaleID(entity.NewID()),
		TicketStockID: entity.TicketStockID(entity.NewID()),
		TicketCount:   2,
	}

	t.Run("opened with an id of its own", func(t *testing.T) {
		cards := newFakeCards(db)
		repo := rdb.NewCardAuthorizationRepository(db, cards)

		opened, err := repo.Create(ctx, entry, 10000)
		require.NoError(t, err)

		parsed, err := uuid.Parse(opened.ID)
		require.NoError(t, err, "the id is a UUID")
		assert.Equal(t, uuid.Version(7), parsed.Version())
		assert.True(t, strings.HasPrefix(opened.ProviderID, fakeProviderPrefix))
		got, err := repo.Get(ctx, opened.ID)
		require.NoError(t, err)
		assert.Equal(t, opened.ID, got.ID)
		assert.Equal(t, int64(10000), got.Amount)
	})

	t.Run("a retried checkout keeps its record", func(t *testing.T) {
		repo := rdb.NewCardAuthorizationRepository(db, &retriedCards{fakeCards: newFakeCards(db)})
		checkout := entity.CardAuthorizationPurchase{Purpose: entity.CardAuthorizationPurposeCheckout, CheckoutID: entity.CheckoutID(entity.NewID())}

		first, err := repo.Create(ctx, checkout, 6000)
		require.NoError(t, err)
		again, err := repo.Create(ctx, checkout, 6000)
		require.NoError(t, err)

		assert.Equal(t, first.ID, again.ID)
		assert.Equal(t, first.ProviderID, again.ProviderID)
	})

	t.Run("notice for a known card authorization", func(t *testing.T) {
		// @spec components/entity/card-authorization/get-by-provider-card-authorization-id "Notice for a known card authorization"
		cards := newFakeCards(db)
		repo := rdb.NewCardAuthorizationRepository(db, cards)
		opened, err := repo.Create(ctx, entry, 10000)
		require.NoError(t, err)
		cards.authenticate(t, opened.ID)

		got, err := repo.GetByProviderCardAuthorizationID(ctx, opened.ProviderID)

		require.NoError(t, err)
		assert.Equal(t, opened.ID, got.ID, "its own id")
		assert.Equal(t, entity.CardAuthorizationStatusAuthorized, got.Status)
		assert.Equal(t, entry, got.Purchase)
		require.NotNil(t, got.ExpireTime, "with its expiry time")
		assert.True(t, got.ExpireTime.Equal(fakeExpireTime))
	})

	t.Run("unknown provider ID", func(t *testing.T) {
		// @spec components/entity/card-authorization/get-by-provider-card-authorization-id "Unknown provider ID"
		repo := rdb.NewCardAuthorizationRepository(db, newFakeCards(db))

		_, err := repo.GetByProviderCardAuthorizationID(ctx, "pi_unknown_"+entity.NewID())

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("payment provider unavailable", func(t *testing.T) {
		// @spec components/entity/card-authorization/get-by-provider-card-authorization-id "Payment provider unavailable"
		id := newCardAuthorizationID(db)
		logger, err := logging.New()
		require.NoError(t, err)
		repo := rdb.NewCardAuthorizationRepository(db, payment.NewNoopCardAuthorizationProvider(logger))

		_, err = repo.GetByProviderCardAuthorizationID(ctx, "pi_"+id)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})

	t.Run("an id that is not a card authorization's", func(t *testing.T) {
		repo := rdb.NewCardAuthorizationRepository(db, newFakeCards(db))

		_, err := repo.Get(ctx, "pi_not_an_id")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
