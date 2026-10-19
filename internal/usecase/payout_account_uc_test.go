package usecase_test

import (
	"context"
	"fmt"
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

const testOnboardingReturnURL = "https://console.example.com/"

// recordingProvider is a payment provider stub that records the accounts it
// opens and the onboarding links it creates, and can fail either.
type recordingProvider struct {
	*stubPaymentProvider
	status    entity.PayoutOnboardingStatus
	statusErr error
	createErr error
	linkErr   error

	contacts    []string
	linkedAccts []string
	returnURLs  []string
}

func (p *recordingProvider) Create(_ context.Context, _ string, contactEmail string) (string, error) {
	p.contacts = append(p.contacts, contactEmail)
	if p.createErr != nil {
		return "", p.createErr
	}
	return "acct_first", nil
}

func (p *recordingProvider) GetStatus(_ context.Context, _ string) (entity.PayoutOnboardingStatus, error) {
	if p.statusErr != nil {
		return entity.PayoutOnboardingStatusUnspecified, p.statusErr
	}
	return p.status, nil
}

func (p *recordingProvider) CreateOnboardingLink(_ context.Context, providerAccountID, returnURL string) (string, error) {
	p.linkedAccts = append(p.linkedAccts, providerAccountID)
	p.returnURLs = append(p.returnURLs, returnURL)
	if p.linkErr != nil {
		return "", p.linkErr
	}
	return fmt.Sprintf("https://connect.stripe.com/setup/%d", len(p.linkedAccts)), nil
}

// memoryPayoutAccountRepo stores at most one payout account per Organizer in
// memory and records every Upsert and UpdateStatus.
type memoryPayoutAccountRepo struct {
	stubPayoutAccountRepo
	accounts map[string]entity.PayoutAccount
	upserts  int
	updates  []entity.PayoutOnboardingStatus
}

func newMemoryPayoutAccountRepo(accounts ...entity.PayoutAccount) *memoryPayoutAccountRepo {
	r := &memoryPayoutAccountRepo{accounts: map[string]entity.PayoutAccount{}}
	for _, a := range accounts {
		r.accounts[a.OrganizerID] = a
	}
	return r
}

func (r *memoryPayoutAccountRepo) GetByOrganizerID(_ context.Context, organizerID string) (*entity.PayoutAccount, error) {
	a, ok := r.accounts[organizerID]
	if !ok {
		return nil, apperr.New(codes.NotFound, "payout account not found")
	}
	return &a, nil
}

func (r *memoryPayoutAccountRepo) Upsert(_ context.Context, a *entity.PayoutAccount) error {
	r.upserts++
	r.accounts[a.OrganizerID] = *a
	return nil
}

func (r *memoryPayoutAccountRepo) UpdateStatus(_ context.Context, organizerID string, status entity.PayoutOnboardingStatus) error {
	r.updates = append(r.updates, status)
	a := r.accounts[organizerID]
	a.Status = status
	r.accounts[organizerID] = a
	return nil
}

const (
	payoutTestOrgID      = "org-payout"
	payoutTestOwnerEmail = "band@example.com"
)

func newPayoutAccountUC(t *testing.T, repo entity.PayoutAccountRepository, provider paymentProviderStub) usecase.PayoutAccountUseCase {
	t.Helper()
	organizers := &stubOrganizerRepoForPayoutAccount{
		getFn: func(_ context.Context, id string) (*entity.Organizer, error) {
			if id != payoutTestOrgID {
				return nil, apperr.New(codes.NotFound, "organizer not found")
			}
			return &entity.Organizer{ID: id, OwnerEmail: payoutTestOwnerEmail, Status: entity.OrganizerStatusActive}, nil
		},
	}
	return usecase.NewPayoutAccountUseCase(withPayoutAccountProvider(repo, provider), organizers,
		testOnboardingReturnURL, newTestLogger(t))
}

// storedPayoutAccount is the test Organizer's account at the given provider
// account id and status.
func storedPayoutAccount(providerAccountID string, status entity.PayoutOnboardingStatus) entity.PayoutAccount {
	return entity.PayoutAccount{OrganizerID: payoutTestOrgID, ProviderAccountID: providerAccountID, Status: status}
}

func TestPayoutAccountUseCase_Get(t *testing.T) {
	t.Parallel()

	type want struct {
		status  entity.PayoutOnboardingStatus
		updates []entity.PayoutOnboardingStatus
	}
	tests := []struct {
		name     string
		stored   []entity.PayoutAccount
		provider *recordingProvider
		want     want
		wantErr  error
	}{
		{
			// @spec components/usecase/payout-account/get "Organizer without an account"
			name:     "Organizer without an account: fails with NotFound and opens or stores no account",
			provider: &recordingProvider{stubPaymentProvider: &stubPaymentProvider{}},
			wantErr:  apperr.ErrNotFound,
		},
		{
			// @spec components/usecase/payout-account/get "Organizer with an account"
			name:     "Organizer with an account: returns the pending account and creates no onboarding link",
			stored:   []entity.PayoutAccount{storedPayoutAccount("acct_stored", entity.PayoutOnboardingStatusPending)},
			provider: &recordingProvider{stubPaymentProvider: &stubPaymentProvider{}, status: entity.PayoutOnboardingStatusPending},
			want:     want{status: entity.PayoutOnboardingStatusPending},
		},
		{
			// @spec components/usecase/payout-account/get "Identity check just cleared"
			name:     "Identity check just cleared: stores and returns active",
			stored:   []entity.PayoutAccount{storedPayoutAccount("acct_stored", entity.PayoutOnboardingStatusPending)},
			provider: &recordingProvider{stubPaymentProvider: &stubPaymentProvider{}, status: entity.PayoutOnboardingStatusActive},
			want: want{
				status:  entity.PayoutOnboardingStatusActive,
				updates: []entity.PayoutOnboardingStatus{entity.PayoutOnboardingStatusActive},
			},
		},
		{
			// @spec components/usecase/payout-account/get "Status cannot be read"
			name:   "Status cannot be read: returns the stored status and succeeds",
			stored: []entity.PayoutAccount{storedPayoutAccount("acct_stored", entity.PayoutOnboardingStatusRestricted)},
			provider: &recordingProvider{
				stubPaymentProvider: &stubPaymentProvider{},
				statusErr:           apperr.New(codes.Unavailable, "payment provider unreachable"),
			},
			want: want{status: entity.PayoutOnboardingStatusRestricted},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo := newMemoryPayoutAccountRepo(tt.stored...)
			uc := newPayoutAccountUC(t, repo, tt.provider)

			got, err := uc.Get(context.Background(), payoutTestOrgID)

			assert.Empty(t, tt.provider.contacts, "Get never opens an account")
			assert.Zero(t, repo.upserts, "Get never stores an account")
			assert.Empty(t, tt.provider.linkedAccts, "Get never creates an onboarding link")
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "acct_stored", got.ProviderAccountID)
			assert.Equal(t, tt.want.status, got.Status)
			assert.Equal(t, tt.want.updates, repo.updates)
		})
	}
}

