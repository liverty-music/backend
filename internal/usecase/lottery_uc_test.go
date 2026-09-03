package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- helpers ---

// --- self-contained test doubles ---
//
// mockery cannot currently generate these mocks (v2.53.6 dies with
// `package "image" without types` while loading internal/entity). These
// function-field stubs verify the business logic without that dependency.
// TODO: replace with mockery expecter mocks once mockery is fixed / after BSR gen.

type stubPhaseRepo struct {
	createFn                        func(context.Context, *entity.LotterySalesPhase) (*entity.LotterySalesPhase, error)
	getFn                           func(context.Context, entity.LotteryPhaseID) (*entity.LotterySalesPhase, error)
	listPhasesDueForDrawFn          func(context.Context, time.Time) ([]*entity.LotterySalesPhase, error)
	updateVerificationRequirementFn func(context.Context, entity.LotteryPhaseID, entity.VerificationRequirement) (*entity.LotterySalesPhase, error)
}

func (s *stubPhaseRepo) Create(ctx context.Context, p *entity.LotterySalesPhase) (*entity.LotterySalesPhase, error) {
	if s.createFn != nil {
		return s.createFn(ctx, p)
	}
	return p, nil
}

func (s *stubPhaseRepo) Get(ctx context.Context, id entity.LotteryPhaseID) (*entity.LotterySalesPhase, error) {
	if s.getFn != nil {
		return s.getFn(ctx, id)
	}
	return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
}

func (s *stubPhaseRepo) ListPhasesDueForDraw(ctx context.Context, now time.Time) ([]*entity.LotterySalesPhase, error) {
	if s.listPhasesDueForDrawFn != nil {
		return s.listPhasesDueForDrawFn(ctx, now)
	}
	return nil, nil
}

func (s *stubPhaseRepo) UpdateVerificationRequirement(ctx context.Context, id entity.LotteryPhaseID, req entity.VerificationRequirement) (*entity.LotterySalesPhase, error) {
	if s.updateVerificationRequirementFn != nil {
		return s.updateVerificationRequirementFn(ctx, id, req)
	}
	return &entity.LotterySalesPhase{ID: id, VerificationRequirement: req}, nil
}

type stubAppRepo struct {
	createFn              func(context.Context, *entity.TicketApplication) (*entity.TicketApplication, error)
	getByPairFn           func(context.Context, entity.LotteryPhaseID, entity.UserID) (*entity.TicketApplication, error)
	getFn                 func(context.Context, entity.TicketApplicationID) (*entity.TicketApplication, error)
	updateStateFn         func(context.Context, entity.TicketApplicationID, entity.TicketApplicationState) error
	getPhaseStatsFn       func(context.Context, entity.LotteryPhaseID) (entity.LotteryPhaseStatus, error)
	listAppliedForPhaseFn func(context.Context, entity.LotteryPhaseID) ([]*entity.TicketApplication, error)
	persistDrawOutcomeFn  func(context.Context, entity.LotteryPhaseID, []entity.DrawWinnerRow, []entity.DrawLoserRow) error
}

func (s *stubAppRepo) Create(ctx context.Context, a *entity.TicketApplication) (*entity.TicketApplication, error) {
	if s.createFn != nil {
		return s.createFn(ctx, a)
	}
	return a, nil
}

func (s *stubAppRepo) GetByPhaseAndApplicant(ctx context.Context, phaseID entity.LotteryPhaseID, applicantID entity.UserID) (*entity.TicketApplication, error) {
	if s.getByPairFn != nil {
		return s.getByPairFn(ctx, phaseID, applicantID)
	}
	return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
}

func (s *stubAppRepo) Get(ctx context.Context, id entity.TicketApplicationID) (*entity.TicketApplication, error) {
	if s.getFn != nil {
		return s.getFn(ctx, id)
	}
	return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
}

func (s *stubAppRepo) UpdateState(ctx context.Context, id entity.TicketApplicationID, state entity.TicketApplicationState) error {
	if s.updateStateFn != nil {
		return s.updateStateFn(ctx, id, state)
	}
	return nil
}

func (s *stubAppRepo) GetPhaseStats(ctx context.Context, phaseID entity.LotteryPhaseID) (entity.LotteryPhaseStatus, error) {
	if s.getPhaseStatsFn != nil {
		return s.getPhaseStatsFn(ctx, phaseID)
	}
	return entity.LotteryPhaseStatus{}, nil
}

func (s *stubAppRepo) ListAppliedForPhase(ctx context.Context, phaseID entity.LotteryPhaseID) ([]*entity.TicketApplication, error) {
	if s.listAppliedForPhaseFn != nil {
		return s.listAppliedForPhaseFn(ctx, phaseID)
	}
	return nil, nil
}

func (s *stubAppRepo) PersistDrawOutcome(ctx context.Context, phaseID entity.LotteryPhaseID, winners []entity.DrawWinnerRow, losers []entity.DrawLoserRow) error {
	if s.persistDrawOutcomeFn != nil {
		return s.persistDrawOutcomeFn(ctx, phaseID, winners, losers)
	}
	return nil
}

type stubEventState struct {
	fn func(context.Context, string) (bool, error)
}

func (s *stubEventState) IsEventPublished(ctx context.Context, eventID string) (bool, error) {
	if s.fn != nil {
		return s.fn(ctx, eventID)
	}
	return true, nil
}

// stubVerifiedIdentityRepo stubs [entity.VerifiedIdentityRepository] with
// function fields so each test case can inject targeted behavior.
type stubVerifiedIdentityRepo struct {
	getByUserIDFn           func(ctx context.Context, userID string) (*entity.VerifiedIdentity, error)
	getByPocketSignUserIDFn func(ctx context.Context, pocketSignUserID string) (*entity.VerifiedIdentity, error)
	createFn                func(ctx context.Context, vi *entity.VerifiedIdentity) (*entity.VerifiedIdentity, error)
	updateStatusFn          func(ctx context.Context, id string, status entity.VerificationStatus) error
	deleteFn                func(ctx context.Context, id string) error
}

func (s *stubVerifiedIdentityRepo) GetByUserID(ctx context.Context, userID string) (*entity.VerifiedIdentity, error) {
	if s.getByUserIDFn != nil {
		return s.getByUserIDFn(ctx, userID)
	}
	return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
}

