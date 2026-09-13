package usecase_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
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
// OnboardingUseCase — GetOrCreateOnboarding
// ─────────────────────────────────────────────────────────────────────────────

// stubOrganizerRepoForOnboarding is a local stub; we cannot reuse the one from
// organizer_uc_test.go because that file is in the same package and may not
// compile with mockery issues.
type stubOrganizerRepoForOnboarding struct {
	getFn func(ctx context.Context, id string) (*entity.Organizer, error)
}

func (s *stubOrganizerRepoForOnboarding) Get(ctx context.Context, id string) (*entity.Organizer, error) {
	if s.getFn != nil {
		return s.getFn(ctx, id)
	}
	return &entity.Organizer{ID: id, Status: entity.OrganizerStatusActive}, nil
}

// Satisfy the full OrganizerRepository interface with no-op stubs.
func (s *stubOrganizerRepoForOnboarding) Create(ctx context.Context, o *entity.Organizer) (*entity.Organizer, error) {
	return o, nil
}
func (s *stubOrganizerRepoForOnboarding) GetByZitadelOrgID(ctx context.Context, zitadelOrgID string) (*entity.Organizer, error) {
	return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
}
func (s *stubOrganizerRepoForOnboarding) List(ctx context.Context) ([]*entity.Organizer, error) {
	return nil, nil
}
func (s *stubOrganizerRepoForOnboarding) ListByStatus(ctx context.Context, status entity.OrganizerStatus) ([]*entity.Organizer, error) {
	return nil, nil
}
func (s *stubOrganizerRepoForOnboarding) SetZitadelOrgID(ctx context.Context, id, zitadelOrgID string) error {
	return nil
}
func (s *stubOrganizerRepoForOnboarding) SetStatus(ctx context.Context, id string, status entity.OrganizerStatus) error {
	return nil
}
func (s *stubOrganizerRepoForOnboarding) CompareAndSetStatus(ctx context.Context, id string, from, to entity.OrganizerStatus) (bool, error) {
	return true, nil
}
func (s *stubOrganizerRepoForOnboarding) AssociateArtist(ctx context.Context, organizerID, artistID string) error {
	return nil
}
func (s *stubOrganizerRepoForOnboarding) DisassociateArtist(ctx context.Context, organizerID, artistID string) error {
	return nil
}
func (s *stubOrganizerRepoForOnboarding) ListArtists(ctx context.Context, organizerID string) ([]*entity.Artist, error) {
	return nil, nil
}
func (s *stubOrganizerRepoForOnboarding) FreeArtists(ctx context.Context, organizerID string) error {
	return nil
}
func (s *stubOrganizerRepoForOnboarding) IsArtistRepresentedByActiveOrganizer(ctx context.Context, artistID string) (bool, error) {
	return false, nil
}

func newOnboardingUC(t *testing.T,
	acctRepo entity.OrganizerConnectedAccountRepository,
	port usecase.PaymentSettlementPort,
) usecase.OnboardingUseCase {
	t.Helper()
	return usecase.NewOnboardingUseCase(
		acctRepo,
		&stubOrganizerRepoForOnboarding{},
		port,
		"https://console.example.com/payout",
		newTestLogger(t),
	)
}

