package usecase_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ─────────────────────────────────────────────────────────────────────────────
// PayoutOnboardingStatus mapping — unit tests
// ─────────────────────────────────────────────────────────────────────────────

func TestPayoutOnboardingStatus_IsPayoutEligible(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status entity.PayoutOnboardingStatus
		want   bool
	}{
		{
			name:   "Active status is payout-eligible",
			status: entity.PayoutOnboardingStatusActive,
			want:   true,
		},
		{
			name:   "Pending status is not payout-eligible",
			status: entity.PayoutOnboardingStatusPending,
			want:   false,
		},
		{
			name:   "Restricted status is not payout-eligible",
			status: entity.PayoutOnboardingStatusRestricted,
			want:   false,
		},
		{
			name:   "Unspecified status is not payout-eligible",
			status: entity.PayoutOnboardingStatusUnspecified,
			want:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.status.IsPayoutEligible())
		})
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// PayoutAccountUseCase — PayoutAccountUseCase.Get
// ─────────────────────────────────────────────────────────────────────────────

const testOnboardingReturnURL = "https://console.example.com/"

const (
	payoutTestOrgID      = "org-payout"
	payoutTestOwnerEmail = "band@example.com"
)

// payoutAccountFixture holds the mocks of a PayoutAccountUseCase under test.
// The test Organizer payoutTestOrgID is readable; any other is NotFound.
type payoutAccountFixture struct {
	accounts   *entitymocks.MockPayoutAccountRepository
	organizers *entitymocks.MockOrganizerRepository
	uc         usecase.PayoutAccountUseCase
}

func newPayoutAccountFixture(t *testing.T) *payoutAccountFixture {
	t.Helper()
	f := &payoutAccountFixture{
		accounts:   entitymocks.NewMockPayoutAccountRepository(t),
		organizers: entitymocks.NewMockOrganizerRepository(t),
	}
	f.organizers.EXPECT().Get(mock.Anything, payoutTestOrgID).
		Return(&entity.Organizer{ID: payoutTestOrgID, OwnerEmail: payoutTestOwnerEmail, Status: entity.OrganizerStatusActive}, nil).Maybe()
	f.organizers.EXPECT().Get(mock.Anything, mock.Anything).
		Return(nil, apperr.New(codes.NotFound, "organizer not found")).Maybe()
	f.uc = usecase.NewPayoutAccountUseCase(f.accounts, f.organizers, testOnboardingReturnURL, newTestLogger(t))
	return f
}

// noAccount makes the test Organizer have no payout account on the next read.
func (f *payoutAccountFixture) noAccount() {
	f.accounts.EXPECT().GetByOrganizerID(mock.Anything, payoutTestOrgID).
		Return(nil, apperr.New(codes.NotFound, "payout account not found")).Once()
}

// storedAccount makes the test Organizer's account a on the next read.
func (f *payoutAccountFixture) storedAccount(a entity.PayoutAccount) {
	f.accounts.EXPECT().GetByOrganizerID(mock.Anything, payoutTestOrgID).Return(&a, nil).Once()
}

// opensAccount expects the account to be opened at the provider with the
// owner email as acct_first, and stored as pending.
func (f *payoutAccountFixture) opensAccount() {
	f.accounts.EXPECT().Create(mock.Anything, payoutTestOrgID, payoutTestOwnerEmail, entity.DefaultPayoutAccountTerms()).Return("acct_first", nil).Once()
	f.accounts.EXPECT().Upsert(mock.Anything, new(storedPayoutAccount("acct_first", entity.PayoutOnboardingStatusPending))).Return(nil).Once()
}

// storedPayoutAccount is the test Organizer's account at the given provider
// account id and status.
func storedPayoutAccount(providerAccountID string, status entity.PayoutOnboardingStatus) entity.PayoutAccount {
	return entity.PayoutAccount{OrganizerID: payoutTestOrgID, ProviderAccountID: providerAccountID, Status: status}
}

func TestPayoutAccountUseCase_Get(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// setup sets the stored account and the provider's answers. No case
		// expects Create, Upsert or CreateOnboardingLink: Get never opens or
		// stores an account and never creates an onboarding link.
		setup      func(f *payoutAccountFixture)
		wantStatus entity.PayoutOnboardingStatus
		wantErr    error
	}{
		{
			// @spec components/usecase/payout-account/get "Organizer without an account"
			name:    "Organizer without an account: fails with NotFound and opens or stores no account",
			setup:   func(f *payoutAccountFixture) { f.noAccount() },
			wantErr: apperr.ErrNotFound,
		},
		{
			// @spec components/usecase/payout-account/get "Organizer with an account"
			name: "Organizer with an account: returns the pending account and creates no onboarding link",
			setup: func(f *payoutAccountFixture) {
				f.storedAccount(storedPayoutAccount("acct_stored", entity.PayoutOnboardingStatusPending))
				f.accounts.EXPECT().GetStatus(mock.Anything, "acct_stored").Return(entity.PayoutOnboardingStatusPending, nil).Once()
			},
			wantStatus: entity.PayoutOnboardingStatusPending,
		},
		{
			// @spec components/usecase/payout-account/get "Identity check just cleared"
			name: "Identity check just cleared: stores and returns active",
			setup: func(f *payoutAccountFixture) {
				f.storedAccount(storedPayoutAccount("acct_stored", entity.PayoutOnboardingStatusPending))
				f.accounts.EXPECT().GetStatus(mock.Anything, "acct_stored").Return(entity.PayoutOnboardingStatusActive, nil).Once()
				f.accounts.EXPECT().UpdateStatus(mock.Anything, payoutTestOrgID, entity.PayoutOnboardingStatusActive).Return(nil).Once()
			},
			wantStatus: entity.PayoutOnboardingStatusActive,
		},
		{
			// @spec components/usecase/payout-account/get "Status cannot be read"
			name: "Status cannot be read: returns the stored status and succeeds",
			setup: func(f *payoutAccountFixture) {
				f.storedAccount(storedPayoutAccount("acct_stored", entity.PayoutOnboardingStatusRestricted))
				f.accounts.EXPECT().GetStatus(mock.Anything, "acct_stored").
					Return(entity.PayoutOnboardingStatusUnspecified, apperr.New(codes.Unavailable, "payment provider unreachable")).Once()
			},
			wantStatus: entity.PayoutOnboardingStatusRestricted,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newPayoutAccountFixture(t)
			tt.setup(f)

			got, err := f.uc.Get(context.Background(), payoutTestOrgID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "acct_stored", got.ProviderAccountID)
			assert.Equal(t, tt.wantStatus, got.Status)
		})
	}
}