func (s *stubVerifiedIdentityRepo) GetByPocketSignUserID(ctx context.Context, pocketSignUserID string) (*entity.VerifiedIdentity, error) {
	if s.getByPocketSignUserIDFn != nil {
		return s.getByPocketSignUserIDFn(ctx, pocketSignUserID)
	}
	return nil, apperr.New(apperr.ErrNotFound.Code, "not found")
}

func (s *stubVerifiedIdentityRepo) Create(ctx context.Context, vi *entity.VerifiedIdentity) (*entity.VerifiedIdentity, error) {
	if s.createFn != nil {
		return s.createFn(ctx, vi)
	}
	return vi, nil
}

func (s *stubVerifiedIdentityRepo) UpdateStatus(ctx context.Context, id string, status entity.VerificationStatus) error {
	if s.updateStatusFn != nil {
		return s.updateStatusFn(ctx, id, status)
	}
	return nil
}

func (s *stubVerifiedIdentityRepo) Delete(ctx context.Context, id string) error {
	if s.deleteFn != nil {
		return s.deleteFn(ctx, id)
	}
	return nil
}

// stubPaymentPort stubs [usecase.PaymentAuthorizationPort] with function fields
// so each test case can inject targeted behavior without running mockery.
type stubPaymentPort struct {
	createAuthFn  func(ctx context.Context, amountJPY int64) (string, string, error)
	verifyAuthFn  func(ctx context.Context, piRef string, expectedAmountJPY int64) error
	cancelAuthFn  func(ctx context.Context, piRef string) error
	captureAuthFn func(ctx context.Context, piRef string) error
}

func (s *stubPaymentPort) CreateAuthorization(ctx context.Context, amountJPY int64) (string, string, error) {
	if s.createAuthFn != nil {
		return s.createAuthFn(ctx, amountJPY)
	}
	return "pi_test_ref", "cs_test_secret", nil
}

func (s *stubPaymentPort) VerifyAuthorization(ctx context.Context, piRef string, expectedAmountJPY int64) error {
	if s.verifyAuthFn != nil {
		return s.verifyAuthFn(ctx, piRef, expectedAmountJPY)
	}
	return nil
}

func (s *stubPaymentPort) CancelAuthorization(ctx context.Context, piRef string) error {
	if s.cancelAuthFn != nil {
		return s.cancelAuthFn(ctx, piRef)
	}
	return nil
}

func (s *stubPaymentPort) CaptureAuthorization(ctx context.Context, piRef string) error {
	if s.captureAuthFn != nil {
		return s.captureAuthFn(ctx, piRef)
	}
	return nil
}

func fixedClock(t time.Time) usecase.Clock {
	return func() time.Time { return t }
}

// basePhase returns a valid LotterySalesPhase for use in test cases. The
// window is 7 days, capacity 100, max 4, price 5000 JPY.
func basePhase(open, close time.Time) *entity.LotterySalesPhase {
	return &entity.LotterySalesPhase{
		ID:                       "phase-1",
		EventID:                  "event-1",
		OpenTime:                 open,
		CloseTime:                close,
		TicketCapacity:           100,
		MaxTicketsPerApplication: 4,
		TicketPrice:              5000,
	}
}

// newLotteryUC builds a lotteryUseCase with the given repos, and a fixed
// clock set to withinTime. paymentPort, eventState, and verifiedIdentityRepo
// default to passing stubs.
func newLotteryUC(
	t *testing.T,
	phaseRepo entity.LotteryPhaseRepository,
	appRepo entity.TicketApplicationRepository,
	eventState usecase.EventPublishStatePort,
	paymentPort usecase.PaymentAuthorizationPort,
	clockTime time.Time,
	viRepo ...entity.VerifiedIdentityRepository,
) usecase.LotteryUseCase {
	t.Helper()
	var repo entity.VerifiedIdentityRepository
	if len(viRepo) > 0 && viRepo[0] != nil {
		repo = viRepo[0]
	} else {
		repo = &stubVerifiedIdentityRepo{}
	}
	return usecase.NewLotteryUseCase(
		phaseRepo, appRepo, eventState, paymentPort, repo,
		fixedClock(clockTime), newTestLogger(t),
	)
}

// --- ConfigureLotteryPhase ---

func TestLotteryUseCase_ConfigureLotteryPhase(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	open := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	// base returns a valid input for ConfigureLotteryPhase; mutate overrides fields.
	base := func() usecase.ConfigureLotteryPhaseInput {
		return usecase.ConfigureLotteryPhaseInput{
			EventID:                  "event-1",
			OpenTime:                 open,
			CloseTime:                open.Add(7 * 24 * time.Hour),
			TicketCapacity:           100,
			MaxTicketsPerApplication: 4,
			TicketPrice:              5000,
		}
	}

	tests := []struct {
		name       string
		mutate     func(*usecase.ConfigureLotteryPhaseInput)
		published  func(context.Context, string) (bool, error)
		wantErr    error
		wantCalled bool // expect phaseRepo.Create to be invoked
	}{
		{
			name:       "success: valid 7-day window creates phase",
			mutate:     func(in *usecase.ConfigureLotteryPhaseInput) {},
			wantCalled: true,
		},
		{
			name:       "success: window of exactly 1 day is valid lower bound",
			mutate:     func(in *usecase.ConfigureLotteryPhaseInput) { in.CloseTime = in.OpenTime.Add(24 * time.Hour) },
			wantCalled: true,
		},
		{
			name:       "success: window of exactly 14 days is valid upper bound",
			mutate:     func(in *usecase.ConfigureLotteryPhaseInput) { in.CloseTime = in.OpenTime.Add(14 * 24 * time.Hour) },
			wantCalled: true,
		},
		{
			name:    "reject: missing event id",
			mutate:  func(in *usecase.ConfigureLotteryPhaseInput) { in.EventID = "" },
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "reject: close not after open",
			mutate:  func(in *usecase.ConfigureLotteryPhaseInput) { in.CloseTime = in.OpenTime },
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "reject: window shorter than 1 day",
			mutate: func(in *usecase.ConfigureLotteryPhaseInput) {
				in.CloseTime = in.OpenTime.Add(23*time.Hour + 59*time.Minute)
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "reject: window longer than 14 days",
			mutate: func(in *usecase.ConfigureLotteryPhaseInput) {
				in.CloseTime = in.OpenTime.Add(14*24*time.Hour + time.Second)
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "reject: non-positive capacity",
			mutate:  func(in *usecase.ConfigureLotteryPhaseInput) { in.TicketCapacity = 0 },
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "reject: non-positive max per application",
			mutate:  func(in *usecase.ConfigureLotteryPhaseInput) { in.MaxTicketsPerApplication = 0 },
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "reject: max exceeds capacity",
			mutate:  func(in *usecase.ConfigureLotteryPhaseInput) { in.MaxTicketsPerApplication = in.TicketCapacity + 1 },
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "reject: zero ticket price",
			mutate:  func(in *usecase.ConfigureLotteryPhaseInput) { in.TicketPrice = 0 },
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "reject: negative ticket price",
			mutate:  func(in *usecase.ConfigureLotteryPhaseInput) { in.TicketPrice = -1 },
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:      "reject: event not published returns FailedPrecondition",
			mutate:    func(in *usecase.ConfigureLotteryPhaseInput) {},
			published: func(context.Context, string) (bool, error) { return false, nil },
			wantErr:   apperr.ErrFailedPrecondition,
		},
		{
			name:   "reject: event not found propagates NotFound",
			mutate: func(in *usecase.ConfigureLotteryPhaseInput) {},
			published: func(context.Context, string) (bool, error) {
				return false, apperr.New(apperr.ErrNotFound.Code, "no event")
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var created bool
			phaseRepo := &stubPhaseRepo{
				createFn: func(_ context.Context, p *entity.LotterySalesPhase) (*entity.LotterySalesPhase, error) {
					created = true
					return p, nil
				},
			}
			eventState := &stubEventState{fn: tt.published}
			uc := newLotteryUC(t, phaseRepo, &stubAppRepo{}, eventState, &stubPaymentPort{}, open)

			in := base()
			tt.mutate(&in)

			got, err := uc.ConfigureLotteryPhase(ctx, in)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)
				assert.False(t, created, "Create must not be called on validation/precondition failure")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, in.EventID, got.EventID)
			assert.Equal(t, in.TicketCapacity, got.TicketCapacity)
			assert.Equal(t, in.TicketPrice, got.TicketPrice)
			assert.NotEmpty(t, got.ID)
			assert.True(t, tt.wantCalled == created)
		})
	}
}

