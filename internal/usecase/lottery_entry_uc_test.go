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
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

func fixedClock(t time.Time) usecase.Clock {
	return func() time.Time { return t }
}

// lotteryFixture holds the mocks of a LotteryEntryUseCase under test.
type lotteryFixture struct {
	sales                *entitymocks.MockTicketSaleRepository
	entries              *entitymocks.MockLotteryEntryRepository
	tickets              *entitymocks.MockTicketRepository
	identityVerification *entitymocks.MockIdentityVerificationRepository
	users                *entitymocks.MockUserRepository
	cardAuthorizations   *entitymocks.MockCardAuthorizationRepository
	uc                   usecase.LotteryEntryUseCase
	// drawn collects the ORDER.lottery_entry_drawn announcements.
	drawn []entity.LotteryEntryDrawnData
}

// newLotteryFixture builds the use case at now, drawing in the order of a
// PCG source seeded with seed.
func newLotteryFixture(t *testing.T, now time.Time, seed uint64) *lotteryFixture {
	t.Helper()
	f := &lotteryFixture{
		sales:                entitymocks.NewMockTicketSaleRepository(t),
		entries:              entitymocks.NewMockLotteryEntryRepository(t),
		tickets:              entitymocks.NewMockTicketRepository(t),
		identityVerification: entitymocks.NewMockIdentityVerificationRepository(t),
		users:                entitymocks.NewMockUserRepository(t),
		cardAuthorizations:   entitymocks.NewMockCardAuthorizationRepository(t),
	}
	publisher := ucmocks.NewMockEventPublisher(t)
	publisher.EXPECT().
		PublishEvent(mock.Anything, entity.SubjectLotteryEntryDrawn, mock.Anything).
		RunAndReturn(func(_ context.Context, _ string, data any) error {
			f.drawn = append(f.drawn, data.(entity.LotteryEntryDrawnData))
			return nil
		}).
		Maybe()
	f.uc = usecase.NewLotteryEntryUseCaseWithRand(f.sales, f.entries, f.tickets, f.identityVerification, f.users, f.cardAuthorizations,
		publisher, fixedClock(now), func() *mrand.Rand { return mrand.New(mrand.NewPCG(seed, seed+1)) }, newTestLogger(t))
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

// authorization returns a card authorization of a lottery entry of fan for
// count tickets of tt.
func authorization(id string, status entity.CardAuthorizationStatus, amount int64, brand entity.CardBrand, fan entity.UserID, tt entity.TicketTypeID, count int, code *entity.ReferralCode) *entity.CardAuthorization {
	return &entity.CardAuthorization{
		ID: id, Amount: amount, CardBrand: brand, Status: status, Secret: id + "_secret",
		Purchase: entity.CardAuthorizationPurchase{
			Purpose: entity.CardAuthorizationPurposeLotteryEntry, UserID: fan, TicketTypeID: tt, EventID: "event-1",
			TicketCount: count, ReferralCode: code,
		},
	}
}

func TestLotteryEntryUseCase_AuthorizeCard(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const name, phone = "山田 花子", "+819012345678"
	code := func(c entity.ReferralCode) *entity.ReferralCode { return &c }

	// opens expects the fan's details stored and then a card authorization
	// opened for count tickets with the code.
	opens := func(f *lotteryFixture, sale *entity.TicketSale, count int, c *entity.ReferralCode) *entity.CardAuthorization {
		tt := sale.TicketTypes[0]
		opened := authorization("pi_"+string(rune('0'+count)), entity.CardAuthorizationStatusPending, tt.AmountFor(count), "", "fan-1", tt.ID, count, c)
		details := f.users.EXPECT().UpdatePersonalDetails(mock.Anything, "fan-1", name, phone).
			Return(&entity.User{ID: "fan-1", FullName: name, PhoneNumber: phone}, nil).Once()
		f.cardAuthorizations.EXPECT().Create(mock.Anything, opened.Purchase, tt.AmountFor(count)).Return(opened, nil).Once().NotBefore(details)
		return opened
	}
	// notEntered makes the fan have no entry for the TicketType.
	notEntered := func(f *lotteryFixture, sale *entity.TicketSale) {
		f.entries.EXPECT().ListByTicketTypeAndUser(mock.Anything, sale.TicketTypes[0].ID, entity.UserID("fan-1")).Return([]*entity.LotteryEntry{}, nil).Maybe()
	}

	t.Run("fan authorizes their card", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Fan authorizes their card"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		f.holdsTickets()
		notEntered(f, sale)
		opened := opens(f, sale, 2, nil)

		got, err := f.uc.AuthorizeCard(ctx, sale.TicketTypes[0].ID, "fan-1", 2, name, phone, nil)

		require.NoError(t, err)
		assert.Equal(t, opened.ID, got.ID)
		assert.Equal(t, opened.Secret, got.Secret)
		assert.Equal(t, int64(16000), opened.Amount)
		f.entries.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	})

	t.Run("count changed before card authentication", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Count changed before card authentication"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		f.holdsTickets()
		notEntered(f, sale)
		first := opens(f, sale, 2, nil)
		second := opens(f, sale, 3, nil)

		got2, err := f.uc.AuthorizeCard(ctx, sale.TicketTypes[0].ID, "fan-1", 2, name, phone, nil)
		require.NoError(t, err)
		got3, err := f.uc.AuthorizeCard(ctx, sale.TicketTypes[0].ID, "fan-1", 3, name, phone, nil)
		require.NoError(t, err)

		assert.Equal(t, first.ID, got2.ID)
		assert.Equal(t, second.ID, got3.ID)
		assert.Equal(t, int64(24000), second.Amount)
		f.entries.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	})

	t.Run("entry from a referral link", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Entry from a referral link"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		f.holdsTickets()
		notEntered(f, sale)
		opened := opens(f, sale, 2, code("member-a"))

		got, err := f.uc.AuthorizeCard(ctx, sale.TicketTypes[0].ID, "fan-1", 2, name, phone, code("member-a"))

		require.NoError(t, err)
		assert.Equal(t, opened.ID, got.ID)
		assert.Equal(t, code("member-a"), got.Purchase.ReferralCode)
	})

	t.Run("window closed", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Window closed"
		f := newLotteryFixture(t, afterWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)

		_, err := f.uc.AuthorizeCard(ctx, sale.TicketTypes[0].ID, "fan-1", 2, name, phone, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("count over the limit", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Count over the limit"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)

		_, err := f.uc.AuthorizeCard(ctx, sale.TicketTypes[0].ID, "fan-1", 5, name, phone, nil)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("presale winner reaches the limit", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Presale winner reaches the limit"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		f.holdsTickets(entity.TicketStatusIssued, entity.TicketStatusIssued, entity.TicketStatusIssued, entity.TicketStatusIssued)

		_, err := f.uc.AuthorizeCard(ctx, sale.TicketTypes[0].ID, "fan-1", 1, name, phone, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("voided tickets do not count toward the limit", func(t *testing.T) {
		t.Parallel()
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		f.holdsTickets(entity.TicketStatusIssued, entity.TicketStatusIssued, entity.TicketStatusVoided, entity.TicketStatusVoided)
		notEntered(f, sale)
		opens(f, sale, 2, nil)

		_, err := f.uc.AuthorizeCard(ctx, sale.TicketTypes[0].ID, "fan-1", 2, name, phone, nil)

		require.NoError(t, err)
	})

	t.Run("already entered", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Already entered"
		for _, state := range []entity.LotteryEntryState{entity.LotteryEntryStateEntered, entity.LotteryEntryStateWon, entity.LotteryEntryStateLost} {
			t.Run(state.String(), func(t *testing.T) {
				t.Parallel()
				f := newLotteryFixture(t, duringWindow, 1)
				sale := lotterySale(entity.VerificationRequirementNone)
				f.readsTicketType(sale)
				f.holdsTickets()
				f.entries.EXPECT().ListByTicketTypeAndUser(mock.Anything, sale.TicketTypes[0].ID, entity.UserID("fan-1")).
					Return([]*entity.LotteryEntry{lotteryEntry("entry-1", sale.TicketTypes[0].ID, "fan-1", state, 2, "pi_1")}, nil)

				_, err := f.uc.AuthorizeCard(ctx, sale.TicketTypes[0].ID, "fan-1", 2, name, phone, nil)

				assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
			})
		}
	})

	t.Run("missing identity details", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Missing identity details"
		for label, details := range map[string][2]string{"no name": {"", phone}, "no phone": {name, ""}} {
			t.Run(label, func(t *testing.T) {
				t.Parallel()
				f := newLotteryFixture(t, duringWindow, 1)
				sale := lotterySale(entity.VerificationRequirementNone)
				f.readsTicketType(sale)

				_, err := f.uc.AuthorizeCard(ctx, sale.TicketTypes[0].ID, "fan-1", 2, details[0], details[1], nil)

				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			})
		}
	})

	t.Run("payment provider unavailable", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Payment provider unavailable"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		f.holdsTickets()
		notEntered(f, sale)
		f.users.EXPECT().UpdatePersonalDetails(mock.Anything, "fan-1", name, phone).Return(&entity.User{ID: "fan-1"}, nil)
		f.cardAuthorizations.EXPECT().Create(mock.Anything, mock.Anything, int64(16000)).Return(nil, apperr.New(codes.Unavailable, "stripe down"))

		_, err := f.uc.AuthorizeCard(ctx, sale.TicketTypes[0].ID, "fan-1", 2, name, phone, nil)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})

	t.Run("no identity verification", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "No IdentityVerification"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementJPKIOnly)
		f.readsTicketType(sale)
		f.identityVerification.EXPECT().GetByUserID(mock.Anything, "fan-1").Return(nil, apperr.ErrNotFound)

		_, err := f.uc.AuthorizeCard(ctx, sale.TicketTypes[0].ID, "fan-1", 2, name, phone, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("verification not active", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Verification not active"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementVerifiedAny)
		f.readsTicketType(sale)
		f.identityVerification.EXPECT().GetByUserID(mock.Anything, "fan-1").Return(&entity.IdentityVerification{UserID: "fan-1", Status: entity.VerificationStatusNeedsReverification}, nil)

		_, err := f.uc.AuthorizeCard(ctx, sale.TicketTypes[0].ID, "fan-1", 2, name, phone, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("active verification", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Active verification"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementJPKIOnly)
		f.readsTicketType(sale)
		f.identityVerification.EXPECT().GetByUserID(mock.Anything, "fan-1").Return(&entity.IdentityVerification{UserID: "fan-1", Status: entity.VerificationStatusActive}, nil)
		f.holdsTickets()
		notEntered(f, sale)
		opens(f, sale, 2, nil)

		_, err := f.uc.AuthorizeCard(ctx, sale.TicketTypes[0].ID, "fan-1", 2, name, phone, nil)

		require.NoError(t, err)
	})

	t.Run("first-come ticket type", func(t *testing.T) {
		t.Parallel()
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		sale.Method = entity.TicketSaleMethodFirstCome
		f.readsTicketType(sale)

		_, err := f.uc.AuthorizeCard(ctx, sale.TicketTypes[0].ID, "fan-1", 2, name, phone, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("unknown ticket type", func(t *testing.T) {
		t.Parallel()
		f := newLotteryFixture(t, duringWindow, 1)
		f.sales.EXPECT().GetTicketType(mock.Anything, entity.TicketTypeID("missing"), duringWindow).Return(nil, nil, apperr.ErrNotFound)

		_, err := f.uc.AuthorizeCard(ctx, "missing", "fan-1", 2, name, phone, nil)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestLotteryEntryUseCase_Confirm(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fan := entity.UserID("fan-1")
	code := entity.ReferralCode("member-a")

	// authorized makes the fan's Authorized 16000 yen card authorization for 2
	// tickets readable, with no entry holding it yet.
	authorized := func(f *lotteryFixture, sale *entity.TicketSale, amount int64, brand entity.CardBrand) *entity.CardAuthorization {
		a := authorization("pi_1", entity.CardAuthorizationStatusAuthorized, amount, brand, fan, sale.TicketTypes[0].ID, 2, &code)
		f.cardAuthorizations.EXPECT().Get(mock.Anything, "pi_1").Return(a, nil)
		f.entries.EXPECT().GetByCardAuthorizationID(mock.Anything, "pi_1").Return(nil, apperr.ErrNotFound).Maybe()
		return a
	}
	// creates expects the entry made from the card authorization.
	creates := func(f *lotteryFixture, sale *entity.TicketSale) *entitymocks.MockLotteryEntryRepository_Create_Call {
		return f.entries.EXPECT().Create(mock.Anything, mock.MatchedBy(func(e *entity.LotteryEntry) bool {
			return e.TicketTypeID == sale.TicketTypes[0].ID && e.UserID == fan && e.TicketCount == 2 &&
				e.CardAuthorizationID == "pi_1" && e.State == entity.LotteryEntryStateEntered &&
				e.ReferralCode != nil && *e.ReferralCode == code
		}))
	}

	t.Run("fan confirms", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/confirm "Fan confirms"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		authorized(f, sale, 16000, "visa")
		creates(f, sale).RunAndReturn(func(_ context.Context, e *entity.LotteryEntry) (*entity.LotteryEntry, error) { return e, nil })

		got, err := f.uc.Confirm(ctx, "pi_1", &fan)

		require.NoError(t, err)
		assert.Equal(t, entity.LotteryEntryStateEntered, got.State)
		assert.Equal(t, 2, got.TicketCount)
		assert.Equal(t, "pi_1", got.CardAuthorizationID)
		f.cardAuthorizations.AssertNotCalled(t, "Capture", mock.Anything, mock.Anything)
		f.cardAuthorizations.AssertNotCalled(t, "Release", mock.Anything, mock.Anything)
	})

	t.Run("confirmed twice", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/confirm "Confirmed twice"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		a := authorization("pi_1", entity.CardAuthorizationStatusAuthorized, 16000, "visa", fan, sale.TicketTypes[0].ID, 2, nil)
		f.cardAuthorizations.EXPECT().Get(mock.Anything, "pi_1").Return(a, nil)
		existing := lotteryEntry("entry-1", sale.TicketTypes[0].ID, fan, entity.LotteryEntryStateEntered, 2, "pi_1")
		f.entries.EXPECT().GetByCardAuthorizationID(mock.Anything, "pi_1").Return(existing, nil)

		got, err := f.uc.Confirm(ctx, "pi_1", &fan)

		require.NoError(t, err)
		assert.Same(t, existing, got)
	})

	t.Run("card authentication not completed", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/confirm "Card authentication not completed"
		for _, status := range []entity.CardAuthorizationStatus{
			entity.CardAuthorizationStatusPending, entity.CardAuthorizationStatusCaptured, entity.CardAuthorizationStatusReleased,
		} {
			f := newLotteryFixture(t, duringWindow, 1)
			sale := lotterySale(entity.VerificationRequirementNone)
			f.cardAuthorizations.EXPECT().Get(mock.Anything, "pi_1").
				Return(authorization("pi_1", status, 16000, "", fan, sale.TicketTypes[0].ID, 2, nil), nil)
			f.entries.EXPECT().GetByCardAuthorizationID(mock.Anything, "pi_1").Return(nil, apperr.ErrNotFound)

			_, err := f.uc.Confirm(ctx, "pi_1", &fan)

			assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
			f.cardAuthorizations.AssertNotCalled(t, "Release", mock.Anything, mock.Anything)
		}
	})

	rejected := []struct {
		name    string
		setup   func(f *lotteryFixture, sale *entity.TicketSale)
		wantErr error
	}{
		{
			// @spec components/usecase/lottery-entry/confirm "American Express card"
			name: "American Express card",
			setup: func(f *lotteryFixture, sale *entity.TicketSale) {
				f.readsTicketType(sale)
				authorized(f, sale, 16000, entity.CardBrandAmex)
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			// @spec components/usecase/lottery-entry/confirm "Amount differs"
			name: "amount differs",
			setup: func(f *lotteryFixture, sale *entity.TicketSale) {
				f.readsTicketType(sale)
				authorized(f, sale, 8000, "visa")
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			// @spec components/usecase/lottery-entry/confirm "Second entry"
			name: "second entry",
			setup: func(f *lotteryFixture, sale *entity.TicketSale) {
				f.readsTicketType(sale)
				authorized(f, sale, 16000, "visa")
				creates(f, sale).Return(nil, apperr.New(codes.AlreadyExists, "already entered"))
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			// @spec components/usecase/lottery-entry/confirm "After the end time"
			name: "after the end time",
			setup: func(f *lotteryFixture, sale *entity.TicketSale) {
				f.readsTicketType(sale)
				authorized(f, sale, 16000, "visa")
				creates(f, sale).Return(nil, apperr.New(codes.FailedPrecondition, "the sale has ended"))
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			// @spec components/usecase/lottery-entry/confirm "TicketType gone"
			name: "TicketType gone",
			setup: func(f *lotteryFixture, sale *entity.TicketSale) {
				f.sales.EXPECT().GetTicketType(mock.Anything, sale.TicketTypes[0].ID, mock.Anything).Return(nil, nil, apperr.ErrNotFound)
				authorized(f, sale, 16000, "visa")
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
	}
	for _, tt := range rejected {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newLotteryFixture(t, duringWindow, 1)
			sale := lotterySale(entity.VerificationRequirementNone)
			tt.setup(f, sale)
			f.cardAuthorizations.EXPECT().Release(mock.Anything, "pi_1").Return(nil).Once()

			got, err := f.uc.Confirm(ctx, "pi_1", &fan)

			assert.ErrorIs(t, err, tt.wantErr)
			assert.Nil(t, got, "no entry is made")
		})
	}

	t.Run("release fails", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/confirm "Release fails"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		authorized(f, sale, 16000, entity.CardBrandAmex)
		f.cardAuthorizations.EXPECT().Release(mock.Anything, "pi_1").Return(apperr.New(codes.Unavailable, "stripe down"))

		got, err := f.uc.Confirm(ctx, "pi_1", &fan)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Nil(t, got)
	})

	t.Run("storing fails without a rejection", func(t *testing.T) {
		t.Parallel()
		// A failure that is not a rejection keeps the card authorization, so
		// a retry can still make the entry.
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		authorized(f, sale, 16000, "visa")
		creates(f, sale).Return(nil, apperr.New(codes.Unavailable, "lock timeout"))

		_, err := f.uc.Confirm(ctx, "pi_1", &fan)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		f.cardAuthorizations.AssertNotCalled(t, "Release", mock.Anything, mock.Anything)
	})

	t.Run("someone else's card authorization", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/confirm "Someone else's card authorization"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.cardAuthorizations.EXPECT().Get(mock.Anything, "pi_1").
			Return(authorization("pi_1", entity.CardAuthorizationStatusAuthorized, 16000, "visa", "fan-2", sale.TicketTypes[0].ID, 2, nil), nil)

		got, err := f.uc.Confirm(ctx, "pi_1", &fan)

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		assert.Nil(t, got)
		f.entries.AssertNotCalled(t, "GetByCardAuthorizationID", mock.Anything, mock.Anything)
	})

	t.Run("unknown or checkout card authorization is not revealed", func(t *testing.T) {
		t.Parallel()
		f := newLotteryFixture(t, duringWindow, 1)
		f.cardAuthorizations.EXPECT().Get(mock.Anything, "pi_missing").Return(nil, apperr.ErrNotFound)
		f.cardAuthorizations.EXPECT().Get(mock.Anything, "pi_checkout").Return(&entity.CardAuthorization{
			ID: "pi_checkout", Status: entity.CardAuthorizationStatusAuthorized,
			Purchase: entity.CardAuthorizationPurchase{Purpose: entity.CardAuthorizationPurposeCheckout, CheckoutID: "checkout-1", UserID: fan},
		}, nil)

		_, err := f.uc.Confirm(ctx, "pi_missing", &fan)
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		_, err = f.uc.Confirm(ctx, "pi_checkout", &fan)
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("browser closed after card authentication", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/confirm "Browser closed after card authentication"
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		f.readsTicketType(sale)
		authorized(f, sale, 16000, "visa")
		creates(f, sale).RunAndReturn(func(_ context.Context, e *entity.LotteryEntry) (*entity.LotteryEntry, error) { return e, nil })

		got, err := f.uc.Confirm(ctx, "pi_1", nil)

		require.NoError(t, err)
		assert.Equal(t, "pi_1", got.CardAuthorizationID)
		assert.Equal(t, fan, got.UserID)
	})

	t.Run("checkout's card authorization reported", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/confirm "Checkout's card authorization reported"
		f := newLotteryFixture(t, duringWindow, 1)
		f.cardAuthorizations.EXPECT().Get(mock.Anything, "pi_checkout").Return(&entity.CardAuthorization{
			ID: "pi_checkout", Status: entity.CardAuthorizationStatusAuthorized,
			Purchase: entity.CardAuthorizationPurchase{Purpose: entity.CardAuthorizationPurposeCheckout, CheckoutID: "checkout-1", UserID: fan},
		}, nil)

		got, err := f.uc.Confirm(ctx, "pi_checkout", nil)

		require.NoError(t, err)
		assert.Nil(t, got)
		f.cardAuthorizations.AssertNotCalled(t, "Release", mock.Anything, mock.Anything)
	})

	t.Run("payment provider unavailable", func(t *testing.T) {
		t.Parallel()
		f := newLotteryFixture(t, duringWindow, 1)
		f.cardAuthorizations.EXPECT().Get(mock.Anything, "pi_1").Return(nil, apperr.New(codes.Unavailable, "stripe down"))

		_, err := f.uc.Confirm(ctx, "pi_1", nil)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
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
			own := lotteryEntry("entry-1", sale.TicketTypes[0].ID, "fan-1", tc.state, 2, "pi_1")
			f.entries.EXPECT().ListByTicketTypeAndUser(mock.Anything, sale.TicketTypes[0].ID, entity.UserID("fan-1")).
				Return([]*entity.LotteryEntry{own}, nil)

			got, err := f.uc.ListByTicketType(ctx, sale.TicketTypes[0].ID, "fan-1")

			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, tc.state, got[0].State)
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
}

// recordsOutcome captures the outcome RunDraw records for the sale.
func (f *lotteryFixture) recordsOutcome(saleID entity.TicketSaleID, out *drawOutcome) {
	f.entries.EXPECT().PersistDrawOutcome(mock.Anything, saleID, mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, _ entity.TicketSaleID, w []entity.DrawWinnerRow, l []entity.DrawLoserRow) error {
			out.called, out.winners, out.losers = true, w, l
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
	})

	t.Run("two entries, one place", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/run-draw "Two entries, one place"
		f := newLotteryFixture(t, afterWindow, 1)
		sale := closedSale(1)
		tt := sale.TicketTypes[0].ID
		f.sales.EXPECT().Get(mock.Anything, sale.ID, afterWindow).Return(sale, nil)
		f.entries.EXPECT().ListUndrawnForTicketType(mock.Anything, tt).Return([]*entity.LotteryEntry{
			lotteryEntry("entry-a", tt, "fan-a", entity.LotteryEntryStateEntered, 1, "pi_a"),
			lotteryEntry("entry-b", tt, "fan-b", entity.LotteryEntryStateEntered, 1, "pi_b"),
		}, nil)
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, mock.Anything).Return(nil).Once()
		f.cardAuthorizations.EXPECT().Release(mock.Anything, mock.Anything).Return(nil).Once()
		var out drawOutcome
		f.recordsOutcome(sale.ID, &out)

		require.NoError(t, f.uc.RunDraw(ctx, sale.ID))

		require.Len(t, out.winners, 1)
		winner := out.winners[0].LotteryEntryID
		fanOf := map[entity.LotteryEntryID]string{"entry-a": "fan-a", "entry-b": "fan-b"}
		loser := entity.LotteryEntryID("entry-a")
		if winner == loser {
			loser = "entry-b"
		}
		assert.ElementsMatch(t, []entity.LotteryEntryDrawnData{
			{LotteryEntryID: string(winner), UserID: fanOf[winner], TicketSaleID: string(sale.ID), TicketTypeID: string(tt), Result: "won", DrawTime: afterWindow},
			{LotteryEntryID: string(loser), UserID: fanOf[loser], TicketSaleID: string(sale.ID), TicketTypeID: string(tt), Result: "lost", DrawTime: afterWindow},
		}, f.drawn)
	})

	t.Run("no result is reported when the draw cannot be recorded", func(t *testing.T) {
		t.Parallel()
		f := newLotteryFixture(t, afterWindow, 1)
		sale := closedSale(1)
		tt := sale.TicketTypes[0].ID
		f.sales.EXPECT().Get(mock.Anything, sale.ID, afterWindow).Return(sale, nil)
		f.entries.EXPECT().ListUndrawnForTicketType(mock.Anything, tt).Return([]*entity.LotteryEntry{
			lotteryEntry("entry-a", tt, "fan-a", entity.LotteryEntryStateEntered, 1, "pi_a"),
		}, nil)
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, "pi_a").Return(nil).Once()
		f.entries.EXPECT().PersistDrawOutcome(mock.Anything, sale.ID, mock.Anything, mock.Anything).Return(apperr.ErrInternal)

		require.ErrorIs(t, f.uc.RunDraw(ctx, sale.ID), apperr.ErrInternal)
		assert.Empty(t, f.drawn)
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
		// The loss is reported as coming from the failed capture.
		for _, r := range f.drawn {
			if r.LotteryEntryID == "closed-card" {
				assert.Equal(t, "lost", r.Result)
				assert.True(t, r.CaptureFailed)
			}
		}
		assert.Len(t, f.drawn, 2)
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
