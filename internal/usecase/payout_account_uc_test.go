package usecase_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
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
// PayoutAccountUseCase — PayoutAccountUseCase.Get
// ─────────────────────────────────────────────────────────────────────────────

// stubOrganizerRepoForPayoutAccount is a local stub; we cannot reuse the one from
// organizer_uc_test.go because that file is in the same package and may not
// compile with mockery issues.
type stubOrganizerRepoForPayoutAccount struct {
	getFn func(ctx context.Context, id string) (*entity.Organizer, error)
}

func (s *stubOrganizerRepoForPayoutAccount) Get(ctx context.Context, id string) (*entity.Organizer, error) {
	if s.getFn != nil {
		return s.getFn(ctx, id)
	}
	return &entity.Organizer{ID: id, Status: entity.OrganizerStatusActive}, nil
}

// Satisfy the full OrganizerRepository interface with no-op stubs.
func (s *stubOrganizerRepoForPayoutAccount) Create(ctx context.Context, o *entity.Organizer) (*entity.Organizer, error) {
	return o, nil
}
func (s *stubOrganizerRepoForPayoutAccount) GetByProviderOrganizerID(ctx context.Context, providerOrganizerID string) (*entity.Organizer, error) {
	return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
}
func (s *stubOrganizerRepoForPayoutAccount) List(ctx context.Context) ([]*entity.Organizer, error) {
	return nil, nil
}
func (s *stubOrganizerRepoForPayoutAccount) ListByOwnerEmail(ctx context.Context, email string) ([]*entity.Organizer, error) {
	return nil, nil
}
func (s *stubOrganizerRepoForPayoutAccount) ListByStatus(ctx context.Context, status entity.OrganizerStatus) ([]*entity.Organizer, error) {
	return nil, nil
}
func (s *stubOrganizerRepoForPayoutAccount) SetProviderOrganizerID(ctx context.Context, id, providerOrganizerID string) error {
	return nil
}
func (s *stubOrganizerRepoForPayoutAccount) SetStatus(ctx context.Context, id string, status entity.OrganizerStatus) error {
	return nil
}
func (s *stubOrganizerRepoForPayoutAccount) CompareAndSetStatus(ctx context.Context, id string, from, to entity.OrganizerStatus) (bool, error) {
	return true, nil
}
func (s *stubOrganizerRepoForPayoutAccount) AssociateArtist(ctx context.Context, organizerID, artistID string) error {
	return nil
}
func (s *stubOrganizerRepoForPayoutAccount) DisassociateArtist(ctx context.Context, organizerID, artistID string) error {
	return nil
}
func (s *stubOrganizerRepoForPayoutAccount) ListArtists(ctx context.Context, organizerID string) ([]*entity.Artist, error) {
	return nil, nil
}
func (s *stubOrganizerRepoForPayoutAccount) FreeArtists(ctx context.Context, organizerID string) error {
	return nil
}
func (s *stubOrganizerRepoForPayoutAccount) IsArtistRepresentedByActiveOrganizer(ctx context.Context, artistID string) (bool, error) {
	return false, nil
}

func (s *stubOrganizerRepoForPayoutAccount) Delete(_ context.Context, _ string, _ bool) error {
	return nil
}

func (s *stubOrganizerRepoForPayoutAccount) SetBusinessDetails(_ context.Context, _ string, _ entity.BusinessDetails) (*entity.Organizer, error) {
	return nil, nil
}

func (s *stubOrganizerRepoForPayoutAccount) SetPlatformFeeRate(_ context.Context, _ string, _ int) error {
	return nil
}

func newPayoutAccountUC(t *testing.T,
	acctRepo entity.PayoutAccountRepository,
	port paymentProviderStub,
) usecase.PayoutAccountUseCase {
	t.Helper()
	return usecase.NewPayoutAccountUseCase(
		withPayoutAccountProvider(acctRepo, port),
		&stubOrganizerRepoForPayoutAccount{},
		"https://console.example.com/payout",
		newTestLogger(t),
	)
}