// --- CreateAuthorization ---

func TestLotteryUseCase_CreateAuthorization(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	open := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	close := open.Add(7 * 24 * time.Hour)
	within := open.Add(24 * time.Hour)

	phase := basePhase(open, close)

	base := func() usecase.CreateAuthorizationInput {
		return usecase.CreateAuthorizationInput{
			PhaseID:              "phase-1",
			RequestedTicketCount: 2,
		}
	}

	tests := []struct {
		name         string
		mutate       func(*usecase.CreateAuthorizationInput)
		now          time.Time
		phaseGet     func(context.Context, entity.LotteryPhaseID) (*entity.LotterySalesPhase, error)
		createAuthFn func(ctx context.Context, amountJPY int64) (string, string, error)
		wantErr      error
		wantAmount   int64 // expected amountJPY passed to CreateAuthorization
	}{
		{
			name:       "success: returns piRef and clientSecret, amount = price x count",
			mutate:     func(in *usecase.CreateAuthorizationInput) {},
			now:        within,
			wantAmount: 5000 * 2, // price=5000, count=2
		},
		{
			name:       "success: single ticket, amount = price",
			mutate:     func(in *usecase.CreateAuthorizationInput) { in.RequestedTicketCount = 1 },
			now:        within,
			wantAmount: 5000 * 1,
		},
		{
			name:   "reject: phase not found propagates NotFound",
			mutate: func(in *usecase.CreateAuthorizationInput) {},
			now:    within,
			phaseGet: func(context.Context, entity.LotteryPhaseID) (*entity.LotterySalesPhase, error) {
				return nil, apperr.New(apperr.ErrNotFound.Code, "no phase")
			},
			wantErr: apperr.ErrNotFound,
		},
		{
			name:    "reject: zero count returns InvalidArgument",
			mutate:  func(in *usecase.CreateAuthorizationInput) { in.RequestedTicketCount = 0 },
			now:     within,
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "reject: count exceeds max returns InvalidArgument",
			mutate: func(in *usecase.CreateAuthorizationInput) {
				in.RequestedTicketCount = phase.MaxTicketsPerApplication + 1
			},
			now:     within,
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "reject: before window open returns FailedPrecondition",
			mutate:  func(in *usecase.CreateAuthorizationInput) {},
			now:     open.Add(-time.Hour),
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name:    "reject: at window close returns FailedPrecondition",
			mutate:  func(in *usecase.CreateAuthorizationInput) {},
			now:     close,
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name:   "reject: payment provider failure propagates Unavailable",
			mutate: func(in *usecase.CreateAuthorizationInput) {},
			now:    within,
			createAuthFn: func(_ context.Context, _ int64) (string, string, error) {
				return "", "", apperr.New(apperr.ErrUnavailable.Code, "stripe down")
			},
			wantErr: apperr.ErrUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			phaseGet := tt.phaseGet
			if phaseGet == nil {
				phaseGet = func(context.Context, entity.LotteryPhaseID) (*entity.LotterySalesPhase, error) { return phase, nil }
			}

			var capturedAmount int64
			createAuthFn := tt.createAuthFn
			if createAuthFn == nil {
				createAuthFn = func(_ context.Context, amountJPY int64) (string, string, error) {
					capturedAmount = amountJPY
					return "pi_test_ref", "cs_test_secret", nil
				}
			} else {
				// Wrap to still capture the amount even on failure path (for debugging).
				origFn := tt.createAuthFn
				createAuthFn = func(ctx context.Context, amountJPY int64) (string, string, error) {
					capturedAmount = amountJPY
					return origFn(ctx, amountJPY)
				}
			}

			paymentPort := &stubPaymentPort{createAuthFn: createAuthFn}
			uc := newLotteryUC(t, &stubPhaseRepo{getFn: phaseGet}, &stubAppRepo{}, &stubEventState{}, paymentPort, tt.now)

			in := base()
			tt.mutate(&in)

			got, err := uc.CreateAuthorization(ctx, in)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Empty(t, got.PaymentIntentRef)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "pi_test_ref", got.PaymentIntentRef)
			assert.Equal(t, "cs_test_secret", got.ClientSecret)
			if tt.wantAmount > 0 {
				assert.Equal(t, tt.wantAmount, capturedAmount, "amount passed to CreateAuthorization must be price × count")
			}
		})
	}
}

