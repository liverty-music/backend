package entity_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/liverty-music/backend/internal/entity"
)

func TestPayoutAccountTerms(t *testing.T) {
	t.Parallel()

	terms := entity.DefaultPayoutAccountTerms()

	assert.Equal(t, "JP", terms.CountryCode)
	assert.Equal(t, "JPY", terms.CurrencyCode)
	assert.Equal(t, "ja-JP", terms.Locale)
	assert.Equal(t, "7922", terms.MerchantCategoryCode, "theatrical producers and ticket agencies: the transfers capability does not activate without an MCC")
	assert.False(t, terms.ProviderDashboard, "the platform manages the account")
	assert.True(t, terms.PlatformCollectsFees)
	assert.True(t, terms.PlatformAbsorbsLosses, "transfer reversals need the platform to absorb losses")
}

func TestNewPayoutOnboardingStatus(t *testing.T) {
	t.Parallel()

	for capability, want := range map[entity.TransfersCapability]entity.PayoutOnboardingStatus{
		entity.TransfersCapabilityActive:      entity.PayoutOnboardingStatusActive,
		entity.TransfersCapabilityInactive:    entity.PayoutOnboardingStatusRestricted,
		entity.TransfersCapabilityPending:     entity.PayoutOnboardingStatusPending,
		entity.TransfersCapabilityUnrequested: entity.PayoutOnboardingStatusPending,
	} {
		assert.Equal(t, want, entity.NewPayoutOnboardingStatus(capability), "capability %d", capability)
	}
}