func TestPayoutAccountUseCase_Get(t *testing.T) {
	t.Parallel()

	const (
		orgID             = "org-onboard"
		providerAccountID = "acct_onboard"
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
			name: "return pending account with onboarding URL when payout account verification is not complete",
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

			acctRepo := &stubPayoutAccountRepo{
				getByOrganizerIDFn: func(_ context.Context, _ string) (*entity.PayoutAccount, error) {
					if !tc.dep.accountExists {
						return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
					}
					return &entity.PayoutAccount{
						OrganizerID:       orgID,
						ProviderAccountID: providerAccountID,
						Status:            tc.dep.accountStatus,
					}, nil
				},
			}

			port := &stubPaymentProvider{
				getAccountStatusFn: func(_ context.Context, _ string) (entity.PayoutOnboardingStatus, error) {
					return tc.dep.providerStatus, nil
				},
				createPayoutAccountFn: func(_ context.Context, _ string) (string, error) {
					return providerAccountID, nil
				},
				createOnboardingLinkFn: func(_ context.Context, _, _ string) (string, error) {
					return "https://connect.stripe.com/onboarding/test", nil
				},
			}

			uc := newPayoutAccountUC(t, acctRepo, port)
			acct, onboardingURL, err := uc.Get(ctx, tc.args.organizerID)

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

// contactCapturingProvider is a stubPaymentProvider that records the contact
// email a payout account is opened with and can fail the opening.
type contactCapturingProvider struct {
	*stubPaymentProvider
	contacts  []string
	createErr error
}

func (p *contactCapturingProvider) Create(_ context.Context, _ string, contactEmail string) (string, error) {
	p.contacts = append(p.contacts, contactEmail)
	if p.createErr != nil {
		return "", p.createErr
	}
	return "acct_first", nil
}

func TestPayoutAccountUseCase_Get_FirstCall(t *testing.T) {
	t.Parallel()

	const (
		orgID      = "org-first"
		ownerEmail = "band@example.com"
	)
	organizers := &stubOrganizerRepoForPayoutAccount{
		getFn: func(_ context.Context, id string) (*entity.Organizer, error) {
			return &entity.Organizer{ID: id, ShortName: "xx-records", OwnerEmail: ownerEmail, Status: entity.OrganizerStatusActive}, nil
		},
	}

	// @spec components/usecase/payout-account/get "First call"
	t.Run("opens the account with the owner email as contact, stores it pending and returns an onboarding link", func(t *testing.T) {
		t.Parallel()
		var stored []*entity.PayoutAccount
		acctRepo := &stubPayoutAccountRepo{upsertFn: func(_ context.Context, a *entity.PayoutAccount) error {
			stored = append(stored, a)
			return nil
		}}
		provider := &contactCapturingProvider{stubPaymentProvider: &stubPaymentProvider{}}
		uc := usecase.NewPayoutAccountUseCase(withPayoutAccountProvider(acctRepo, provider), organizers,
			"https://console.example.com/payout", newTestLogger(t))

		acct, onboardingURL, err := uc.Get(context.Background(), orgID)

		require.NoError(t, err)
		assert.Equal(t, []string{ownerEmail}, provider.contacts, "the account is opened with the owner email as contact")
		require.Len(t, stored, 1)
		assert.Equal(t, entity.PayoutOnboardingStatusPending, stored[0].Status)
		assert.Equal(t, "acct_first", stored[0].ProviderAccountID)
		assert.Equal(t, entity.PayoutOnboardingStatusPending, acct.Status)
		assert.NotEmpty(t, onboardingURL)
	})

	// @spec components/usecase/payout-account/get "Provider unreachable on first call"
	t.Run("fails with Unavailable and stores nothing when the payment provider cannot be reached", func(t *testing.T) {
		t.Parallel()
		acctRepo := &stubPayoutAccountRepo{upsertFn: func(context.Context, *entity.PayoutAccount) error {
			t.Fatal("no account may be stored")
			return nil
		}}
		provider := &contactCapturingProvider{
			stubPaymentProvider: &stubPaymentProvider{},
			createErr:           apperr.New(codes.Unavailable, "payment provider unreachable"),
		}
		uc := usecase.NewPayoutAccountUseCase(withPayoutAccountProvider(acctRepo, provider), organizers,
			"https://console.example.com/payout", newTestLogger(t))

		_, _, err := uc.Get(context.Background(), orgID)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})
}

// @spec components/usecase/payout-account/get "Unknown Organizer"
func TestPayoutAccountUseCase_Get_OrganizerNotFound(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	uc := usecase.NewPayoutAccountUseCase(
		withPayoutAccountProvider(&stubPayoutAccountRepo{}, &stubPaymentProvider{}),
		&stubOrganizerRepoForPayoutAccount{
			getFn: func(_ context.Context, _ string) (*entity.Organizer, error) {
				return nil, apperr.New(apperr.ErrNotFound.Code, "organizer not found")
			},
		},
		"https://console.example.com/payout",
		newTestLogger(t),
	)

	_, _, err := uc.Get(ctx, "org-nonexistent")
	require.Error(t, err)
	assert.ErrorIs(t, err, apperr.ErrNotFound)
}