// --- Apply ---

func TestLotteryUseCase_Apply(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	open := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	close := open.Add(7 * 24 * time.Hour)
	within := open.Add(24 * time.Hour)

	phase := basePhase(open, close)

	base := func() usecase.ApplyInput {
		return usecase.ApplyInput{
			PhaseID:              "phase-1",
			ApplicantID:          "user-1",
			RequestedTicketCount: 2,
			Identity:             entity.ApplicantIdentity{FullName: "山田太郎", PhoneNumber: "+819012345678"},
			PaymentIntentRef:     "pi_test_ref",
		}
	}

	tests := []struct {
		name         string
		mutate       func(*usecase.ApplyInput)
		now          time.Time
		phaseGet     func(context.Context, entity.LotteryPhaseID) (*entity.LotterySalesPhase, error)
		dupGet       func(context.Context, entity.LotteryPhaseID, entity.UserID) (*entity.TicketApplication, error)
		verifyAuthFn func(ctx context.Context, piRef string, expectedAmountJPY int64) error
		wantErr      error
		wantCalled   bool // expect appRepo.Create
	}{
		{
			name:       "success: valid application is persisted in Applied state with authorization",
			mutate:     func(in *usecase.ApplyInput) {},
			now:        within,
			wantCalled: true,
		},
		{
			name:   "reject: phase not found propagates NotFound",
			mutate: func(in *usecase.ApplyInput) {},
			now:    within,
			phaseGet: func(context.Context, entity.LotteryPhaseID) (*entity.LotterySalesPhase, error) {
				return nil, apperr.New(apperr.ErrNotFound.Code, "no phase")
			},
			wantErr: apperr.ErrNotFound,
		},
		{
			name:    "reject: zero count returns InvalidArgument",
			mutate:  func(in *usecase.ApplyInput) { in.RequestedTicketCount = 0 },
			now:     within,
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "reject: count exceeds max returns InvalidArgument",
			mutate:  func(in *usecase.ApplyInput) { in.RequestedTicketCount = 5 },
			now:     within,
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "reject: missing full name returns InvalidArgument",
			mutate:  func(in *usecase.ApplyInput) { in.Identity.FullName = "" },
			now:     within,
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "reject: missing phone returns InvalidArgument",
			mutate:  func(in *usecase.ApplyInput) { in.Identity.PhoneNumber = "" },
			now:     within,
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "reject: before window open returns FailedPrecondition",
			mutate:  func(in *usecase.ApplyInput) {},
			now:     open.Add(-time.Hour),
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name:    "reject: at window close returns FailedPrecondition",
			mutate:  func(in *usecase.ApplyInput) {},
			now:     close,
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name:   "reject: duplicate active application returns FailedPrecondition",
			mutate: func(in *usecase.ApplyInput) {},
			now:    within,
			dupGet: func(context.Context, entity.LotteryPhaseID, entity.UserID) (*entity.TicketApplication, error) {
				return &entity.TicketApplication{ID: "existing", State: entity.TicketApplicationStateApplied}, nil
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name:   "reject: VerifyAuthorization returns InvalidArgument (amount mismatch)",
			mutate: func(in *usecase.ApplyInput) {},
			now:    within,
			verifyAuthFn: func(_ context.Context, _ string, _ int64) error {
				return apperr.New(apperr.ErrInvalidArgument.Code, "amount mismatch")
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:   "reject: VerifyAuthorization returns FailedPrecondition (American Express)",
			mutate: func(in *usecase.ApplyInput) {},
			now:    within,
			verifyAuthFn: func(_ context.Context, _ string, _ int64) error {
				return apperr.New(apperr.ErrFailedPrecondition.Code, "american express cards are not accepted")
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name:   "reject: VerifyAuthorization returns FailedPrecondition (non-JPY card)",
			mutate: func(in *usecase.ApplyInput) {},
			now:    within,
			verifyAuthFn: func(_ context.Context, _ string, _ int64) error {
				return apperr.New(apperr.ErrFailedPrecondition.Code, "non-JPY cards are not accepted")
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			phaseGet := tt.phaseGet
			if phaseGet == nil {
				phaseGet = func(context.Context, entity.LotteryPhaseID) (*entity.LotterySalesPhase, error) { return phase, nil }
			}

			var created bool
			appRepo := &stubAppRepo{
				getByPairFn: tt.dupGet,
				createFn: func(_ context.Context, a *entity.TicketApplication) (*entity.TicketApplication, error) {
					created = true
					return a, nil
				},
			}
			paymentPort := &stubPaymentPort{verifyAuthFn: tt.verifyAuthFn}
			uc := newLotteryUC(t, &stubPhaseRepo{getFn: phaseGet}, appRepo, &stubEventState{}, paymentPort, tt.now)

			in := base()
			tt.mutate(&in)

			got, err := uc.Apply(ctx, in)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)
				assert.False(t, created, "Create must not be called on failure")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.True(t, created)
			assert.Equal(t, entity.TicketApplicationStateApplied, got.State)
			assert.Equal(t, in.PaymentIntentRef, got.Authorization.PaymentIntentRef)
			assert.Equal(t, in.RequestedTicketCount, got.RequestedTicketCount)
			assert.Equal(t, in.Identity, got.Identity)
			assert.NotEmpty(t, got.ID)
		})
	}
}

// TestLotteryUseCase_Apply_VerifiesCorrectAmount checks that Apply passes
// price × count (not just price) to VerifyAuthorization.
func TestLotteryUseCase_Apply_VerifiesCorrectAmount(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	open := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	close := open.Add(7 * 24 * time.Hour)
	within := open.Add(24 * time.Hour)

	phase := basePhase(open, close) // price = 5000, max = 4

	var verifiedAmount int64
	paymentPort := &stubPaymentPort{
		verifyAuthFn: func(_ context.Context, _ string, expectedAmountJPY int64) error {
			verifiedAmount = expectedAmountJPY
			return nil
		},
	}

	appRepo := &stubAppRepo{}
	uc := newLotteryUC(t,
		&stubPhaseRepo{getFn: func(context.Context, entity.LotteryPhaseID) (*entity.LotterySalesPhase, error) { return phase, nil }},
		appRepo, &stubEventState{}, paymentPort, within,
	)

	_, err := uc.Apply(ctx, usecase.ApplyInput{
		PhaseID:              "phase-1",
		ApplicantID:          "user-1",
		RequestedTicketCount: 3,
		Identity:             entity.ApplicantIdentity{FullName: "田中花子", PhoneNumber: "+819011112222"},
		PaymentIntentRef:     "pi_xyz",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(5000*3), verifiedAmount, "VerifyAuthorization must receive price × count")
}

// --- WithdrawApplication ---

func TestLotteryUseCase_WithdrawApplication(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	open := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name          string
		appGet        func(context.Context, entity.TicketApplicationID) (*entity.TicketApplication, error)
		caller        entity.UserID
		cancelAuthFn  func(ctx context.Context, piRef string) error
		wantErr       error
		wantUpdated   bool
		wantCancelled bool
	}{
		{
			name: "success: releases hold and sets state to Withdrawn",
			appGet: func(context.Context, entity.TicketApplicationID) (*entity.TicketApplication, error) {
				return &entity.TicketApplication{
					ID:            "app-1",
					ApplicantID:   "user-1",
					State:         entity.TicketApplicationStateApplied,
					Authorization: entity.PaymentAuthorization{PaymentIntentRef: "pi_test"},
				}, nil
			},
			caller:        "user-1",
			wantUpdated:   true,
			wantCancelled: true,
		},
		{
			name: "reject: not found propagates NotFound",
			appGet: func(context.Context, entity.TicketApplicationID) (*entity.TicketApplication, error) {
				return nil, apperr.New(apperr.ErrNotFound.Code, "no app")
			},
			caller:  "user-1",
			wantErr: apperr.ErrNotFound,
		},
		{
			name: "reject: wrong owner returns PermissionDenied",
			appGet: func(context.Context, entity.TicketApplicationID) (*entity.TicketApplication, error) {
				return &entity.TicketApplication{ID: "app-1", ApplicantID: "user-2", State: entity.TicketApplicationStateApplied}, nil
			},
			caller:  "user-1",
			wantErr: apperr.ErrPermissionDenied,
		},
		{
			name: "reject: Won state is not withdrawable (FailedPrecondition)",
			appGet: func(context.Context, entity.TicketApplicationID) (*entity.TicketApplication, error) {
				return &entity.TicketApplication{ID: "app-1", ApplicantID: "user-1", State: entity.TicketApplicationStateWon}, nil
			},
			caller:  "user-1",
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name: "reject: Lost state is not withdrawable (FailedPrecondition)",
			appGet: func(context.Context, entity.TicketApplicationID) (*entity.TicketApplication, error) {
				return &entity.TicketApplication{ID: "app-1", ApplicantID: "user-1", State: entity.TicketApplicationStateLost}, nil
			},
			caller:  "user-1",
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name: "reject: already Withdrawn is not withdrawable (FailedPrecondition)",
			appGet: func(context.Context, entity.TicketApplicationID) (*entity.TicketApplication, error) {
				return &entity.TicketApplication{ID: "app-1", ApplicantID: "user-1", State: entity.TicketApplicationStateWithdrawn}, nil
			},
			caller:  "user-1",
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name: "reject: CancelAuthorization failure propagates",
			appGet: func(context.Context, entity.TicketApplicationID) (*entity.TicketApplication, error) {
				return &entity.TicketApplication{
					ID:            "app-1",
					ApplicantID:   "user-1",
					State:         entity.TicketApplicationStateApplied,
					Authorization: entity.PaymentAuthorization{PaymentIntentRef: "pi_err"},
				}, nil
			},
			caller: "user-1",
			cancelAuthFn: func(_ context.Context, _ string) error {
				return apperr.New(apperr.ErrUnavailable.Code, "stripe down")
			},
			wantErr: apperr.ErrUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var updated bool
			var cancelledPI string
			appRepo := &stubAppRepo{
				getFn: tt.appGet,
				updateStateFn: func(_ context.Context, _ entity.TicketApplicationID, state entity.TicketApplicationState) error {
					updated = true
					assert.Equal(t, entity.TicketApplicationStateWithdrawn, state)
					return nil
				},
			}
			paymentPort := &stubPaymentPort{
				cancelAuthFn: func(ctx context.Context, piRef string) error {
					cancelledPI = piRef
					if tt.cancelAuthFn != nil {
						return tt.cancelAuthFn(ctx, piRef)
					}
					return nil
				},
			}
			uc := newLotteryUC(t, &stubPhaseRepo{}, appRepo, &stubEventState{}, paymentPort, open)

			err := uc.WithdrawApplication(ctx, "app-1", tt.caller)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.False(t, updated, "UpdateState must not be called on failure")
				return
			}
			require.NoError(t, err)
			assert.True(t, updated)
			if tt.wantCancelled {
				assert.NotEmpty(t, cancelledPI, "CancelAuthorization must be called with the PI ref")
			}
		})
	}
}

