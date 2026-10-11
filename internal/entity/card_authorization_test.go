package entity_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
)

func TestCardAuthorizationPurchase_AcceptsCardBrand(t *testing.T) {
	t.Parallel()

	lotteryEntry := entity.CardAuthorizationPurchase{LotteryEntryID: "entry-1"}
	checkout := entity.CardAuthorizationPurchase{CheckoutID: "checkout-1"}

	t.Run("American Express on a lottery entry", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/card-authorization "American Express on a lottery entry"
		assert.False(t, lotteryEntry.AcceptsCardBrand(entity.CardBrandAmex))
	})

	t.Run("American Express on a checkout", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/card-authorization "American Express on a checkout"
		assert.True(t, checkout.AcceptsCardBrand(entity.CardBrandAmex))
	})

	t.Run("other or unknown brand", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/card-authorization "Other or unknown brand"
		for _, brand := range []entity.CardBrand{"visa", "mastercard", "jcb", "diners", "discover", ""} {
			assert.True(t, lotteryEntry.AcceptsCardBrand(brand), brand)
			assert.True(t, checkout.AcceptsCardBrand(brand), brand)
		}
	})
}

func TestCardAuthorization_Confirm(t *testing.T) {
	t.Parallel()

	lotteryEntry := entity.CardAuthorizationPurchase{LotteryEntryID: "entry-1"}
	checkout := entity.CardAuthorizationPurchase{CheckoutID: "checkout-1"}
	authorized := func(amount int64, brand entity.CardBrand) *entity.CardAuthorization {
		return &entity.CardAuthorization{ID: "pi_1", Amount: amount, CardBrand: brand, Status: entity.CardAuthorizationStatusAuthorized}
	}

	tests := []struct {
		name          string
		authorization *entity.CardAuthorization
		purchase      entity.CardAuthorizationPurchase
		expected      int64
		wantErr       error
	}{
		{
			// @spec components/entity/card-authorization "Matching card authorization"
			name:          "matching card authorization",
			authorization: authorized(16000, "visa"),
			purchase:      lotteryEntry,
			expected:      16000,
		},
		{
			// @spec components/entity/card-authorization "Card authentication not completed"
			name:          "card authentication not completed",
			authorization: &entity.CardAuthorization{ID: "pi_1", Amount: 16000, Status: entity.CardAuthorizationStatusPending},
			purchase:      lotteryEntry,
			expected:      16000,
			wantErr:       apperr.ErrFailedPrecondition,
		},
		{
			// @spec components/entity/card-authorization "Already captured or released"
			name:          "already captured",
			authorization: &entity.CardAuthorization{ID: "pi_1", Amount: 6000, Status: entity.CardAuthorizationStatusCaptured},
			purchase:      checkout,
			expected:      6000,
			wantErr:       apperr.ErrFailedPrecondition,
		},
		{
			// @spec components/entity/card-authorization "Already captured or released"
			name:          "already released",
			authorization: &entity.CardAuthorization{ID: "pi_1", Amount: 6000, Status: entity.CardAuthorizationStatusReleased},
			purchase:      checkout,
			expected:      6000,
			wantErr:       apperr.ErrFailedPrecondition,
		},
		{
			// @spec components/entity/card-authorization "Amount differs"
			name:          "amount differs",
			authorization: authorized(3000, "visa"),
			purchase:      checkout,
			expected:      6000,
			wantErr:       apperr.ErrInvalidArgument,
		},
		{
			// @spec components/entity/card-authorization "Card not accepted"
			name:          "card not accepted",
			authorization: authorized(16000, entity.CardBrandAmex),
			purchase:      lotteryEntry,
			expected:      16000,
			wantErr:       apperr.ErrFailedPrecondition,
		},
		{
			name:          "American Express confirms a checkout",
			authorization: authorized(6000, entity.CardBrandAmex),
			purchase:      checkout,
			expected:      6000,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.authorization.Confirm(tt.purchase, tt.expected)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}