func TestPayoutAccountUseCase_StartOnboarding(t *testing.T) {
	t.Parallel()

	// @spec components/usecase/payout-account/start-onboarding "First start"
	t.Run("First start: opens the account with the owner email, stores it pending and returns a link for it", func(t *testing.T) {
		t.Parallel()
		f := newPayoutAccountFixture(t)
		f.noAccount()
		f.opensAccount()
		f.accounts.EXPECT().CreateOnboardingLink(mock.Anything, "acct_first", testOnboardingReturnURL).
			Return("https://connect.stripe.com/setup/1", nil).Once()

		url, err := f.uc.StartOnboarding(context.Background(), payoutTestOrgID)

		require.NoError(t, err)
		assert.NotEmpty(t, url)
	})

	// @spec components/usecase/payout-account/start-onboarding "Account already open"
	for _, status := range []entity.PayoutOnboardingStatus{entity.PayoutOnboardingStatusPending, entity.PayoutOnboardingStatusRestricted} {
		t.Run(fmt.Sprintf("Account already open (%v): opens no account and returns a link for the stored one", status), func(t *testing.T) {
			t.Parallel()
			f := newPayoutAccountFixture(t)
			f.storedAccount(storedPayoutAccount("acct_stored", status))
			// No Create or Upsert is expected: no account is opened.
			f.accounts.EXPECT().CreateOnboardingLink(mock.Anything, "acct_stored", testOnboardingReturnURL).
				Return("https://connect.stripe.com/setup/1", nil).Once()

			url, err := f.uc.StartOnboarding(context.Background(), payoutTestOrgID)

			require.NoError(t, err)
			assert.NotEmpty(t, url)
		})
	}

	// @spec components/usecase/payout-account/start-onboarding "Provider unreachable when opening"
	t.Run("Provider unreachable when opening: fails with Unavailable, stores nothing and returns no link", func(t *testing.T) {
		t.Parallel()
		f := newPayoutAccountFixture(t)
		f.noAccount()
		// No Upsert or CreateOnboardingLink is expected: nothing is stored and
		// no link is created.
		f.accounts.EXPECT().Create(mock.Anything, payoutTestOrgID, payoutTestOwnerEmail, entity.DefaultPayoutAccountTerms()).
			Return("", apperr.New(codes.Unavailable, "payment provider unreachable")).Once()

		url, err := f.uc.StartOnboarding(context.Background(), payoutTestOrgID)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Empty(t, url)
	})

	// @spec components/usecase/payout-account/start-onboarding "Unknown Organizer"
	t.Run("Unknown Organizer: fails with NotFound and opens no account", func(t *testing.T) {
		t.Parallel()
		f := newPayoutAccountFixture(t)
		// No payout account call is expected: no account is opened or stored.

		_, err := f.uc.StartOnboarding(context.Background(), "org-nonexistent")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	// @spec components/usecase/payout-account/start-onboarding "Two calls"
	t.Run("Two calls: each returns a different link for the same account", func(t *testing.T) {
		t.Parallel()
		f := newPayoutAccountFixture(t)
		// The first call opens the account, once; the second reads it back.
		f.noAccount()
		f.opensAccount()
		f.accounts.EXPECT().CreateOnboardingLink(mock.Anything, "acct_first", testOnboardingReturnURL).
			Return("https://connect.stripe.com/setup/1", nil).Once()
		f.storedAccount(storedPayoutAccount("acct_first", entity.PayoutOnboardingStatusPending))
		f.accounts.EXPECT().CreateOnboardingLink(mock.Anything, "acct_first", testOnboardingReturnURL).
			Return("https://connect.stripe.com/setup/2", nil).Once()

		first, err := f.uc.StartOnboarding(context.Background(), payoutTestOrgID)
		require.NoError(t, err)
		second, err := f.uc.StartOnboarding(context.Background(), payoutTestOrgID)
		require.NoError(t, err)

		assert.NotEqual(t, first, second)
	})

	// @spec components/usecase/payout-account/start-onboarding "Link cannot be created"
	t.Run("Link cannot be created: fails with Unavailable and keeps the opened account stored as pending", func(t *testing.T) {
		t.Parallel()
		f := newPayoutAccountFixture(t)
		f.noAccount()
		// The opened account is stored as pending before the link fails.
		f.opensAccount()
		f.accounts.EXPECT().CreateOnboardingLink(mock.Anything, "acct_first", testOnboardingReturnURL).
			Return("", apperr.New(codes.Unavailable, "payment provider unreachable")).Once()

		url, err := f.uc.StartOnboarding(context.Background(), payoutTestOrgID)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Empty(t, url)
	})
}