// --- RunDraw ---

// makeApp returns a minimal Applied TicketApplication for use in draw tests.
func makeApp(id entity.TicketApplicationID, pi string, count int) *entity.TicketApplication {
	return &entity.TicketApplication{
		ID:                   id,
		PhaseID:              "phase-1",
		ApplicantID:          entity.UserID("user-" + string(id)),
		RequestedTicketCount: count,
		State:                entity.TicketApplicationStateApplied,
		Authorization:        entity.PaymentAuthorization{PaymentIntentRef: pi},
	}
}

func TestLotteryUseCase_RunDraw(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	open := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	// phase with capacity 10, so 3 apps of 2 tickets each: all fit.
	largeCap := basePhase(open, open.Add(7*24*time.Hour))
	largeCap.TicketCapacity = 10

	// phase with tiny capacity: only the first fit wins.
	tightCap := basePhase(open, open.Add(7*24*time.Hour))
	tightCap.TicketCapacity = 2 // only 1 app of 2 tickets fits

	tests := []struct {
		name string
		args struct {
			phase *entity.LotterySalesPhase
			apps  []*entity.TicketApplication
		}
		dep struct {
			captureErr error // non-nil → capture fails for ALL winners
		}
		// assertions
		wantWinnerCount   int
		wantLoserCount    int
		wantPersistCalled bool
	}{
		{
			name: "normal draw: winners captured and marked Won, losers cancelled and marked Lost",
			args: struct {
				phase *entity.LotterySalesPhase
				apps  []*entity.TicketApplication
			}{
				phase: largeCap,
				apps: []*entity.TicketApplication{
					makeApp("app-1", "pi_1", 2),
					makeApp("app-2", "pi_2", 2),
					makeApp("app-3", "pi_3", 2),
				},
			},
			// capacity=10, 3×2=6 ≤ 10 → all three win
			wantWinnerCount:   3,
			wantLoserCount:    0,
			wantPersistCalled: true,
		},
		{
			name: "zero-applications phase: PersistDrawOutcome called with empty slices",
			args: struct {
				phase *entity.LotterySalesPhase
				apps  []*entity.TicketApplication
			}{
				phase: largeCap,
				apps:  nil,
			},
			wantWinnerCount:   0,
			wantLoserCount:    0,
			wantPersistCalled: true,
		},
		{
			name: "capture failure demotes winner to loser, no oversell",
			args: struct {
				phase *entity.LotterySalesPhase
				apps  []*entity.TicketApplication
			}{
				phase: largeCap,
				apps: []*entity.TicketApplication{
					makeApp("app-1", "pi_1", 2),
					makeApp("app-2", "pi_2", 2),
				},
			},
			dep: struct{ captureErr error }{
				captureErr: apperr.New(apperr.ErrFailedPrecondition.Code, "card closed"),
			},
			// both would win (4 ≤ 10) but capture fails → both demoted to losers
			wantWinnerCount:   0,
			wantLoserCount:    2,
			wantPersistCalled: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var persistedWinners []entity.DrawWinnerRow
			var persistedLosers []entity.DrawLoserRow
			var persistCalled bool

			phaseRepo := &stubPhaseRepo{
				getFn: func(_ context.Context, _ entity.LotteryPhaseID) (*entity.LotterySalesPhase, error) {
					return tt.args.phase, nil
				},
			}
			appRepo := &stubAppRepo{
				listAppliedForPhaseFn: func(_ context.Context, _ entity.LotteryPhaseID) ([]*entity.TicketApplication, error) {
					return tt.args.apps, nil
				},
				persistDrawOutcomeFn: func(_ context.Context, _ entity.LotteryPhaseID, w []entity.DrawWinnerRow, l []entity.DrawLoserRow) error {
					persistCalled = true
					persistedWinners = w
					persistedLosers = l
					return nil
				},
			}

			var cancelledPIs []string
			paymentPort := &stubPaymentPort{
				captureAuthFn: func(_ context.Context, pi string) error {
					return tt.dep.captureErr
				},
				cancelAuthFn: func(_ context.Context, pi string) error {
					cancelledPIs = append(cancelledPIs, pi)
					return nil
				},
			}

			uc := newLotteryUC(t, phaseRepo, appRepo, &stubEventState{}, paymentPort, open)
			err := uc.RunDraw(ctx, "phase-1")

			require.NoError(t, err)
			assert.True(t, persistCalled, "PersistDrawOutcome must always be called")
			assert.Len(t, persistedWinners, tt.wantWinnerCount)
			assert.Len(t, persistedLosers, tt.wantLoserCount)

			// Winners must have state Won, losers must have draw_sequence set.
			for _, w := range persistedWinners {
				assert.NotEmpty(t, w.ApplicationID)
			}
			for _, l := range persistedLosers {
				assert.NotEmpty(t, l.ApplicationID)
			}
		})
	}
}

