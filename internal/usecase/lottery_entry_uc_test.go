package usecase_test

import (
	"context"
	"errors"
	mrand "math/rand/v2"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
)

func fixedClock(t time.Time) usecase.Clock {
	return func() time.Time { return t }
}

// lotteryFixture holds the mocks of a LotteryEntryUseCase under test.
type lotteryFixture struct {
	sales                *entitymocks.MockTicketSaleRepository
	entries              *entitymocks.MockLotteryEntryRepository
	referrals            *entitymocks.MockReferralRepository
	tickets              *entitymocks.MockTicketRepository
	identityVerification *entitymocks.MockIdentityVerificationRepository
	users                *entitymocks.MockUserRepository
	cardAuthorizations   *entitymocks.MockCardAuthorizationRepository
	uc                   usecase.LotteryEntryUseCase
}

// newLotteryFixture builds the use case at now, drawing in the order of a
// PCG source seeded with seed.
func newLotteryFixture(t *testing.T, now time.Time, seed uint64) *lotteryFixture {
	t.Helper()
	f := &lotteryFixture{
		sales:                entitymocks.NewMockTicketSaleRepository(t),
		entries:              entitymocks.NewMockLotteryEntryRepository(t),
		referrals:            entitymocks.NewMockReferralRepository(t),
		tickets:              entitymocks.NewMockTicketRepository(t),
		identityVerification: entitymocks.NewMockIdentityVerificationRepository(t),
		users:                entitymocks.NewMockUserRepository(t),
		cardAuthorizations:   entitymocks.NewMockCardAuthorizationRepository(t),
	}
	f.uc = usecase.NewLotteryEntryUseCaseWithRand(f.sales, f.entries, f.referrals, f.tickets, f.identityVerification, f.users, f.cardAuthorizations,
		fixedClock(now), func() *mrand.Rand { return mrand.New(mrand.NewPCG(seed, seed+1)) }, newTestLogger(t))
	return f
}