func TestOnboardingUseCase_GetOrCreateOnboarding(t *testing.T) {
	t.Parallel()

	const (
		orgID   = "org-onboard"
		acctRef = "acct_onboard"
	)

	tests := []struct {
		name string
		args struct{ organizerID string }
		dep  struct {
			accountExists  bool
			accountStatus  entity.PayoutOnboardingStatus
			providerStatus entity.PayoutOnboardingStatus
		}
		wantStatus        entity.PayoutOnboardingStatus
		wantOnboardingURL bool // true → non-empty URL expected
		wantErr           bool
	}{
		{
			name: "return active account without onboarding URL when account already active",
			args: struct{ organizerID string }{organizerID: orgID},
			dep: struct {
				accountExists  bool
				accountStatus  entity.PayoutOnboardingStatus
				providerStatus entity.PayoutOnboardingStatus
			}{
				accountExists:  true,
				accountStatus:  entity.PayoutOnboardingStatusActive,
				providerStatus: entity.PayoutOnboardingStatusActive,
			},
			wantStatus:        entity.PayoutOnboardingStatusActive,
			wantOnboardingURL: false,
		},
		{
			name: "return pending account with onboarding URL when KYC not complete",
			args: struct{ organizerID string }{organizerID: orgID},
			dep: struct {
				accountExists  bool
				accountStatus  entity.PayoutOnboardingStatus
				providerStatus entity.PayoutOnboardingStatus
			}{
				accountExists:  true,
				accountStatus:  entity.PayoutOnboardingStatusPending,
				providerStatus: entity.PayoutOnboardingStatusPending,
			},
			wantStatus:        entity.PayoutOnboardingStatusPending,
			wantOnboardingURL: true,
		},
		{
			name: "return restricted account with onboarding URL when capability disabled",
			args: struct{ organizerID string }{organizerID: orgID},
			dep: struct {
				accountExists  bool
				accountStatus  entity.PayoutOnboardingStatus
				providerStatus entity.PayoutOnboardingStatus
			}{
				accountExists:  true,
				accountStatus:  entity.PayoutOnboardingStatusRestricted,
				providerStatus: entity.PayoutOnboardingStatusRestricted,
			},
			wantStatus:        entity.PayoutOnboardingStatusRestricted,
			wantOnboardingURL: true,
		},
		{
			name: "provision new account and return pending status when no account exists",
			args: struct{ organizerID string }{organizerID: orgID},
			dep: struct {
				accountExists  bool
				accountStatus  entity.PayoutOnboardingStatus
				providerStatus entity.PayoutOnboardingStatus
			}{
				accountExists:  false,
				accountStatus:  entity.PayoutOnboardingStatusUnspecified,
				providerStatus: entity.PayoutOnboardingStatusUnspecified,
			},
			wantStatus:        entity.PayoutOnboardingStatusPending,
			wantOnboardingURL: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			acctRepo := &stubConnectedAccountRepo{
				getByOrganizerIDFn: func(_ context.Context, _ string) (*entity.OrganizerConnectedAccount, error) {
					if !tc.dep.accountExists {
						return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
					}
					return &entity.OrganizerConnectedAccount{
						OrganizerID: orgID,
						AccountRef:  acctRef,
						Status:      tc.dep.accountStatus,
					}, nil
				},
			}

			port := &stubPaymentSettlementPort{
				getAccountStatusFn: func(_ context.Context, _ string) (entity.PayoutOnboardingStatus, error) {
					return tc.dep.providerStatus, nil
				},
				createConnectedAccountFn: func(_ context.Context, _ string) (string, error) {
					return acctRef, nil
				},
				createOnboardingLinkFn: func(_ context.Context, _, _ string) (string, error) {
					return "https://connect.stripe.com/onboarding/test", nil
				},
			}

			uc := newOnboardingUC(t, acctRepo, port)
			acct, onboardingURL, err := uc.GetOrCreateOnboarding(ctx, tc.args.organizerID)

			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, acct)
			assert.Equal(t, tc.wantStatus, acct.Status)
			if tc.wantOnboardingURL {
				assert.NotEmpty(t, onboardingURL, "expected non-empty onboarding URL")
			} else {
				assert.Empty(t, onboardingURL, "expected empty onboarding URL for active account")
			}
		})
	}
}

func TestOnboardingUseCase_GetOrCreateOnboarding_OrganizerNotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	uc := usecase.NewOnboardingUseCase(
		&stubConnectedAccountRepo{},
		&stubOrganizerRepoForOnboarding{
			getFn: func(_ context.Context, _ string) (*entity.Organizer, error) {
				return nil, apperr.New(apperr.ErrNotFound.Code, "organizer not found")
			},
		},
		&stubPaymentSettlementPort{},
		"https://console.example.com/payout",
		newTestLogger(t),
	)

	_, _, err := uc.GetOrCreateOnboarding(ctx, "org-nonexistent")
	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrNotFound)
}