// TestLotteryUseCase_RunDraw_CaptureFail_CancelCalled verifies that when
// capture fails for a winner, CancelAuthorization is called to release the hold
// before the application is demoted to a loser.
func TestLotteryUseCase_RunDraw_CaptureFail_CancelCalled(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	open := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	phase := basePhase(open, open.Add(7*24*time.Hour))
	phase.TicketCapacity = 100

	apps := []*entity.TicketApplication{
		makeApp("app-winner", "pi_winner", 1),
	}

	captureErr := apperr.New(apperr.ErrFailedPrecondition.Code, "card closed")

	var cancelledPI string
	paymentPort := &stubPaymentPort{
		captureAuthFn: func(_ context.Context, _ string) error { return captureErr },
		cancelAuthFn:  func(_ context.Context, pi string) error { cancelledPI = pi; return nil },
	}

	var persistedWinners []entity.DrawWinnerRow
	var persistedLosers []entity.DrawLoserRow
	appRepo := &stubAppRepo{
		listAppliedForPhaseFn: func(_ context.Context, _ entity.LotteryPhaseID) ([]*entity.TicketApplication, error) {
			return apps, nil
		},
		persistDrawOutcomeFn: func(_ context.Context, _ entity.LotteryPhaseID, w []entity.DrawWinnerRow, l []entity.DrawLoserRow) error {
			persistedWinners = w
			persistedLosers = l
			return nil
		},
	}
	phaseRepo := &stubPhaseRepo{
		getFn: func(_ context.Context, _ entity.LotteryPhaseID) (*entity.LotterySalesPhase, error) {
			return phase, nil
		},
	}

	uc := newLotteryUC(t, phaseRepo, appRepo, &stubEventState{}, paymentPort, open)
	err := uc.RunDraw(ctx, "phase-1")

	require.NoError(t, err)
	assert.Equal(t, "pi_winner", cancelledPI, "CancelAuthorization must be called with the winner PI ref after capture failure")
	assert.Empty(t, persistedWinners, "demoted winner must not appear in winners")
	assert.Len(t, persistedLosers, 1, "demoted winner must appear as a loser")
	assert.Equal(t, entity.TicketApplicationID("app-winner"), persistedLosers[0].ApplicationID)
}

// --- DrawDuePhases ---