// lotterySale returns a Lottery sale open from 1 to 10 November with one
// TicketType for event-1 of 100 tickets at 8000 yen, limit 4.
func lotterySale(req entity.VerificationRequirement) *entity.TicketSale {
	sale := entity.NewTicketSale("series-1", "ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(11, 1, 10, 0), saleTime(11, 10, 10, 0))
	sale.VerificationRequirement = req
	entity.NewTicketType(sale, "event-1", 8000, 100, 4)
	return sale
}

// readsTicketType makes TicketType.Get return the sale's first TicketType.
func (f *lotteryFixture) readsTicketType(sale *entity.TicketSale) {
	f.sales.EXPECT().GetTicketType(mock.Anything, sale.TicketTypes[0].ID, mock.Anything).Return(sale, sale.TicketTypes[0], nil).Maybe()
}

// holdsTickets makes the fan hold the given tickets for event-1.
func (f *lotteryFixture) holdsTickets(statuses ...entity.TicketStatus) {
	tickets := make([]*entity.Ticket, 0, len(statuses))
	for _, s := range statuses {
		tickets = append(tickets, &entity.Ticket{EventID: "event-1", Status: s})
	}
	f.tickets.EXPECT().ListByUserAndEvent(mock.Anything, entity.UserID("fan-1"), "event-1").Return(tickets, nil).Maybe()
}

// lotteryEntry returns an entry of fan-1 (or user) for tt.
func lotteryEntry(id entity.LotteryEntryID, tt entity.TicketTypeID, user entity.UserID, state entity.LotteryEntryState, count int, ref string) *entity.LotteryEntry {
	return &entity.LotteryEntry{
		ID: id, TicketTypeID: tt, UserID: user, TicketCount: count, State: state,
		CardAuthorizationID: ref,
	}
}

var (
	duringWindow = saleTime(11, 5, 12, 0)
	afterWindow  = saleTime(11, 10, 10, 0)
)

func TestLotteryEntryUseCase_Start(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("fan starts an entry within the window", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/start "Fan starts an entry within the window"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		f.holdsTickets()
		draft := entity.NewLotteryEntry(sale.TicketTypes[0].ID, "fan-1", 2)
		f.entries.EXPECT().GetOrCreateDraft(mock.Anything, sale.TicketTypes[0].ID, entity.UserID("fan-1"), 2).Return(draft, nil)

		got, err := f.uc.Start(ctx, sale.TicketTypes[0].ID, "fan-1", 2, nil)

		require.NoError(t, err)
		assert.Equal(t, entity.LotteryEntryStateDraft, got.State)
		assert.Equal(t, 2, got.TicketCount)
		assert.False(t, got.HasCardAuthorization(), "nothing is held on the card")
	})

	t.Run("fan reloads mid-entry", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/start "Fan reloads mid-entry"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		f.holdsTickets()
		draft := entity.NewLotteryEntry(sale.TicketTypes[0].ID, "fan-1", 2)
		f.entries.EXPECT().GetOrCreateDraft(mock.Anything, sale.TicketTypes[0].ID, entity.UserID("fan-1"), 2).Return(draft, nil).Twice()

		first, err := f.uc.Start(ctx, sale.TicketTypes[0].ID, "fan-1", 2, nil)
		require.NoError(t, err)
		second, err := f.uc.Start(ctx, sale.TicketTypes[0].ID, "fan-1", 2, nil)

		require.NoError(t, err)
		assert.Equal(t, first.ID, second.ID)
	})

	t.Run("window closed", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/start "Window closed"
		f := newLotteryFixture(t, afterWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)

		_, err := f.uc.Start(ctx, sale.TicketTypes[0].ID, "fan-1", 2, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("count over the limit", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/start "Count over the limit"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)

		_, err := f.uc.Start(ctx, sale.TicketTypes[0].ID, "fan-1", 5, nil)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("presale winner reaches the limit", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/start "Presale winner reaches the limit"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		f.holdsTickets(entity.TicketStatusIssued, entity.TicketStatusIssued, entity.TicketStatusIssued, entity.TicketStatusIssued)

		_, err := f.uc.Start(ctx, sale.TicketTypes[0].ID, "fan-1", 1, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("voided tickets do not count toward the limit", func(t *testing.T) {
		t.Parallel()
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		f.holdsTickets(entity.TicketStatusIssued, entity.TicketStatusIssued, entity.TicketStatusIssued, entity.TicketStatusVoided)
		f.entries.EXPECT().GetOrCreateDraft(mock.Anything, sale.TicketTypes[0].ID, entity.UserID("fan-1"), 1).
			Return(entity.NewLotteryEntry(sale.TicketTypes[0].ID, "fan-1", 1), nil)

		_, err := f.uc.Start(ctx, sale.TicketTypes[0].ID, "fan-1", 1, nil)

		require.NoError(t, err)
	})

	t.Run("second entry", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/start "Second entry"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		f.holdsTickets()
		f.entries.EXPECT().GetOrCreateDraft(mock.Anything, sale.TicketTypes[0].ID, entity.UserID("fan-1"), 2).Return(nil, apperr.ErrAlreadyExists)

		_, err := f.uc.Start(ctx, sale.TicketTypes[0].ID, "fan-1", 2, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("re-entry after withdrawal", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/start "Re-entry after withdrawal"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		f.holdsTickets()
		fresh := entity.NewLotteryEntry(sale.TicketTypes[0].ID, "fan-1", 2)
		f.entries.EXPECT().GetOrCreateDraft(mock.Anything, sale.TicketTypes[0].ID, entity.UserID("fan-1"), 2).Return(fresh, nil)

		got, err := f.uc.Start(ctx, sale.TicketTypes[0].ID, "fan-1", 2, nil)

		require.NoError(t, err)
		assert.Equal(t, fresh.ID, got.ID)
		assert.Equal(t, entity.LotteryEntryStateDraft, got.State)
	})

	t.Run("no identity verification", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/start "No IdentityVerification"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementJPKIOnly)
		f.readsTicketType(sale)
		f.identityVerification.EXPECT().GetByUserID(mock.Anything, "fan-1").Return(nil, apperr.ErrNotFound)

		_, err := f.uc.Start(ctx, sale.TicketTypes[0].ID, "fan-1", 2, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("verification not active", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/start "Verification not active"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementVerifiedAny)
		f.readsTicketType(sale)
		f.identityVerification.EXPECT().GetByUserID(mock.Anything, "fan-1").Return(&entity.IdentityVerification{UserID: "fan-1", Status: entity.VerificationStatusNeedsReverification}, nil)

		_, err := f.uc.Start(ctx, sale.TicketTypes[0].ID, "fan-1", 2, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("active verification", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/start "Active verification"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementJPKIOnly)
		f.readsTicketType(sale)
		f.identityVerification.EXPECT().GetByUserID(mock.Anything, "fan-1").Return(&entity.IdentityVerification{UserID: "fan-1", Status: entity.VerificationStatusActive}, nil)
		f.holdsTickets()
		f.entries.EXPECT().GetOrCreateDraft(mock.Anything, sale.TicketTypes[0].ID, entity.UserID("fan-1"), 2).
			Return(entity.NewLotteryEntry(sale.TicketTypes[0].ID, "fan-1", 2), nil)

		_, err := f.uc.Start(ctx, sale.TicketTypes[0].ID, "fan-1", 2, nil)

		require.NoError(t, err)
	})

	t.Run("first-come ticket type", func(t *testing.T) {
		t.Parallel()
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		sale.Method = entity.TicketSaleMethodFirstCome
		f.readsTicketType(sale)

		_, err := f.uc.Start(ctx, sale.TicketTypes[0].ID, "fan-1", 2, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("unknown ticket type", func(t *testing.T) {
		t.Parallel()
		f := newLotteryFixture(t, duringWindow, 1)
		f.sales.EXPECT().GetTicketType(mock.Anything, entity.TicketTypeID("missing"), duringWindow).Return(nil, nil, apperr.ErrNotFound)

		_, err := f.uc.Start(ctx, "missing", "fan-1", 2, nil)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestLotteryEntryUseCase_Authorize(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("fan authorizes", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Fan authorizes their card"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		draft := lotteryEntry("entry-1", sale.TicketTypes[0].ID, "fan-1", entity.LotteryEntryStateDraft, 2, "")
		f.entries.EXPECT().Get(mock.Anything, draft.ID).Return(draft, nil)
		f.users.EXPECT().UpdatePersonalDetails(mock.Anything, "fan-1", "山田 花子", "+819012345678").Return(&entity.User{ID: "fan-1"}, nil)
		f.cardAuthorizations.EXPECT().Create(mock.Anything, mock.Anything, int64(16000)).Return(&entity.CardAuthorization{ID: "pi_1", Secret: "pi_1_secret", Status: entity.CardAuthorizationStatusPending}, nil)
		f.entries.EXPECT().SetCardAuthorization(mock.Anything, draft.ID, "pi_1").Return(nil)

		secret, err := f.uc.AuthorizeCard(ctx, draft.ID, "fan-1", "山田 花子", "+819012345678")

		require.NoError(t, err)
		assert.Equal(t, "pi_1_secret", secret)
	})

	t.Run("card declined, fan retries", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Card declined, fan retries"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		draft := lotteryEntry("entry-1", sale.TicketTypes[0].ID, "fan-1", entity.LotteryEntryStateDraft, 2, "pi_1")
		f.cardAuthorizations.EXPECT().Get(mock.Anything, "pi_1").Return(&entity.CardAuthorization{ID: "pi_1", Secret: "pi_1_secret", Status: entity.CardAuthorizationStatusPending}, nil)
		f.entries.EXPECT().Get(mock.Anything, draft.ID).Return(draft, nil)
		f.users.EXPECT().UpdatePersonalDetails(mock.Anything, "fan-1", "山田 花子", "+819012345678").Return(&entity.User{ID: "fan-1"}, nil)

		secret, err := f.uc.AuthorizeCard(ctx, draft.ID, "fan-1", "山田 花子", "+819012345678")

		require.NoError(t, err)
		assert.Equal(t, "pi_1_secret", secret, "the same card authorization is returned, no new one is opened")
	})

	t.Run("someone else's entry", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Someone else's entry"
		f := newLotteryFixture(t, duringWindow, 1)
		other := lotteryEntry("entry-1", "tt-1", "fan-2", entity.LotteryEntryStateDraft, 2, "")
		f.entries.EXPECT().Get(mock.Anything, other.ID).Return(other, nil)

		_, err := f.uc.AuthorizeCard(ctx, other.ID, "fan-1", "山田 花子", "+819012345678")

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("unknown entry is not revealed", func(t *testing.T) {
		t.Parallel()
		f := newLotteryFixture(t, duringWindow, 1)
		f.entries.EXPECT().Get(mock.Anything, entity.LotteryEntryID("missing")).Return(nil, apperr.ErrNotFound)

		_, err := f.uc.AuthorizeCard(ctx, "missing", "fan-1", "山田 花子", "+819012345678")

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("missing identity details", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Missing identity details"
		for name, details := range map[string][2]string{
			"empty name":     {"", "+819012345678"},
			"empty phone":    {"山田 花子", ""},
			"domestic phone": {"山田 花子", "090-1234-5678"},
		} {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				f := newLotteryFixture(t, duringWindow, 1)
				sale := lotterySale(entity.VerificationRequirementNone)
				f.readsTicketType(sale)
				draft := lotteryEntry("entry-1", sale.TicketTypes[0].ID, "fan-1", entity.LotteryEntryStateDraft, 2, "")
				f.entries.EXPECT().Get(mock.Anything, draft.ID).Return(draft, nil)

				_, err := f.uc.AuthorizeCard(ctx, draft.ID, "fan-1", details[0], details[1])

				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			})
		}
	})

	t.Run("window closed", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Window closed"
		f := newLotteryFixture(t, afterWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		draft := lotteryEntry("entry-1", sale.TicketTypes[0].ID, "fan-1", entity.LotteryEntryStateDraft, 2, "")
		f.entries.EXPECT().Get(mock.Anything, draft.ID).Return(draft, nil)

		_, err := f.uc.AuthorizeCard(ctx, draft.ID, "fan-1", "山田 花子", "+819012345678")

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("card payments unavailable", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Payment provider unavailable"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		draft := lotteryEntry("entry-1", sale.TicketTypes[0].ID, "fan-1", entity.LotteryEntryStateDraft, 2, "")
		f.entries.EXPECT().Get(mock.Anything, draft.ID).Return(draft, nil)
		f.users.EXPECT().UpdatePersonalDetails(mock.Anything, "fan-1", "山田 花子", "+819012345678").Return(&entity.User{ID: "fan-1"}, nil)
		f.cardAuthorizations.EXPECT().Create(mock.Anything, mock.Anything, int64(16000)).Return(nil, apperr.New(codes.Unavailable, "stripe down"))

		_, err := f.uc.AuthorizeCard(ctx, draft.ID, "fan-1", "山田 花子", "+819012345678")

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		f.entries.AssertNotCalled(t, "SetAuthorization", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("entered entry", func(t *testing.T) {
		t.Parallel()
		f := newLotteryFixture(t, duringWindow, 1)
		entered := lotteryEntry("entry-1", "tt-1", "fan-1", entity.LotteryEntryStateEntered, 2, "pi_1")
		f.entries.EXPECT().Get(mock.Anything, entered.ID).Return(entered, nil)

		_, err := f.uc.AuthorizeCard(ctx, entered.ID, "fan-1", "山田 花子", "+819012345678")

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})
}

func TestLotteryEntryUseCase_Confirm(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("fan confirms", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/confirm "Fan confirms"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		draft := lotteryEntry("entry-1", sale.TicketTypes[0].ID, "fan-1", entity.LotteryEntryStateDraft, 2, "pi_1")
		f.entries.EXPECT().Get(mock.Anything, draft.ID).Return(draft, nil)
		f.cardAuthorizations.EXPECT().Get(mock.Anything, "pi_1").Return(&entity.CardAuthorization{ID: "pi_1", Amount: 16000, CardBrand: "visa", Status: entity.CardAuthorizationStatusAuthorized}, nil)
		f.entries.EXPECT().UpdateState(mock.Anything, draft.ID, entity.LotteryEntryStateEntered).Return(nil)

		got, err := f.uc.Confirm(ctx, draft.ID, "fan-1")

		require.NoError(t, err)
		assert.Equal(t, entity.LotteryEntryStateEntered, got.State)
		assert.Equal(t, "pi_1", got.CardAuthorizationID)
	})

	t.Run("confirmed twice", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/confirm "Confirmed twice"
		f := newLotteryFixture(t, duringWindow, 1)
		entered := lotteryEntry("entry-1", "tt-1", "fan-1", entity.LotteryEntryStateEntered, 2, "pi_1")
		f.entries.EXPECT().Get(mock.Anything, entered.ID).Return(entered, nil)

		got, err := f.uc.Confirm(ctx, entered.ID, "fan-1")

		require.NoError(t, err)
		assert.Equal(t, entered, got)
	})

	t.Run("card authentication not completed", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/confirm "Card authentication not completed"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		draft := lotteryEntry("entry-1", sale.TicketTypes[0].ID, "fan-1", entity.LotteryEntryStateDraft, 2, "pi_1")
		f.entries.EXPECT().Get(mock.Anything, draft.ID).Return(draft, nil)
		f.cardAuthorizations.EXPECT().Get(mock.Anything, "pi_1").Return(&entity.CardAuthorization{
			ID: "pi_1", Amount: 16000, Status: entity.CardAuthorizationStatusPending,
		}, nil)

		_, err := f.uc.Confirm(ctx, draft.ID, "fan-1")

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		f.entries.AssertNotCalled(t, "UpdateState", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("amount differs", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/confirm "Amount differs"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		draft := lotteryEntry("entry-1", sale.TicketTypes[0].ID, "fan-1", entity.LotteryEntryStateDraft, 2, "pi_1")
		f.entries.EXPECT().Get(mock.Anything, draft.ID).Return(draft, nil)
		f.cardAuthorizations.EXPECT().Get(mock.Anything, "pi_1").Return(&entity.CardAuthorization{
			ID: "pi_1", Amount: 8000, CardBrand: "visa", Status: entity.CardAuthorizationStatusAuthorized,
		}, nil)

		_, err := f.uc.Confirm(ctx, draft.ID, "fan-1")

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		f.entries.AssertNotCalled(t, "UpdateState", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("American Express card", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/confirm "American Express card"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		draft := lotteryEntry("entry-1", sale.TicketTypes[0].ID, "fan-1", entity.LotteryEntryStateDraft, 2, "pi_1")
		f.entries.EXPECT().Get(mock.Anything, draft.ID).Return(draft, nil)
		f.cardAuthorizations.EXPECT().Get(mock.Anything, "pi_1").Return(&entity.CardAuthorization{
			ID: "pi_1", Amount: 16000, CardBrand: entity.CardBrandAmex, Status: entity.CardAuthorizationStatusAuthorized,
		}, nil)

		_, err := f.uc.Confirm(ctx, draft.ID, "fan-1")

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "an American Express card does not confirm a lottery entry")
		f.entries.AssertNotCalled(t, "UpdateState", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("draft without a card authorization", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/confirm "Draft without a card authorization"
		f := newLotteryFixture(t, duringWindow, 1)
		draft := lotteryEntry("entry-1", "tt-1", "fan-1", entity.LotteryEntryStateDraft, 2, "")
		f.entries.EXPECT().Get(mock.Anything, draft.ID).Return(draft, nil)

		_, err := f.uc.Confirm(ctx, draft.ID, "fan-1")

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("window closed", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/confirm "Window closed"
		f := newLotteryFixture(t, afterWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		draft := lotteryEntry("entry-1", sale.TicketTypes[0].ID, "fan-1", entity.LotteryEntryStateDraft, 2, "pi_1")
		f.entries.EXPECT().Get(mock.Anything, draft.ID).Return(draft, nil)

		_, err := f.uc.Confirm(ctx, draft.ID, "fan-1")

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("someone else's entry", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/confirm "Someone else's entry"
		f := newLotteryFixture(t, duringWindow, 1)
		other := lotteryEntry("entry-1", "tt-1", "fan-2", entity.LotteryEntryStateDraft, 2, "pi_1")
		f.entries.EXPECT().Get(mock.Anything, other.ID).Return(other, nil)

		_, err := f.uc.Confirm(ctx, other.ID, "fan-1")

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})
}

func TestLotteryEntryUseCase_Withdraw(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("withdraw before the draw", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/withdraw "Withdraw before the draw"
		f := newLotteryFixture(t, duringWindow, 1)
		entered := lotteryEntry("entry-1", "tt-1", "fan-1", entity.LotteryEntryStateEntered, 2, "pi_1")
		f.entries.EXPECT().Get(mock.Anything, entered.ID).Return(entered, nil)
		released := f.cardAuthorizations.EXPECT().Release(mock.Anything, "pi_1").Return(nil).Call
		f.entries.EXPECT().UpdateState(mock.Anything, entered.ID, entity.LotteryEntryStateWithdrawn).Return(nil).NotBefore(released)

		err := f.uc.Withdraw(ctx, entered.ID, "fan-1")

		require.NoError(t, err)
	})

	t.Run("withdraw a draft", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/withdraw "Withdraw a draft"
		f := newLotteryFixture(t, duringWindow, 1)
		draft := lotteryEntry("entry-1", "tt-1", "fan-1", entity.LotteryEntryStateDraft, 2, "")
		f.entries.EXPECT().Get(mock.Anything, draft.ID).Return(draft, nil)
		f.entries.EXPECT().UpdateState(mock.Anything, draft.ID, entity.LotteryEntryStateWithdrawn).Return(nil)

		err := f.uc.Withdraw(ctx, draft.ID, "fan-1")

		require.NoError(t, err)
		f.cardAuthorizations.AssertNotCalled(t, "Release", mock.Anything, mock.Anything)
	})

	t.Run("window closed but not drawn", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/withdraw "Window closed but not drawn"
		f := newLotteryFixture(t, afterWindow, 1)
		entered := lotteryEntry("entry-1", "tt-1", "fan-1", entity.LotteryEntryStateEntered, 2, "pi_1")
		f.entries.EXPECT().Get(mock.Anything, entered.ID).Return(entered, nil)
		f.cardAuthorizations.EXPECT().Release(mock.Anything, "pi_1").Return(nil)
		f.entries.EXPECT().UpdateState(mock.Anything, entered.ID, entity.LotteryEntryStateWithdrawn).Return(nil)

		err := f.uc.Withdraw(ctx, entered.ID, "fan-1")

		require.NoError(t, err)
	})

	t.Run("already drawn", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/withdraw "Already drawn"
		for _, state := range []entity.LotteryEntryState{entity.LotteryEntryStateWon, entity.LotteryEntryStateLost} {
			t.Run(state.String(), func(t *testing.T) {
				t.Parallel()
				f := newLotteryFixture(t, afterWindow, 1)
				drawn := lotteryEntry("entry-1", "tt-1", "fan-1", state, 2, "pi_1")
				f.entries.EXPECT().Get(mock.Anything, drawn.ID).Return(drawn, nil)

				err := f.uc.Withdraw(ctx, drawn.ID, "fan-1")

				assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
			})
		}
	})

	t.Run("another fan's entry", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/withdraw "Another fan's entry"
		f := newLotteryFixture(t, duringWindow, 1)
		other := lotteryEntry("entry-1", "tt-1", "fan-2", entity.LotteryEntryStateEntered, 2, "pi_1")
		f.entries.EXPECT().Get(mock.Anything, other.ID).Return(other, nil)

		err := f.uc.Withdraw(ctx, other.ID, "fan-1")

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("unknown entry", func(t *testing.T) {
		t.Parallel()
		f := newLotteryFixture(t, duringWindow, 1)
		f.entries.EXPECT().Get(mock.Anything, entity.LotteryEntryID("missing")).Return(nil, apperr.ErrNotFound)

		err := f.uc.Withdraw(ctx, "missing", "fan-1")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("release fails", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/withdraw "Release fails"
		f := newLotteryFixture(t, duringWindow, 1)
		entered := lotteryEntry("entry-1", "tt-1", "fan-1", entity.LotteryEntryStateEntered, 2, "pi_1")
		f.entries.EXPECT().Get(mock.Anything, entered.ID).Return(entered, nil)
		f.cardAuthorizations.EXPECT().Release(mock.Anything, "pi_1").Return(apperr.New(codes.Unavailable, "stripe down"))

		err := f.uc.Withdraw(ctx, entered.ID, "fan-1")

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		f.entries.AssertNotCalled(t, "UpdateState", mock.Anything, mock.Anything, mock.Anything)
	})
}

func TestLotteryEntryUseCase_ListByTicketType(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	for _, tc := range []struct {
		name  string
		state entity.LotteryEntryState
	}{
		// @spec components/usecase/lottery-entry/list-by-ticket-type "Entered entry"
		{name: "entered entry", state: entity.LotteryEntryStateEntered},
		// @spec components/usecase/lottery-entry/list-by-ticket-type "Fan won"
		{name: "fan won", state: entity.LotteryEntryStateWon},
		// @spec components/usecase/lottery-entry/list-by-ticket-type "Fan lost"
		{name: "fan lost", state: entity.LotteryEntryStateLost},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newLotteryFixture(t, afterWindow, 1)
			sale := lotterySale(entity.VerificationRequirementNone)
			f.readsTicketType(sale)
			own := lotteryEntry("entry-2", sale.TicketTypes[0].ID, "fan-1", tc.state, 2, "pi_2")
			withdrawn := lotteryEntry("entry-1", sale.TicketTypes[0].ID, "fan-1", entity.LotteryEntryStateWithdrawn, 2, "pi_1")
			f.entries.EXPECT().ListByTicketTypeAndUser(mock.Anything, sale.TicketTypes[0].ID, entity.UserID("fan-1")).
				Return([]*entity.LotteryEntry{own, withdrawn}, nil)

			got, err := f.uc.ListByTicketType(ctx, sale.TicketTypes[0].ID, "fan-1")

			require.NoError(t, err)
			require.Len(t, got, 2)
			assert.Equal(t, tc.state, got[0].State, "the newest entry comes first")
		})
	}

	t.Run("never entered", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/list-by-ticket-type "Never entered"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		f.entries.EXPECT().ListByTicketTypeAndUser(mock.Anything, sale.TicketTypes[0].ID, entity.UserID("fan-1")).Return([]*entity.LotteryEntry{}, nil)

		got, err := f.uc.ListByTicketType(ctx, sale.TicketTypes[0].ID, "fan-1")

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("unknown ticket type", func(t *testing.T) {
		t.Parallel()
		f := newLotteryFixture(t, duringWindow, 1)
		f.sales.EXPECT().GetTicketType(mock.Anything, entity.TicketTypeID("missing"), duringWindow).Return(nil, nil, apperr.ErrNotFound)

		_, err := f.uc.ListByTicketType(ctx, "missing", "fan-1")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

// drawOutcome captures what RunDraw recorded with PersistDrawOutcome.
type drawOutcome struct {
	called  bool
	winners []entity.DrawWinnerRow
	losers  []entity.DrawLoserRow
	drafts  []entity.LotteryEntryID
}

// recordsOutcome captures the outcome RunDraw records for the sale.
func (f *lotteryFixture) recordsOutcome(saleID entity.TicketSaleID, out *drawOutcome) {
	f.entries.EXPECT().PersistDrawOutcome(mock.Anything, saleID, mock.Anything, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, _ entity.TicketSaleID, w []entity.DrawWinnerRow, l []entity.DrawLoserRow, d []entity.LotteryEntryID) error {
			out.called, out.winners, out.losers, out.drafts = true, w, l, d
			return nil
		})
}

// winnerIDs returns the ids of the recorded winners.
func (o *drawOutcome) winnerIDs() []entity.LotteryEntryID {
	ids := make([]entity.LotteryEntryID, 0, len(o.winners))
	for _, w := range o.winners {
		ids = append(ids, w.LotteryEntryID)
	}
	return ids
}

// closedSale returns a closed Lottery sale with one TicketType per quantity.
func closedSale(quantities ...int) *entity.TicketSale {
	sale := entity.NewTicketSale("series-1", "ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(11, 1, 10, 0), saleTime(11, 10, 10, 0))
	for i, q := range quantities {
		entity.NewTicketType(sale, "event-"+string(rune('1'+i)), 8000, q, min(q, 4))
	}
	return sale
}

func TestLotteryEntryUseCase_RunDraw(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("unconfirmed draft at the draw", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/run-draw "Unconfirmed draft at the draw"
		f := newLotteryFixture(t, afterWindow, 1)
		sale := closedSale(4)
		tt := sale.TicketTypes[0].ID
		f.sales.EXPECT().Get(mock.Anything, sale.ID, afterWindow).Return(sale, nil)
		draft := lotteryEntry("draft-1", tt, "fan-1", entity.LotteryEntryStateDraft, 2, "pi_d")
		bare := lotteryEntry("draft-2", tt, "fan-2", entity.LotteryEntryStateDraft, 1, "")
		f.entries.EXPECT().ListUndrawnForTicketType(mock.Anything, tt).Return([]*entity.LotteryEntry{draft, bare}, nil)
		f.cardAuthorizations.EXPECT().Release(mock.Anything, "pi_d").Return(nil).Once()
		var out drawOutcome
		f.recordsOutcome(sale.ID, &out)

		require.NoError(t, f.uc.RunDraw(ctx, sale.ID))

		assert.ElementsMatch(t, []entity.LotteryEntryID{"draft-1", "draft-2"}, out.drafts)
		assert.Empty(t, out.winners)
		assert.Empty(t, out.losers)
	})

	t.Run("winners charged, losers released", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/run-draw "Winners charged, losers released"
		f := newLotteryFixture(t, afterWindow, 1)
		sale := closedSale(4)
		tt := sale.TicketTypes[0].ID
		f.sales.EXPECT().Get(mock.Anything, sale.ID, afterWindow).Return(sale, nil)
		f.entries.EXPECT().ListUndrawnForTicketType(mock.Anything, tt).Return([]*entity.LotteryEntry{
			lotteryEntry("two-a", tt, "fan-1", entity.LotteryEntryStateEntered, 2, "pi_a"),
			lotteryEntry("two-b", tt, "fan-2", entity.LotteryEntryStateEntered, 2, "pi_b"),
			lotteryEntry("three", tt, "fan-3", entity.LotteryEntryStateEntered, 3, "pi_c"),
		}, nil)
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, "pi_a").Return(nil).Once()
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, "pi_b").Return(nil).Once()
		f.cardAuthorizations.EXPECT().Release(mock.Anything, "pi_c").Return(nil).Once()
		var out drawOutcome
		f.recordsOutcome(sale.ID, &out)

		require.NoError(t, f.uc.RunDraw(ctx, sale.ID))

		assert.ElementsMatch(t, []entity.LotteryEntryID{"two-a", "two-b"}, out.winnerIDs())
		require.Len(t, out.losers, 1)
		assert.Equal(t, entity.LotteryEntryID("three"), out.losers[0].LotteryEntryID)
		assert.True(t, out.called, "the sale is marked drawn with the outcome")
	})

	t.Run("each ticket type is drawn against its own quantity", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/run-draw "Each ticket type is drawn against its own quantity"
		f := newLotteryFixture(t, afterWindow, 7)
		sale := closedSale(100, 4)
		big, small := sale.TicketTypes[0].ID, sale.TicketTypes[1].ID
		f.sales.EXPECT().Get(mock.Anything, sale.ID, afterWindow).Return(sale, nil)
		var bigEntries, smallEntries []*entity.LotteryEntry
		for i := range 20 {
			bigEntries = append(bigEntries, lotteryEntry(entity.LotteryEntryID("big-"+string(rune('a'+i))), big, "fan", entity.LotteryEntryStateEntered, 4, "pi_big"))
		}
		for i := range 6 {
			smallEntries = append(smallEntries, lotteryEntry(entity.LotteryEntryID("small-"+string(rune('a'+i))), small, "fan", entity.LotteryEntryStateEntered, 2, "pi_small"))
		}
		f.entries.EXPECT().ListUndrawnForTicketType(mock.Anything, big).Return(bigEntries, nil)
		f.entries.EXPECT().ListUndrawnForTicketType(mock.Anything, small).Return(smallEntries, nil)
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, mock.Anything).Return(nil)
		f.cardAuthorizations.EXPECT().Release(mock.Anything, mock.Anything).Return(nil)
		var out drawOutcome
		f.recordsOutcome(sale.ID, &out)

		require.NoError(t, f.uc.RunDraw(ctx, sale.ID))

		bigWon, smallTickets := 0, 0
		for _, w := range out.winners {
			if len(w.LotteryEntryID) > 3 && w.LotteryEntryID[:3] == "big" {
				bigWon++
			} else {
				smallTickets += 2
			}
		}
		assert.Equal(t, 20, bigWon, "every entry of the 100-ticket type wins")
		assert.LessOrEqual(t, smallTickets, 4, "the 4-ticket type's winners hold at most 4 tickets")
		assert.Len(t, out.losers, 6-smallTickets/2)
	})

	t.Run("losers ordered by draw position", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/run-draw "Losers in draw order"
		f := newLotteryFixture(t, afterWindow, 3)
		sale := closedSale(2)
		tt := sale.TicketTypes[0].ID
		f.sales.EXPECT().Get(mock.Anything, sale.ID, afterWindow).Return(sale, nil)
		f.entries.EXPECT().ListUndrawnForTicketType(mock.Anything, tt).Return([]*entity.LotteryEntry{
			lotteryEntry("e-1", tt, "fan-1", entity.LotteryEntryStateEntered, 2, "pi_1"),
			lotteryEntry("e-2", tt, "fan-2", entity.LotteryEntryStateEntered, 2, "pi_2"),
			lotteryEntry("e-3", tt, "fan-3", entity.LotteryEntryStateEntered, 2, "pi_3"),
			lotteryEntry("e-4", tt, "fan-4", entity.LotteryEntryStateEntered, 2, "pi_4"),
		}, nil)
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, mock.Anything).Return(nil).Once()
		f.cardAuthorizations.EXPECT().Release(mock.Anything, mock.Anything).Return(nil).Times(3)
		var out drawOutcome
		f.recordsOutcome(sale.ID, &out)

		require.NoError(t, f.uc.RunDraw(ctx, sale.ID))

		require.Len(t, out.winners, 1)
		require.Len(t, out.losers, 3)
		positions := map[int]bool{out.winners[0].DrawPosition: true}
		for i, l := range out.losers {
			if i > 0 {
				assert.Greater(t, l.DrawPosition, out.losers[i-1].DrawPosition, "losers are in draw order")
			}
			positions[l.DrawPosition] = true
		}
		assert.Len(t, positions, 4, "each draw position is unique within the ticket type")
	})

	t.Run("no entries", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/run-draw "No entries"
		f := newLotteryFixture(t, afterWindow, 1)
		sale := closedSale(4)
		f.sales.EXPECT().Get(mock.Anything, sale.ID, afterWindow).Return(sale, nil)
		f.entries.EXPECT().ListUndrawnForTicketType(mock.Anything, sale.TicketTypes[0].ID).Return([]*entity.LotteryEntry{}, nil)
		var out drawOutcome
		f.recordsOutcome(sale.ID, &out)

		require.NoError(t, f.uc.RunDraw(ctx, sale.ID))

		assert.True(t, out.called, "the sale is marked drawn")
		assert.Empty(t, out.winners)
		assert.Empty(t, out.losers)
		assert.Empty(t, out.drafts)
	})

	t.Run("winner's card was closed", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/run-draw "Winner's card was closed"
		f := newLotteryFixture(t, afterWindow, 1)
		sale := closedSale(4)
		tt := sale.TicketTypes[0].ID
		f.sales.EXPECT().Get(mock.Anything, sale.ID, afterWindow).Return(sale, nil)
		f.entries.EXPECT().ListUndrawnForTicketType(mock.Anything, tt).Return([]*entity.LotteryEntry{
			lotteryEntry("closed-card", tt, "fan-1", entity.LotteryEntryStateEntered, 2, "pi_closed"),
			lotteryEntry("fine", tt, "fan-2", entity.LotteryEntryStateEntered, 2, "pi_fine"),
		}, nil)
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, "pi_closed").Return(apperr.New(codes.FailedPrecondition, "card closed"))
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, "pi_fine").Return(nil)
		f.cardAuthorizations.EXPECT().Release(mock.Anything, "pi_closed").Return(nil).Once()
		var out drawOutcome
		f.recordsOutcome(sale.ID, &out)

		require.NoError(t, f.uc.RunDraw(ctx, sale.ID))

		assert.Equal(t, []entity.LotteryEntryID{"fine"}, out.winnerIDs(), "no loser is promoted")
		require.Len(t, out.losers, 1)
		assert.Equal(t, entity.LotteryEntryID("closed-card"), out.losers[0].LotteryEntryID)
	})

	t.Run("release fails for one loser", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/run-draw "Release fails for one loser"
		f := newLotteryFixture(t, afterWindow, 1)
		sale := closedSale(2)
		tt := sale.TicketTypes[0].ID
		f.sales.EXPECT().Get(mock.Anything, sale.ID, afterWindow).Return(sale, nil)
		f.entries.EXPECT().ListUndrawnForTicketType(mock.Anything, tt).Return([]*entity.LotteryEntry{
			lotteryEntry("e-1", tt, "fan-1", entity.LotteryEntryStateEntered, 2, "pi_1"),
			lotteryEntry("e-2", tt, "fan-2", entity.LotteryEntryStateEntered, 2, "pi_2"),
			lotteryEntry("e-3", tt, "fan-3", entity.LotteryEntryStateEntered, 2, "pi_3"),
		}, nil)
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, mock.Anything).Return(nil).Once()
		f.cardAuthorizations.EXPECT().Release(mock.Anything, mock.Anything).Return(apperr.New(codes.Unavailable, "stripe down")).Once()
		f.cardAuthorizations.EXPECT().Release(mock.Anything, mock.Anything).Return(nil).Once()
		var out drawOutcome
		f.recordsOutcome(sale.ID, &out)

		require.NoError(t, f.uc.RunDraw(ctx, sale.ID))

		assert.Len(t, out.winners, 1)
		assert.Len(t, out.losers, 2, "the entry whose release failed is still recorded Lost")
	})

	t.Run("after the draw", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/run-draw "After the draw"
		// The use case has no Order or Ticket writer, so the draw cannot issue
		// anything; the Won entry is only recorded.
		f := newLotteryFixture(t, afterWindow, 1)
		sale := closedSale(4)
		tt := sale.TicketTypes[0].ID
		f.sales.EXPECT().Get(mock.Anything, sale.ID, afterWindow).Return(sale, nil)
		f.entries.EXPECT().ListUndrawnForTicketType(mock.Anything, tt).Return([]*entity.LotteryEntry{
			lotteryEntry("e-1", tt, "fan-1", entity.LotteryEntryStateEntered, 2, "pi_1"),
		}, nil)
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, "pi_1").Return(nil)
		var out drawOutcome
		f.recordsOutcome(sale.ID, &out)

		require.NoError(t, f.uc.RunDraw(ctx, sale.ID))

		assert.Equal(t, []entity.LotteryEntryID{"e-1"}, out.winnerIDs())
		f.tickets.AssertNotCalled(t, "ListByUserAndEvent", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("unknown sale", func(t *testing.T) {
		t.Parallel()
		f := newLotteryFixture(t, afterWindow, 1)
		f.sales.EXPECT().Get(mock.Anything, entity.TicketSaleID("missing"), afterWindow).Return(nil, apperr.ErrNotFound)

		assert.ErrorIs(t, f.uc.RunDraw(ctx, "missing"), apperr.ErrNotFound)
	})
}

func TestLotteryEntryUseCase_DrawDueSales(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("window closes", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/draw-due-sales "Window closes"
		f := newLotteryFixture(t, afterWindow, 1)
		sale := closedSale(4)
		f.sales.EXPECT().ListDueForDraw(mock.Anything, afterWindow).Return([]*entity.TicketSale{sale}, nil)
		f.sales.EXPECT().Get(mock.Anything, sale.ID, afterWindow).Return(sale, nil)
		f.entries.EXPECT().ListUndrawnForTicketType(mock.Anything, sale.TicketTypes[0].ID).Return(nil, nil)
		var out drawOutcome
		f.recordsOutcome(sale.ID, &out)

		require.NoError(t, f.uc.DrawDueSales(ctx))

		assert.True(t, out.called, "the sale is drawn")
	})

	t.Run("window still open", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/draw-due-sales "Window still open"
		// TicketSale.ListDueForDraw lists only sales whose end time has passed.
		f := newLotteryFixture(t, duringWindow, 1)
		f.sales.EXPECT().ListDueForDraw(mock.Anything, duringWindow).Return([]*entity.TicketSale{}, nil)

		require.NoError(t, f.uc.DrawDueSales(ctx))
	})

	t.Run("drawn once", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/draw-due-sales "Drawn once"
		// A drawn sale is no longer listed due, so a later run draws nothing.
		f := newLotteryFixture(t, afterWindow, 1)
		sale := closedSale(4)
		f.sales.EXPECT().ListDueForDraw(mock.Anything, afterWindow).Return([]*entity.TicketSale{sale}, nil).Once()
		f.sales.EXPECT().ListDueForDraw(mock.Anything, afterWindow).Return([]*entity.TicketSale{}, nil).Once()
		f.sales.EXPECT().Get(mock.Anything, sale.ID, afterWindow).Return(sale, nil).Once()
		f.entries.EXPECT().ListUndrawnForTicketType(mock.Anything, sale.TicketTypes[0].ID).Return(nil, nil).Once()
		var out drawOutcome
		f.recordsOutcome(sale.ID, &out)

		require.NoError(t, f.uc.DrawDueSales(ctx))
		require.NoError(t, f.uc.DrawDueSales(ctx))

		f.entries.AssertNumberOfCalls(t, "PersistDrawOutcome", 1)
	})

	t.Run("one sale fails", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/draw-due-sales "One sale fails"
		f := newLotteryFixture(t, afterWindow, 1)
		failing, other := closedSale(4), closedSale(4)
		f.sales.EXPECT().ListDueForDraw(mock.Anything, afterWindow).Return([]*entity.TicketSale{failing, other}, nil).Once()
		f.sales.EXPECT().Get(mock.Anything, failing.ID, afterWindow).Return(nil, errors.New("connection reset")).Once()
		f.sales.EXPECT().Get(mock.Anything, other.ID, afterWindow).Return(other, nil).Once()
		f.entries.EXPECT().ListUndrawnForTicketType(mock.Anything, other.TicketTypes[0].ID).Return(nil, nil).Once()
		var otherOut drawOutcome
		f.recordsOutcome(other.ID, &otherOut)

		require.NoError(t, f.uc.DrawDueSales(ctx))
		assert.True(t, otherOut.called, "the other due sale is still drawn")

		// The failed sale stays due and is drawn on the next run.
		f.sales.EXPECT().ListDueForDraw(mock.Anything, afterWindow).Return([]*entity.TicketSale{failing}, nil).Once()
		f.sales.EXPECT().Get(mock.Anything, failing.ID, afterWindow).Return(failing, nil).Once()
		f.entries.EXPECT().ListUndrawnForTicketType(mock.Anything, failing.TicketTypes[0].ID).Return(nil, nil).Once()
		var failingOut drawOutcome
		f.recordsOutcome(failing.ID, &failingOut)

		require.NoError(t, f.uc.DrawDueSales(ctx))
		assert.True(t, failingOut.called)
	})

	t.Run("listing fails", func(t *testing.T) {
		t.Parallel()
		f := newLotteryFixture(t, afterWindow, 1)
		listErr := apperr.New(codes.Internal, "db down")
		f.sales.EXPECT().ListDueForDraw(mock.Anything, afterWindow).Return(nil, listErr)

		assert.Equal(t, listErr, f.uc.DrawDueSales(ctx))
	})
}