func TestPayoutAccountUseCase_StartOnboarding(t *testing.T) {
	t.Parallel()

	// @spec components/usecase/payout-account/start-onboarding "First start"
	t.Run("First start: opens the account with the owner email, stores it pending and returns a link for it", func(t *testing.T) {
		t.Parallel()
		repo := newMemoryPayoutAccountRepo()
		provider := &recordingProvider{stubPaymentProvider: &stubPaymentProvider{}}
		uc := newPayoutAccountUC(t, repo, provider)

		url, err := uc.StartOnboarding(context.Background(), payoutTestOrgID)

		require.NoError(t, err)
		assert.Equal(t, []string{payoutTestOwnerEmail}, provider.contacts)
		assert.Equal(t, storedPayoutAccount("acct_first", entity.PayoutOnboardingStatusPending), repo.accounts[payoutTestOrgID])
		assert.Equal(t, []string{"acct_first"}, provider.linkedAccts)
		assert.Equal(t, []string{testOnboardingReturnURL}, provider.returnURLs)
		assert.NotEmpty(t, url)
	})

	// @spec components/usecase/payout-account/start-onboarding "Account already open"
	for _, status := range []entity.PayoutOnboardingStatus{entity.PayoutOnboardingStatusPending, entity.PayoutOnboardingStatusRestricted} {
		t.Run(fmt.Sprintf("Account already open (%v): opens no account and returns a link for the stored one", status), func(t *testing.T) {
			t.Parallel()
			repo := newMemoryPayoutAccountRepo(storedPayoutAccount("acct_stored", status))
			provider := &recordingProvider{stubPaymentProvider: &stubPaymentProvider{}}
			uc := newPayoutAccountUC(t, repo, provider)

			url, err := uc.StartOnboarding(context.Background(), payoutTestOrgID)

			require.NoError(t, err)
			assert.Empty(t, provider.contacts, "no account is opened")
			assert.Zero(t, repo.upserts)
			assert.Equal(t, []string{"acct_stored"}, provider.linkedAccts)
			assert.NotEmpty(t, url)
		})
	}

	// @spec components/usecase/payout-account/start-onboarding "Provider unreachable when opening"
	t.Run("Provider unreachable when opening: fails with Unavailable, stores nothing and returns no link", func(t *testing.T) {
		t.Parallel()
		repo := newMemoryPayoutAccountRepo()
		provider := &recordingProvider{
			stubPaymentProvider: &stubPaymentProvider{},
			createErr:           apperr.New(codes.Unavailable, "payment provider unreachable"),
		}
		uc := newPayoutAccountUC(t, repo, provider)

		url, err := uc.StartOnboarding(context.Background(), payoutTestOrgID)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Empty(t, url)
		assert.Empty(t, repo.accounts, "no account is stored")
		assert.Empty(t, provider.linkedAccts, "no link is created")
	})

	// @spec components/usecase/payout-account/start-onboarding "Unknown Organizer"
	t.Run("Unknown Organizer: fails with NotFound and opens no account", func(t *testing.T) {
		t.Parallel()
		repo := newMemoryPayoutAccountRepo()
		provider := &recordingProvider{stubPaymentProvider: &stubPaymentProvider{}}
		uc := newPayoutAccountUC(t, repo, provider)

		_, err := uc.StartOnboarding(context.Background(), "org-nonexistent")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
		assert.Empty(t, provider.contacts, "no account is opened")
		assert.Empty(t, repo.accounts)
	})

	// @spec components/usecase/payout-account/start-onboarding "Two calls"
	t.Run("Two calls: each returns a different link for the same account", func(t *testing.T) {
		t.Parallel()
		repo := newMemoryPayoutAccountRepo()
		provider := &recordingProvider{stubPaymentProvider: &stubPaymentProvider{}}
		uc := newPayoutAccountUC(t, repo, provider)

		first, err := uc.StartOnboarding(context.Background(), payoutTestOrgID)
		require.NoError(t, err)
		second, err := uc.StartOnboarding(context.Background(), payoutTestOrgID)
		require.NoError(t, err)

		assert.NotEqual(t, first, second)
		assert.Equal(t, []string{"acct_first", "acct_first"}, provider.linkedAccts)
		assert.Len(t, provider.contacts, 1, "the account is opened once")
	})

	// @spec components/usecase/payout-account/start-onboarding "Link cannot be created"
	t.Run("Link cannot be created: fails with Unavailable and keeps the opened account stored as pending", func(t *testing.T) {
		t.Parallel()
		repo := newMemoryPayoutAccountRepo()
		provider := &recordingProvider{
			stubPaymentProvider: &stubPaymentProvider{},
			linkErr:             apperr.New(codes.Unavailable, "payment provider unreachable"),
		}
		uc := newPayoutAccountUC(t, repo, provider)

		url, err := uc.StartOnboarding(context.Background(), payoutTestOrgID)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Empty(t, url)
		assert.Equal(t, storedPayoutAccount("acct_first", entity.PayoutOnboardingStatusPending), repo.accounts[payoutTestOrgID])
	})
}