func TestLotteryUseCase_DrawDuePhases(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	open := now.Add(-8 * 24 * time.Hour)
	close := now.Add(-1 * time.Hour) // window already closed

	duePhase := &entity.LotterySalesPhase{
		ID:                       "phase-due",
		EventID:                  "event-1",
		OpenTime:                 open,
		CloseTime:                close,
		TicketCapacity:           100,
		MaxTicketsPerApplication: 4,
		TicketPrice:              5000,
	}

	t.Run("continues past a failing phase", func(t *testing.T) {
		t.Parallel()

		failPhase := &entity.LotterySalesPhase{
			ID:             "phase-fail",
			EventID:        "event-fail",
			OpenTime:       open,
			CloseTime:      close,
			TicketCapacity: 10,
			TicketPrice:    5000,
		}
		goodPhase := &entity.LotterySalesPhase{
			ID:             "phase-good",
			EventID:        "event-good",
			OpenTime:       open,
			CloseTime:      close,
			TicketCapacity: 10,
			TicketPrice:    5000,
		}

		var persistedPhaseIDs []entity.LotteryPhaseID
		phaseRepo := &stubPhaseRepo{
			listPhasesDueForDrawFn: func(_ context.Context, _ time.Time) ([]*entity.LotterySalesPhase, error) {
				return []*entity.LotterySalesPhase{failPhase, goodPhase}, nil
			},
			getFn: func(_ context.Context, id entity.LotteryPhaseID) (*entity.LotterySalesPhase, error) {
				if id == "phase-fail" {
					return failPhase, nil
				}
				return goodPhase, nil
			},
		}
		appRepo := &stubAppRepo{
			listAppliedForPhaseFn: func(_ context.Context, id entity.LotteryPhaseID) ([]*entity.TicketApplication, error) {
				if id == "phase-fail" {
					return nil, apperr.New(apperr.ErrInternal.Code, "db error")
				}
				return nil, nil
			},
			persistDrawOutcomeFn: func(_ context.Context, id entity.LotteryPhaseID, _ []entity.DrawWinnerRow, _ []entity.DrawLoserRow) error {
				persistedPhaseIDs = append(persistedPhaseIDs, id)
				return nil
			},
		}

		uc := newLotteryUC(t, phaseRepo, appRepo, &stubEventState{}, &stubPaymentPort{}, now)
		err := uc.DrawDuePhases(ctx, now)

		// DrawDuePhases must not return an error even when a phase fails.
		require.NoError(t, err)
		// The good phase must still have been processed.
		assert.Equal(t, []entity.LotteryPhaseID{"phase-good"}, persistedPhaseIDs,
			"good phase must be persisted even though the preceding phase failed")
	})

	t.Run("no-op when no phases are due", func(t *testing.T) {
		t.Parallel()

		phaseRepo := &stubPhaseRepo{
			listPhasesDueForDrawFn: func(_ context.Context, _ time.Time) ([]*entity.LotterySalesPhase, error) {
				return nil, nil
			},
		}

		uc := newLotteryUC(t, phaseRepo, &stubAppRepo{}, &stubEventState{}, &stubPaymentPort{}, now)
		err := uc.DrawDuePhases(ctx, now)
		require.NoError(t, err)
	})

	t.Run("propagates error when ListPhasesDueForDraw fails", func(t *testing.T) {
		t.Parallel()

		dbErr := apperr.New(apperr.ErrInternal.Code, "db down")
		phaseRepo := &stubPhaseRepo{
			listPhasesDueForDrawFn: func(_ context.Context, _ time.Time) ([]*entity.LotterySalesPhase, error) {
				return nil, dbErr
			},
		}

		uc := newLotteryUC(t, phaseRepo, &stubAppRepo{}, &stubEventState{}, &stubPaymentPort{}, now)
		err := uc.DrawDuePhases(ctx, now)
		assert.ErrorIs(t, err, apperr.ErrInternal)
	})

	t.Run("due phase with applications: persist called", func(t *testing.T) {
		t.Parallel()

		var persistCalled bool
		phaseRepo := &stubPhaseRepo{
			listPhasesDueForDrawFn: func(_ context.Context, _ time.Time) ([]*entity.LotterySalesPhase, error) {
				return []*entity.LotterySalesPhase{duePhase}, nil
			},
			getFn: func(_ context.Context, _ entity.LotteryPhaseID) (*entity.LotterySalesPhase, error) {
				return duePhase, nil
			},
		}
		appRepo := &stubAppRepo{
			listAppliedForPhaseFn: func(_ context.Context, _ entity.LotteryPhaseID) ([]*entity.TicketApplication, error) {
				return []*entity.TicketApplication{
					makeApp("app-a", "pi_a", 2),
				}, nil
			},
			persistDrawOutcomeFn: func(_ context.Context, _ entity.LotteryPhaseID, _ []entity.DrawWinnerRow, _ []entity.DrawLoserRow) error {
				persistCalled = true
				return nil
			},
		}

		uc := newLotteryUC(t, phaseRepo, appRepo, &stubEventState{}, &stubPaymentPort{}, now)
		err := uc.DrawDuePhases(ctx, now)

		require.NoError(t, err)
		assert.True(t, persistCalled, "PersistDrawOutcome must be called for the due phase")
	})
}

// --- Apply with identity-verification gate ---

// activeVI returns a VerifiedIdentity with Status=Active for use in tests.
func activeVI(userID string) *entity.VerifiedIdentity {
	return &entity.VerifiedIdentity{
		ID:               "vi-1",
		UserID:           userID,
		Method:           entity.VerificationMethodJPKI,
		PocketSignUserID: "ps-user-abc",
		DedupeStrength:   entity.DedupeStrengthStrong,
		Status:           entity.VerificationStatusActive,
	}
}

// phaseRequiring returns a basePhase with the given VerificationRequirement set.
func phaseRequiring(open, close time.Time, req entity.VerificationRequirement) *entity.LotterySalesPhase {
	p := basePhase(open, close)
	p.VerificationRequirement = req
	return p
}