func TestLotteryEntryUseCase_Start_RecordsReferral(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	code := func(c entity.ReferralCode) *entity.ReferralCode { return &c }
	// startsDraft makes the sale open and GetOrCreateDraft return a Draft of
	// 2 tickets.
	startsDraft := func(f *lotteryFixture, sale *entity.TicketSale) *entity.LotteryEntry {
		f.readsTicketType(sale)
		f.holdsTickets()
		draft := entity.NewLotteryEntry(sale.TicketTypes[0].ID, "fan-1", 2)
		f.entries.EXPECT().GetOrCreateDraft(mock.Anything, sale.TicketTypes[0].ID, entity.UserID("fan-1"), 2).Return(draft, nil)
		return draft
	}

	t.Run("entry from a referral link", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/start "Entry from a referral link"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		draft := startsDraft(f, sale)
		f.referrals.EXPECT().Record(mock.Anything, entity.ReferralSource{LotteryEntryID: draft.ID}, entity.ReferralCode("member-a"), duringWindow).Return(nil)

		got, err := f.uc.Start(ctx, sale.TicketTypes[0].ID, "fan-1", 2, code("member-a"))

		require.NoError(t, err)
		assert.Same(t, draft, got)
	})

	t.Run("another link before confirming", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/start "Another link before confirming"
		// The Draft already has member-a; Record replaces it.
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		draft := startsDraft(f, sale)
		f.referrals.EXPECT().Record(mock.Anything, entity.ReferralSource{LotteryEntryID: draft.ID}, entity.ReferralCode("member-b"), duringWindow).Return(nil)

		got, err := f.uc.Start(ctx, sale.TicketTypes[0].ID, "fan-1", 2, code("member-b"))

		require.NoError(t, err)
		assert.Equal(t, draft.ID, got.ID)
	})

	t.Run("started again without a code", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/start "Started again without a code"
		// No Record call: the Referral holding member-a stays as it is.
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		draft := startsDraft(f, sale)

		got, err := f.uc.Start(ctx, sale.TicketTypes[0].ID, "fan-1", 2, nil)

		require.NoError(t, err)
		assert.Equal(t, draft.ID, got.ID)
		f.referrals.AssertNotCalled(t, "Record", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("already entered with a code", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/start "Already entered with a code"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		f.holdsTickets()
		f.entries.EXPECT().GetOrCreateDraft(mock.Anything, sale.TicketTypes[0].ID, entity.UserID("fan-1"), 2).Return(nil, apperr.ErrAlreadyExists)

		_, err := f.uc.Start(ctx, sale.TicketTypes[0].ID, "fan-1", 2, code("member-b"))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		f.referrals.AssertNotCalled(t, "Record", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("record failure fails start", func(t *testing.T) {
		t.Parallel()
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		draft := startsDraft(f, sale)
		f.referrals.EXPECT().Record(mock.Anything, entity.ReferralSource{LotteryEntryID: draft.ID}, entity.ReferralCode("member-a"), duringWindow).Return(apperr.ErrInternal)

		_, err := f.uc.Start(ctx, sale.TicketTypes[0].ID, "fan-1", 2, code("member-a"))

		assert.ErrorIs(t, err, apperr.ErrInternal)
	})
}