func TestLotteryUseCase_Apply_VerificationGate(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	open := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	close := open.Add(7 * 24 * time.Hour)
	within := open.Add(24 * time.Hour)

	baseIn := func() usecase.ApplyInput {
		return usecase.ApplyInput{
			PhaseID:              "phase-1",
			ApplicantID:          "user-1",
			RequestedTicketCount: 1,
			Identity:             entity.ApplicantIdentity{FullName: "田中太郎", PhoneNumber: "+819000000001"},
			PaymentIntentRef:     "pi_test",
		}
	}

	tests := []struct {
		name        string
		phase       *entity.LotterySalesPhase
		viGetFn     func(ctx context.Context, userID string) (*entity.VerifiedIdentity, error)
		wantErr     error
		wantApplied bool
	}{
		{
			name:  "allow: phase has no requirement (None) — unverified user succeeds",
			phase: basePhase(open, close), // VerificationRequirement == None (zero)
			viGetFn: func(_ context.Context, _ string) (*entity.VerifiedIdentity, error) {
				// repo should NOT be called for None requirement; returning error to surface
				// any accidental call.
				return nil, apperr.New(apperr.ErrInternal.Code, "unexpected GetByUserID call")
			},
			wantApplied: true,
		},
		{
			name:        "allow: phase requires VerifiedAny — applicant has ACTIVE JPKI identity",
			phase:       phaseRequiring(open, close, entity.VerificationRequirementVerifiedAny),
			viGetFn:     func(_ context.Context, userID string) (*entity.VerifiedIdentity, error) { return activeVI(userID), nil },
			wantApplied: true,
		},
		{
			name:        "allow: phase requires JpkiOnly — applicant has ACTIVE JPKI identity",
			phase:       phaseRequiring(open, close, entity.VerificationRequirementJPKIOnly),
			viGetFn:     func(_ context.Context, userID string) (*entity.VerifiedIdentity, error) { return activeVI(userID), nil },
			wantApplied: true,
		},
		{
			name:  "reject: phase requires VerifiedAny — applicant has no verification record",
			phase: phaseRequiring(open, close, entity.VerificationRequirementVerifiedAny),
			viGetFn: func(_ context.Context, _ string) (*entity.VerifiedIdentity, error) {
				return nil, apperr.New(apperr.ErrNotFound.Code, "no record")
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name:  "reject: phase requires JpkiOnly — applicant has no verification record",
			phase: phaseRequiring(open, close, entity.VerificationRequirementJPKIOnly),
			viGetFn: func(_ context.Context, _ string) (*entity.VerifiedIdentity, error) {
				return nil, apperr.New(apperr.ErrNotFound.Code, "no record")
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name:  "reject: phase requires VerifiedAny — applicant status is NeedsReverification",
			phase: phaseRequiring(open, close, entity.VerificationRequirementVerifiedAny),
			viGetFn: func(_ context.Context, userID string) (*entity.VerifiedIdentity, error) {
				vi := activeVI(userID)
				vi.Status = entity.VerificationStatusNeedsReverification
				return vi, nil
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			name:  "reject: phase requires JpkiOnly — applicant status is NeedsReverification",
			phase: phaseRequiring(open, close, entity.VerificationRequirementJPKIOnly),
			viGetFn: func(_ context.Context, userID string) (*entity.VerifiedIdentity, error) {
				vi := activeVI(userID)
				vi.Status = entity.VerificationStatusNeedsReverification
				return vi, nil
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			phase := tt.phase
			phaseRepo := &stubPhaseRepo{
				getFn: func(_ context.Context, _ entity.LotteryPhaseID) (*entity.LotterySalesPhase, error) {
					return phase, nil
				},
			}

			var applied bool
			appRepo := &stubAppRepo{
				createFn: func(_ context.Context, a *entity.TicketApplication) (*entity.TicketApplication, error) {
					applied = true
					return a, nil
				},
			}

			// For the None case, viGetFn is set to fail; the gate must NOT call it.
			viRepo := &stubVerifiedIdentityRepo{getByUserIDFn: tt.viGetFn}

			uc := newLotteryUC(t, phaseRepo, appRepo, &stubEventState{}, &stubPaymentPort{}, within, viRepo)

			in := baseIn()
			got, err := uc.Apply(ctx, in)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)
				assert.False(t, applied, "Create must not be called when gate rejects")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.True(t, applied)
		})
	}
}

// --- SetPhaseVerificationRequirement ---

func TestLotteryUseCase_SetPhaseVerificationRequirement(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	open := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		args    usecase.SetVerificationRequirementInput
		repoFn  func(context.Context, entity.LotteryPhaseID, entity.VerificationRequirement) (*entity.LotterySalesPhase, error)
		wantReq entity.VerificationRequirement
		wantErr error
	}{
		{
			name: "success: sets VerifiedAny and returns updated phase",
			args: usecase.SetVerificationRequirementInput{
				PhaseID:                 "phase-1",
				VerificationRequirement: entity.VerificationRequirementVerifiedAny,
			},
			wantReq: entity.VerificationRequirementVerifiedAny,
		},
		{
			name: "success: sets JpkiOnly and returns updated phase",
			args: usecase.SetVerificationRequirementInput{
				PhaseID:                 "phase-1",
				VerificationRequirement: entity.VerificationRequirementJPKIOnly,
			},
			wantReq: entity.VerificationRequirementJPKIOnly,
		},
		{
			name: "success: resets to None",
			args: usecase.SetVerificationRequirementInput{
				PhaseID:                 "phase-1",
				VerificationRequirement: entity.VerificationRequirementNone,
			},
			wantReq: entity.VerificationRequirementNone,
		},
		{
			name: "reject: empty phase_id returns InvalidArgument",
			args: usecase.SetVerificationRequirementInput{
				PhaseID:                 "",
				VerificationRequirement: entity.VerificationRequirementJPKIOnly,
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name: "reject: phase not found propagates NotFound",
			args: usecase.SetVerificationRequirementInput{
				PhaseID:                 "phase-missing",
				VerificationRequirement: entity.VerificationRequirementJPKIOnly,
			},
			repoFn: func(_ context.Context, _ entity.LotteryPhaseID, _ entity.VerificationRequirement) (*entity.LotterySalesPhase, error) {
				return nil, apperr.New(apperr.ErrNotFound.Code, "no phase")
			},
			wantErr: apperr.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var capturedReq entity.VerificationRequirement
			repoFn := tt.repoFn
			if repoFn == nil {
				repoFn = func(_ context.Context, id entity.LotteryPhaseID, req entity.VerificationRequirement) (*entity.LotterySalesPhase, error) {
					capturedReq = req
					return &entity.LotterySalesPhase{
						ID:                       id,
						EventID:                  "event-1",
						OpenTime:                 open,
						CloseTime:                open.Add(7 * 24 * time.Hour),
						TicketCapacity:           100,
						MaxTicketsPerApplication: 4,
						TicketPrice:              5000,
						VerificationRequirement:  req,
					}, nil
				}
			}

			phaseRepo := &stubPhaseRepo{
				updateVerificationRequirementFn: repoFn,
			}

			uc := newLotteryUC(t, phaseRepo, &stubAppRepo{}, &stubEventState{}, &stubPaymentPort{}, open)

			got, err := uc.SetPhaseVerificationRequirement(ctx, tt.args)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tt.wantReq, capturedReq, "repo must receive the requested VerificationRequirement")
			assert.Equal(t, tt.wantReq, got.VerificationRequirement)
		})
	}
}
