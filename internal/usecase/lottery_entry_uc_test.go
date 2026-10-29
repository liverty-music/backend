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
	stocks               *entitymocks.MockTicketStockRepository
	entries              *entitymocks.MockLotteryEntryRepository
	orders               *entitymocks.MockOrderRepository
	events               *entitymocks.MockEventRepository
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
		stocks:               entitymocks.NewMockTicketStockRepository(t),
		entries:              entitymocks.NewMockLotteryEntryRepository(t),
		orders:               entitymocks.NewMockOrderRepository(t),
		events:               entitymocks.NewMockEventRepository(t),
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
	f.uc = usecase.NewLotteryEntryUseCaseWithRand(usecase.LotteryEntryDeps{
		SaleRepo:                 f.sales,
		StockRepo:                f.stocks,
		EntryRepo:                f.entries,
		OrderRepo:                f.orders,
		EventRepo:                f.events,
		IdentityVerificationRepo: f.identityVerification,
		UserRepo:                 f.users,
		CardAuthorizations:       f.cardAuthorizations,
		Publisher:                publisher,
		Clock:                    fixedClock(now),
		Logger:                   newTestLogger(t),
	}, func() *mrand.Rand { return mrand.New(mrand.NewPCG(seed, seed+1)) })
	return f
}

// leSeatS is the S席 seat type at 8000 yen; leSeatA the A席 seat type.
var (
	leSeatS = &entity.SeatType{ID: "seat-s", SeriesID: "series-1", Name: "S席", Price: 8000}
	leSeatA = &entity.SeatType{ID: "seat-a", SeriesID: "series-1", Name: "A席", Price: 6000}
)

// leStock returns the S席 stock "stock-1" of event-1 with the given quantity
// and sold count.
func leStock(quantity, sold int) *entity.TicketStock {
	return &entity.TicketStock{ID: "stock-1", EventID: "event-1", SeatTypeID: leSeatS.ID, Quantity: quantity, SoldCount: sold, SeatType: leSeatS}
}

// lotterySale returns the Lottery sale "sale-l" of event-1, open from 1 to 10
// November.
func lotterySale(req entity.VerificationRequirement) *entity.TicketSale {
	return &entity.TicketSale{
		ID: "sale-l", SeriesID: "series-1", Name: "ファンクラブ先行", Method: entity.TicketSaleMethodLottery,
		SaleStartTime: saleTime(11, 1, 10, 0), SaleEndTime: saleTime(11, 10, 10, 0),
		VerificationRequirement: req, EventIDs: []string{"event-1"},
	}
}

// leEvent returns event-1 with a per-account limit of 4.
func leEvent() *entity.Event {
	limit := 4
	return &entity.Event{ID: "event-1", SeriesID: "series-1", PerAccountLimit: &limit}
}

// lotteryEntry returns an entry of the sale with its Pending Order of user for
// count tickets of the stock, made from the card authorization ref.
func lotteryEntry(id entity.LotteryEntryID, stock entity.TicketStockID, user entity.UserID, state entity.LotteryEntryState, count int, ref string) *entity.LotteryEntry {
	return &entity.LotteryEntry{
		ID: id, OrderID: entity.OrderID("order-" + string(id)), TicketSaleID: "sale-l", State: state,
		Order: &entity.Order{
			ID: entity.OrderID("order-" + string(id)), UserID: user, TicketStockID: stock, TicketCount: count,
			CardAuthorizationID: ref, Status: entity.OrderStatusPending, LotteryEntryID: id,
		},
	}
}

var (
	duringWindow = saleTime(11, 5, 12, 0)
	afterWindow  = saleTime(11, 10, 10, 0)
)

// authorization returns a card authorization of a lottery entry of fan in
// sale-l for count tickets of stock-1.
func authorization(id string, status entity.CardAuthorizationStatus, amount int64, brand entity.CardBrand, fan entity.UserID, count int, code *entity.ReferralCode) *entity.CardAuthorization {
	return &entity.CardAuthorization{
		ID: id, Amount: amount, CardBrand: brand, Status: status, Secret: id + "_secret",
		Purchase: entity.CardAuthorizationPurchase{
			Purpose: entity.CardAuthorizationPurposeLotteryEntry, UserID: fan, TicketSaleID: "sale-l",
			TicketStockID: "stock-1", EventID: "event-1", TicketCount: count, ReferralCode: code,
		},
	}
}

// readsSaleAndStock makes the sale and the stock readable, with the event.
func (f *lotteryFixture) readsSaleAndStock(sale *entity.TicketSale, stock *entity.TicketStock) {
	f.sales.EXPECT().Get(mock.Anything, sale.ID).Return(sale, nil).Maybe()
	f.stocks.EXPECT().Get(mock.Anything, stock.ID, mock.Anything).Return(stock, nil).Maybe()
	f.events.EXPECT().Get(mock.Anything, "event-1").Return(leEvent(), nil).Maybe()
}

// holds makes the fan hold what held says for event-1.
func (f *lotteryFixture) holds(held entity.HeldTickets) {
	f.orders.EXPECT().GetHeldForEvent(mock.Anything, entity.UserID("fan-1"), "event-1", (*entity.OrderID)(nil), mock.Anything).Return(held, nil).Maybe()
}

// hasEntries makes the fan's entries in sale-l the given ones, with stock-1 as
// the only stock of event-1.
func (f *lotteryFixture) hasEntries(entries ...*entity.LotteryEntry) {
	f.entries.EXPECT().ListByTicketTypeAndUser(mock.Anything, entity.TicketSaleID("sale-l"), entity.UserID("fan-1")).Return(entries, nil).Maybe()
	f.stocks.EXPECT().ListByEvent(mock.Anything, "event-1", mock.Anything).Return([]*entity.TicketStock{leStock(100, 0)}, nil).Maybe()
}

func TestLotteryEntryUseCase_AuthorizeCard(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const name, phone = "山田 花子", "+819012345678"

	// opens expects the card authorization of fan-1's lottery entry for count
	// tickets of stock-1 with the code, after the personal details are stored.
	opens := func(f *lotteryFixture, count int, code *entity.ReferralCode, amount int64) *mock.Call {
		details := f.users.EXPECT().UpdatePersonalDetails(mock.Anything, "fan-1", name, phone).Return(&entity.User{ID: "fan-1"}, nil).Call
		return f.cardAuthorizations.EXPECT().Create(mock.Anything, entity.CardAuthorizationPurchase{
			Purpose: entity.CardAuthorizationPurposeLotteryEntry, UserID: "fan-1", TicketSaleID: "sale-l",
			TicketStockID: "stock-1", EventID: "event-1", TicketCount: count, ReferralCode: code,
		}, amount).NotBefore(details)
	}

	t.Run("fan authorizes their card", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Fan authorizes their card"
		f := newLotteryFixture(t, duringWindow, 1)
		f.readsSaleAndStock(lotterySale(entity.VerificationRequirementNone), leStock(100, 0))
		f.holds(entity.HeldTickets{})
		f.hasEntries()
		opens(f, 2, nil, 16000).Return(&entity.CardAuthorization{ID: "pi_1", Secret: "pi_1_secret", Amount: 16000, Status: entity.CardAuthorizationStatusPending}, nil)

		got, err := f.uc.AuthorizeCard(ctx, "sale-l", "stock-1", "fan-1", 2, name, phone, nil)

		require.NoError(t, err)
		assert.Equal(t, "pi_1", got.ID)
		assert.Equal(t, "pi_1_secret", got.Secret)
		f.entries.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	})

	t.Run("count changed before card authentication", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Count changed before card authentication"
		f := newLotteryFixture(t, duringWindow, 1)
		f.readsSaleAndStock(lotterySale(entity.VerificationRequirementNone), leStock(100, 0))
		f.holds(entity.HeldTickets{})
		f.hasEntries()
		opens(f, 2, nil, 16000).Return(&entity.CardAuthorization{ID: "pi_1", Amount: 16000}, nil).Once()
		opens(f, 3, nil, 24000).Return(&entity.CardAuthorization{ID: "pi_2", Amount: 24000}, nil).Once()

		first, err := f.uc.AuthorizeCard(ctx, "sale-l", "stock-1", "fan-1", 2, name, phone, nil)
		require.NoError(t, err)
		second, err := f.uc.AuthorizeCard(ctx, "sale-l", "stock-1", "fan-1", 3, name, phone, nil)
		require.NoError(t, err)

		assert.NotEqual(t, first.ID, second.ID)
		assert.Equal(t, int64(24000), second.Amount)
	})

	t.Run("entry from a referral link", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Entry from a referral link"
		f := newLotteryFixture(t, duringWindow, 1)
		f.readsSaleAndStock(lotterySale(entity.VerificationRequirementNone), leStock(100, 0))
		f.holds(entity.HeldTickets{})
		f.hasEntries()
		code := entity.ReferralCode("member-a")
		opens(f, 2, &code, 16000).Return(&entity.CardAuthorization{ID: "pi_1"}, nil)

		_, err := f.uc.AuthorizeCard(ctx, "sale-l", "stock-1", "fan-1", 2, name, phone, &code)

		require.NoError(t, err)
	})

	t.Run("window closed", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Window closed"
		f := newLotteryFixture(t, afterWindow, 1)
		f.readsSaleAndStock(lotterySale(entity.VerificationRequirementNone), leStock(100, 0))

		_, err := f.uc.AuthorizeCard(ctx, "sale-l", "stock-1", "fan-1", 2, name, phone, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		f.cardAuthorizations.AssertNotCalled(t, "Create", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("stock of another event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Stock of another event"
		f := newLotteryFixture(t, duringWindow, 1)
		other := leStock(100, 0)
		other.EventID = "event-2"
		f.readsSaleAndStock(lotterySale(entity.VerificationRequirementNone), other)

		_, err := f.uc.AuthorizeCard(ctx, "sale-l", "stock-1", "fan-1", 2, name, phone, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		f.cardAuthorizations.AssertNotCalled(t, "Create", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("first-come sale", func(t *testing.T) {
		t.Parallel()
		f := newLotteryFixture(t, duringWindow, 1)
		sale := lotterySale(entity.VerificationRequirementNone)
		sale.Method = entity.TicketSaleMethodFirstCome
		f.readsSaleAndStock(sale, leStock(100, 0))

		_, err := f.uc.AuthorizeCard(ctx, "sale-l", "stock-1", "fan-1", 2, name, phone, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("count over the limit", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Count over the limit"
		f := newLotteryFixture(t, duringWindow, 1)
		f.readsSaleAndStock(lotterySale(entity.VerificationRequirementNone), leStock(100, 0))

		_, err := f.uc.AuthorizeCard(ctx, "sale-l", "stock-1", "fan-1", 5, name, phone, nil)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("presale winner reaches the limit", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Presale winner reaches the limit"
		f := newLotteryFixture(t, duringWindow, 1)
		f.readsSaleAndStock(lotterySale(entity.VerificationRequirementNone), leStock(100, 0))
		f.holds(entity.HeldTickets{Count: 4, SeatTypeID: &leSeatS.ID})

		_, err := f.uc.AuthorizeCard(ctx, "sale-l", "stock-1", "fan-1", 1, name, phone, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		f.cardAuthorizations.AssertNotCalled(t, "Create", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("another seat type", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Another seat type"
		f := newLotteryFixture(t, duringWindow, 1)
		f.readsSaleAndStock(lotterySale(entity.VerificationRequirementNone), leStock(100, 0))
		f.holds(entity.HeldTickets{Count: 2, SeatTypeID: &leSeatA.ID})

		_, err := f.uc.AuthorizeCard(ctx, "sale-l", "stock-1", "fan-1", 1, name, phone, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		f.cardAuthorizations.AssertNotCalled(t, "Create", mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("already entered", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Already entered"
		for _, state := range []entity.LotteryEntryState{entity.LotteryEntryStateEntered, entity.LotteryEntryStateWon, entity.LotteryEntryStateLost} {
			f := newLotteryFixture(t, duringWindow, 1)
			f.readsSaleAndStock(lotterySale(entity.VerificationRequirementNone), leStock(100, 0))
			f.holds(entity.HeldTickets{})
			f.hasEntries(lotteryEntry("entry-1", "stock-1", "fan-1", state, 2, "pi_0"))

			_, err := f.uc.AuthorizeCard(ctx, "sale-l", "stock-1", "fan-1", 2, name, phone, nil)

			assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "state %s", state)
			f.cardAuthorizations.AssertNotCalled(t, "Create", mock.Anything, mock.Anything, mock.Anything)
		}
	})

	t.Run("entered for another date of the tour", func(t *testing.T) {
		t.Parallel()
		f := newLotteryFixture(t, duringWindow, 1)
		f.readsSaleAndStock(lotterySale(entity.VerificationRequirementNone), leStock(100, 0))
		f.holds(entity.HeldTickets{})
		f.hasEntries(lotteryEntry("entry-1", "stock-other-date", "fan-1", entity.LotteryEntryStateEntered, 2, "pi_0"))
		opens(f, 2, nil, 16000).Return(&entity.CardAuthorization{ID: "pi_1"}, nil)

		_, err := f.uc.AuthorizeCard(ctx, "sale-l", "stock-1", "fan-1", 2, name, phone, nil)

		require.NoError(t, err)
	})

	t.Run("missing identity details", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Missing identity details"
		f := newLotteryFixture(t, duringWindow, 1)
		f.readsSaleAndStock(lotterySale(entity.VerificationRequirementNone), leStock(100, 0))

		_, err := f.uc.AuthorizeCard(ctx, "sale-l", "stock-1", "fan-1", 2, "", phone, nil)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		_, err = f.uc.AuthorizeCard(ctx, "sale-l", "stock-1", "fan-1", 2, name, "", nil)
		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("payment provider unavailable", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Payment provider unavailable"
		f := newLotteryFixture(t, duringWindow, 1)
		f.readsSaleAndStock(lotterySale(entity.VerificationRequirementNone), leStock(100, 0))
		f.holds(entity.HeldTickets{})
		f.hasEntries()
		opens(f, 2, nil, 16000).Return(nil, apperr.New(codes.Unavailable, "stripe down"))

		_, err := f.uc.AuthorizeCard(ctx, "sale-l", "stock-1", "fan-1", 2, name, phone, nil)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})

	t.Run("unknown sale or stock", func(t *testing.T) {
		t.Parallel()
		f := newLotteryFixture(t, duringWindow, 1)
		f.sales.EXPECT().Get(mock.Anything, entity.TicketSaleID("missing")).Return(nil, apperr.ErrNotFound)
		f.sales.EXPECT().Get(mock.Anything, entity.TicketSaleID("sale-l")).Return(lotterySale(entity.VerificationRequirementNone), nil)
		f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("missing"), duringWindow).Return(nil, apperr.ErrNotFound)

		_, err := f.uc.AuthorizeCard(ctx, "missing", "stock-1", "fan-1", 2, name, phone, nil)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
		_, err = f.uc.AuthorizeCard(ctx, "sale-l", "missing", "fan-1", 2, name, phone, nil)
		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	verified := func(status entity.VerificationStatus) *entity.IdentityVerification {
		return &entity.IdentityVerification{ID: "iv-1", UserID: "fan-1", Status: status}
	}

	t.Run("no identity verification", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "No IdentityVerification"
		f := newLotteryFixture(t, duringWindow, 1)
		f.readsSaleAndStock(lotterySale(entity.VerificationRequirementJPKIOnly), leStock(100, 0))
		f.identityVerification.EXPECT().GetByUserID(mock.Anything, "fan-1").Return(nil, apperr.ErrNotFound)

		_, err := f.uc.AuthorizeCard(ctx, "sale-l", "stock-1", "fan-1", 2, name, phone, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("verification not active", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Verification not active"
		f := newLotteryFixture(t, duringWindow, 1)
		f.readsSaleAndStock(lotterySale(entity.VerificationRequirementVerifiedAny), leStock(100, 0))
		f.identityVerification.EXPECT().GetByUserID(mock.Anything, "fan-1").Return(verified(entity.VerificationStatusNeedsReverification), nil)

		_, err := f.uc.AuthorizeCard(ctx, "sale-l", "stock-1", "fan-1", 2, name, phone, nil)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("active verification", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/authorize-card "Active verification"
		f := newLotteryFixture(t, duringWindow, 1)
		f.readsSaleAndStock(lotterySale(entity.VerificationRequirementJPKIOnly), leStock(100, 0))
		f.identityVerification.EXPECT().GetByUserID(mock.Anything, "fan-1").Return(verified(entity.VerificationStatusActive), nil)
		f.holds(entity.HeldTickets{})
		f.hasEntries()
		opens(f, 2, nil, 16000).Return(&entity.CardAuthorization{ID: "pi_1"}, nil)

		_, err := f.uc.AuthorizeCard(ctx, "sale-l", "stock-1", "fan-1", 2, name, phone, nil)

		require.NoError(t, err)
	})
}

func TestLotteryEntryUseCase_Confirm(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fan := entity.UserID("fan-1")

	// authorized makes pi_1 an Authorized card authorization of fan-1 for 2
	// tickets of stock-1, for amount on a card of brand, with no entry yet.
	authorized := func(f *lotteryFixture, amount int64, brand entity.CardBrand) {
		f.cardAuthorizations.EXPECT().Get(mock.Anything, "pi_1").
			Return(authorization("pi_1", entity.CardAuthorizationStatusAuthorized, amount, brand, fan, 2, nil), nil)
		f.entries.EXPECT().GetByCardAuthorizationID(mock.Anything, "pi_1").Return(nil, apperr.ErrNotFound)
	}
	// creates expects the entry of sale-l and its Pending Order for fan-1,
	// 2 tickets of stock-1, made from pi_1.
	creates := func(f *lotteryFixture) *entitymocks.MockLotteryEntryRepository_Create_Call {
		return f.entries.EXPECT().Create(mock.Anything, mock.MatchedBy(func(e *entity.LotteryEntry) bool {
			o := e.Order
			return e.TicketSaleID == "sale-l" && e.State == entity.LotteryEntryStateEntered &&
				o.UserID == fan && o.TicketStockID == "stock-1" && o.TicketCount == 2 && o.Amount == 16000 &&
				o.CardAuthorizationID == "pi_1" && o.Status == entity.OrderStatusPending && o.LotteryEntryID == e.ID
		}))
	}
	echo := func(_ context.Context, e *entity.LotteryEntry) (*entity.LotteryEntry, error) { return e, nil }

	t.Run("fan confirms", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/confirm "Fan confirms"
		f := newLotteryFixture(t, duringWindow, 1)
		f.readsSaleAndStock(lotterySale(entity.VerificationRequirementNone), leStock(100, 0))
		authorized(f, 16000, "visa")
		f.holds(entity.HeldTickets{})
		creates(f).RunAndReturn(echo)

		got, err := f.uc.Confirm(ctx, "pi_1", &fan)

		require.NoError(t, err)
		assert.Equal(t, entity.LotteryEntryStateEntered, got.State)
		assert.Equal(t, entity.OrderStatusPending, got.Order.Status)
		f.cardAuthorizations.AssertNotCalled(t, "Capture", mock.Anything, mock.Anything)
	})

	t.Run("confirmed twice", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/confirm "Confirmed twice"
		f := newLotteryFixture(t, duringWindow, 1)
		existing := lotteryEntry("entry-1", "stock-1", fan, entity.LotteryEntryStateEntered, 2, "pi_1")
		f.cardAuthorizations.EXPECT().Get(mock.Anything, "pi_1").
			Return(authorization("pi_1", entity.CardAuthorizationStatusAuthorized, 16000, "visa", fan, 2, nil), nil)
		f.entries.EXPECT().GetByCardAuthorizationID(mock.Anything, "pi_1").Return(existing, nil)

		got, err := f.uc.Confirm(ctx, "pi_1", &fan)

		require.NoError(t, err)
		assert.Same(t, existing, got)
		f.entries.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	})

	t.Run("fan and notice at the same time", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/confirm "Fan and notice at the same time"
		// Both calls pass the lookup before either stores; LotteryEntry.Create
		// stores one entry and returns it to both.
		f := newLotteryFixture(t, duringWindow, 1)
		f.readsSaleAndStock(lotterySale(entity.VerificationRequirementNone), leStock(100, 0))
		f.cardAuthorizations.EXPECT().Get(mock.Anything, "pi_1").
			Return(authorization("pi_1", entity.CardAuthorizationStatusAuthorized, 16000, "visa", fan, 2, nil), nil).Twice()
		f.entries.EXPECT().GetByCardAuthorizationID(mock.Anything, "pi_1").Return(nil, apperr.ErrNotFound).Twice()
		f.holds(entity.HeldTickets{})
		stored := lotteryEntry("entry-1", "stock-1", fan, entity.LotteryEntryStateEntered, 2, "pi_1")
		creates(f).Return(stored, nil).Twice()

		fromFan, err := f.uc.Confirm(ctx, "pi_1", &fan)
		require.NoError(t, err)
		fromNotice, err := f.uc.Confirm(ctx, "pi_1", nil)
		require.NoError(t, err)

		assert.Equal(t, fromFan.ID, fromNotice.ID)
		f.cardAuthorizations.AssertNotCalled(t, "Release", mock.Anything, mock.Anything)
	})

	t.Run("card authentication not completed", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/confirm "Card authentication not completed"
		for _, status := range []entity.CardAuthorizationStatus{
			entity.CardAuthorizationStatusPending, entity.CardAuthorizationStatusCaptured, entity.CardAuthorizationStatusReleased,
		} {
			f := newLotteryFixture(t, duringWindow, 1)
			f.cardAuthorizations.EXPECT().Get(mock.Anything, "pi_1").Return(authorization("pi_1", status, 16000, "visa", fan, 2, nil), nil)
			f.entries.EXPECT().GetByCardAuthorizationID(mock.Anything, "pi_1").Return(nil, apperr.ErrNotFound)

			got, err := f.uc.Confirm(ctx, "pi_1", &fan)

			assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
			assert.Nil(t, got)
			f.cardAuthorizations.AssertNotCalled(t, "Release", mock.Anything, mock.Anything)
		}
	})

	rejections := []struct {
		name    string
		setup   func(f *lotteryFixture)
		wantErr error
	}{
		{
			// @spec components/usecase/lottery-entry/confirm "American Express card"
			name: "American Express card",
			setup: func(f *lotteryFixture) {
				f.readsSaleAndStock(lotterySale(entity.VerificationRequirementNone), leStock(100, 0))
				authorized(f, 16000, entity.CardBrandAmex)
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			// @spec components/usecase/lottery-entry/confirm "Amount differs"
			name: "amount differs",
			setup: func(f *lotteryFixture) {
				f.readsSaleAndStock(lotterySale(entity.VerificationRequirementNone), leStock(100, 0))
				authorized(f, 8000, "visa")
			},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			// @spec components/usecase/lottery-entry/confirm "No longer fits"
			name: "no longer fits",
			setup: func(f *lotteryFixture) {
				f.readsSaleAndStock(lotterySale(entity.VerificationRequirementNone), leStock(100, 0))
				authorized(f, 16000, "visa")
				f.holds(entity.HeldTickets{Count: 4, SeatTypeID: &leSeatS.ID})
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			// @spec components/usecase/lottery-entry/confirm "Second entry in the sale"
			name: "second entry in the sale",
			setup: func(f *lotteryFixture) {
				f.readsSaleAndStock(lotterySale(entity.VerificationRequirementNone), leStock(100, 0))
				authorized(f, 16000, "visa")
				f.holds(entity.HeldTickets{})
				creates(f).Return(nil, apperr.New(codes.AlreadyExists, "already entered"))
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			// @spec components/usecase/lottery-entry/confirm "After the end time"
			name: "after the end time",
			setup: func(f *lotteryFixture) {
				f.readsSaleAndStock(lotterySale(entity.VerificationRequirementNone), leStock(100, 0))
				authorized(f, 16000, "visa")
				f.holds(entity.HeldTickets{})
				creates(f).Return(nil, apperr.New(codes.FailedPrecondition, "the sale has ended"))
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
		{
			// @spec components/usecase/lottery-entry/confirm "TicketStock gone"
			name: "ticket stock gone",
			setup: func(f *lotteryFixture) {
				authorized(f, 16000, "visa")
				f.stocks.EXPECT().Get(mock.Anything, entity.TicketStockID("stock-1"), mock.Anything).Return(nil, apperr.ErrNotFound)
			},
			wantErr: apperr.ErrFailedPrecondition,
		},
	}
	for _, tt := range rejections {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newLotteryFixture(t, duringWindow, 1)
			tt.setup(f)
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
		f.readsSaleAndStock(lotterySale(entity.VerificationRequirementNone), leStock(100, 0))
		authorized(f, 16000, entity.CardBrandAmex)
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
		f.readsSaleAndStock(lotterySale(entity.VerificationRequirementNone), leStock(100, 0))
		authorized(f, 16000, "visa")
		f.holds(entity.HeldTickets{})
		creates(f).Return(nil, apperr.New(codes.Unavailable, "lock timeout"))

		_, err := f.uc.Confirm(ctx, "pi_1", &fan)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		f.cardAuthorizations.AssertNotCalled(t, "Release", mock.Anything, mock.Anything)
	})

	t.Run("someone else's card authorization", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/confirm "Someone else's card authorization"
		f := newLotteryFixture(t, duringWindow, 1)
		f.cardAuthorizations.EXPECT().Get(mock.Anything, "pi_1").
			Return(authorization("pi_1", entity.CardAuthorizationStatusAuthorized, 16000, "visa", "fan-2", 2, nil), nil)

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
		f.readsSaleAndStock(lotterySale(entity.VerificationRequirementNone), leStock(100, 0))
		authorized(f, 16000, "visa")
		f.holds(entity.HeldTickets{})
		creates(f).RunAndReturn(echo)

		got, err := f.uc.Confirm(ctx, "pi_1", nil)

		require.NoError(t, err)
		assert.Equal(t, "pi_1", got.Order.CardAuthorizationID)
		assert.Equal(t, fan, got.Order.UserID)
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

	for _, tt := range []struct {
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
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newLotteryFixture(t, duringWindow, 1)
			entry := lotteryEntry("entry-1", "stock-1", "fan-1", tt.state, 2, "pi_1")
			f.sales.EXPECT().Get(mock.Anything, entity.TicketSaleID("sale-l")).Return(lotterySale(entity.VerificationRequirementNone), nil)
			f.entries.EXPECT().ListByTicketTypeAndUser(mock.Anything, entity.TicketSaleID("sale-l"), entity.UserID("fan-1")).Return([]*entity.LotteryEntry{entry}, nil)

			got, err := f.uc.ListByTicketType(ctx, "sale-l", "fan-1")

			require.NoError(t, err)
			require.Len(t, got, 1)
			assert.Equal(t, tt.state, got[0].State)
			assert.Equal(t, entity.TicketStockID("stock-1"), got[0].Order.TicketStockID)
			assert.Equal(t, 2, got[0].Order.TicketCount)
		})
	}

	t.Run("never entered", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/list-by-ticket-type "Never entered"
		f := newLotteryFixture(t, duringWindow, 1)
		f.sales.EXPECT().Get(mock.Anything, entity.TicketSaleID("sale-l")).Return(lotterySale(entity.VerificationRequirementNone), nil)
		f.entries.EXPECT().ListByTicketTypeAndUser(mock.Anything, entity.TicketSaleID("sale-l"), entity.UserID("fan-1")).Return([]*entity.LotteryEntry{}, nil)

		got, err := f.uc.ListByTicketType(ctx, "sale-l", "fan-1")

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("unknown sale", func(t *testing.T) {
		t.Parallel()
		f := newLotteryFixture(t, duringWindow, 1)
		f.sales.EXPECT().Get(mock.Anything, entity.TicketSaleID("missing")).Return(nil, apperr.ErrNotFound)

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

// drawStock is a stock of a closed sale's event with its remaining tickets and
// its Entered entries.
type drawStock struct {
	stock   *entity.TicketStock
	entries []*entity.LotteryEntry
}

// closedSaleWith returns a closed Lottery sale of event-1 whose stocks are the
// given ones, wiring the reads RunDraw makes.
func (f *lotteryFixture) closedSaleWith(id entity.TicketSaleID, stocks ...drawStock) *entity.TicketSale {
	sale := lotterySale(entity.VerificationRequirementNone)
	sale.ID = id
	listed := make([]*entity.TicketStock, 0, len(stocks))
	for _, s := range stocks {
		listed = append(listed, s.stock)
		f.entries.EXPECT().ListUndrawnForTicketType(mock.Anything, id, s.stock.ID).Return(s.entries, nil)
		if len(s.entries) > 0 {
			f.stocks.EXPECT().Get(mock.Anything, s.stock.ID, mock.Anything).Return(s.stock, nil)
		}
	}
	f.sales.EXPECT().Get(mock.Anything, id).Return(sale, nil)
	f.stocks.EXPECT().ListByEvent(mock.Anything, "event-1", mock.Anything).Return(listed, nil)
	return sale
}

// stockWith returns a stock of the given id with quantity tickets of which
// sold are sold.
func stockWith(id entity.TicketStockID, quantity, sold int) *entity.TicketStock {
	return &entity.TicketStock{ID: id, EventID: "event-1", SeatTypeID: leSeatS.ID, Quantity: quantity, SoldCount: sold, SeatType: leSeatS}
}

func TestLotteryEntryUseCase_RunDraw(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("winners charged, losers released", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/run-draw "Winners charged, losers released"
		f := newLotteryFixture(t, afterWindow, 1)
		sale := f.closedSaleWith("sale-l", drawStock{stockWith("stock-1", 4, 0), []*entity.LotteryEntry{
			lotteryEntry("two-a", "stock-1", "fan-1", entity.LotteryEntryStateEntered, 2, "pi_a"),
			lotteryEntry("two-b", "stock-1", "fan-2", entity.LotteryEntryStateEntered, 2, "pi_b"),
			lotteryEntry("three", "stock-1", "fan-3", entity.LotteryEntryStateEntered, 3, "pi_c"),
		}})
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, mock.Anything).Return(nil)
		f.cardAuthorizations.EXPECT().Release(mock.Anything, mock.Anything).Return(nil)
		var out drawOutcome
		f.recordsOutcome(sale.ID, &out)

		require.NoError(t, f.uc.RunDraw(ctx, sale.ID))

		won := 0
		for _, w := range out.winners {
			won += map[entity.LotteryEntryID]int{"two-a": 2, "two-b": 2, "three": 3}[w.LotteryEntryID]
		}
		assert.LessOrEqual(t, won, 4, "never more than the stock's remaining tickets")
		assert.Equal(t, 3, len(out.winners)+len(out.losers))
		for _, w := range out.winners {
			f.cardAuthorizations.AssertCalled(t, "Capture", mock.Anything, w.CardAuthorizationID)
		}
		for _, l := range out.losers {
			f.cardAuthorizations.AssertCalled(t, "Release", mock.Anything, l.CardAuthorizationID)
		}
	})

	t.Run("each stock is drawn against its own remaining tickets", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/run-draw "Each stock is drawn against its own remaining tickets"
		f := newLotteryFixture(t, afterWindow, 7)
		var aEntries []*entity.LotteryEntry
		for i := range 6 {
			aEntries = append(aEntries, lotteryEntry(entity.LotteryEntryID("a-"+string(rune('0'+i))), "stock-a", entity.UserID("fan-a"+string(rune('0'+i))), entity.LotteryEntryStateEntered, 2, "pi_a"+string(rune('0'+i))))
		}
		var sEntries []*entity.LotteryEntry
		for i := range 4 {
			sEntries = append(sEntries, lotteryEntry(entity.LotteryEntryID("s-"+string(rune('0'+i))), "stock-s", entity.UserID("fan-s"+string(rune('0'+i))), entity.LotteryEntryStateEntered, 4, "pi_s"+string(rune('0'+i))))
		}
		sale := f.closedSaleWith("sale-l",
			drawStock{stockWith("stock-s", 100, 0), sEntries},
			drawStock{stockWith("stock-a", 4, 0), aEntries},
		)
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, mock.Anything).Return(nil)
		f.cardAuthorizations.EXPECT().Release(mock.Anything, mock.Anything).Return(nil)
		var out drawOutcome
		f.recordsOutcome(sale.ID, &out)

		require.NoError(t, f.uc.RunDraw(ctx, sale.ID))

		sWon, aWon := 0, 0
		for _, w := range out.winners {
			if w.LotteryEntryID[0] == 's' {
				sWon++
			} else {
				aWon++
			}
		}
		assert.Equal(t, 4, sWon, "every S席 entry wins")
		assert.LessOrEqual(t, aWon*2, 4, "the A席 winners get at most 4 tickets")
	})

	t.Run("earlier sales already took tickets", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/run-draw "Earlier sales already took tickets"
		f := newLotteryFixture(t, afterWindow, 3)
		var entries []*entity.LotteryEntry
		for i := range 10 {
			entries = append(entries, lotteryEntry(entity.LotteryEntryID("e-"+string(rune('a'+i))), "stock-1", entity.UserID("fan-"+string(rune('a'+i))), entity.LotteryEntryStateEntered, 5, "pi_"+string(rune('a'+i))))
		}
		sale := f.closedSaleWith("sale-l", drawStock{stockWith("stock-1", 150, 120), entries})
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, mock.Anything).Return(nil)
		f.cardAuthorizations.EXPECT().Release(mock.Anything, mock.Anything).Return(nil)
		var out drawOutcome
		f.recordsOutcome(sale.ID, &out)

		require.NoError(t, f.uc.RunDraw(ctx, sale.ID))

		assert.LessOrEqual(t, len(out.winners)*5, 30)
		assert.Equal(t, 6, len(out.winners), "30 remaining tickets admit six 5-ticket entries")
	})

	t.Run("losers in draw order", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/run-draw "Losers in draw order"
		f := newLotteryFixture(t, afterWindow, 5)
		sale := f.closedSaleWith("sale-l", drawStock{stockWith("stock-1", 2, 0), []*entity.LotteryEntry{
			lotteryEntry("e-1", "stock-1", "fan-1", entity.LotteryEntryStateEntered, 2, "pi_1"),
			lotteryEntry("e-2", "stock-1", "fan-2", entity.LotteryEntryStateEntered, 2, "pi_2"),
			lotteryEntry("e-3", "stock-1", "fan-3", entity.LotteryEntryStateEntered, 2, "pi_3"),
		}})
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, mock.Anything).Return(nil)
		f.cardAuthorizations.EXPECT().Release(mock.Anything, mock.Anything).Return(nil)
		var out drawOutcome
		f.recordsOutcome(sale.ID, &out)

		require.NoError(t, f.uc.RunDraw(ctx, sale.ID))

		require.Len(t, out.losers, 2)
		positions := map[int]bool{out.winners[0].DrawPosition: true}
		for _, l := range out.losers {
			assert.False(t, positions[l.DrawPosition], "each entry has its own position")
			positions[l.DrawPosition] = true
		}
	})

	t.Run("no entries", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/run-draw "No entries"
		f := newLotteryFixture(t, afterWindow, 1)
		sale := f.closedSaleWith("sale-l", drawStock{stockWith("stock-1", 100, 0), nil})

		require.NoError(t, f.uc.RunDraw(ctx, sale.ID))

		f.entries.AssertNotCalled(t, "PersistDrawOutcome", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		f.cardAuthorizations.AssertNotCalled(t, "Capture", mock.Anything, mock.Anything)
		f.cardAuthorizations.AssertNotCalled(t, "Release", mock.Anything, mock.Anything)
	})

	t.Run("two entries, one place", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/run-draw "Two entries, one place"
		f := newLotteryFixture(t, afterWindow, 1)
		sale := f.closedSaleWith("sale-l", drawStock{stockWith("stock-1", 2, 0), []*entity.LotteryEntry{
			lotteryEntry("e-1", "stock-1", "fan-1", entity.LotteryEntryStateEntered, 2, "pi_1"),
			lotteryEntry("e-2", "stock-1", "fan-2", entity.LotteryEntryStateEntered, 2, "pi_2"),
		}})
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, mock.Anything).Return(nil).Once()
		f.cardAuthorizations.EXPECT().Release(mock.Anything, mock.Anything).Return(nil).Once()
		var out drawOutcome
		f.recordsOutcome(sale.ID, &out)

		require.NoError(t, f.uc.RunDraw(ctx, sale.ID))

		require.Len(t, f.drawn, 2)
		results := map[string]int{}
		for _, r := range f.drawn {
			results[r.Result]++
			assert.Equal(t, "sale-l", r.TicketSaleID)
			assert.Equal(t, "stock-1", r.TicketStockID)
			assert.False(t, r.CaptureFailed)
		}
		assert.Equal(t, map[string]int{"won": 1, "lost": 1}, results)
	})

	t.Run("winner's card was closed", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/run-draw "Winner's card was closed"
		f := newLotteryFixture(t, afterWindow, 1)
		sale := f.closedSaleWith("sale-l", drawStock{stockWith("stock-1", 4, 0), []*entity.LotteryEntry{
			lotteryEntry("closed-card", "stock-1", "fan-1", entity.LotteryEntryStateEntered, 2, "pi_closed"),
			lotteryEntry("fine", "stock-1", "fan-2", entity.LotteryEntryStateEntered, 2, "pi_fine"),
		}})
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, "pi_closed").Return(apperr.New(codes.FailedPrecondition, "card closed"))
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, "pi_fine").Return(nil)
		f.cardAuthorizations.EXPECT().Release(mock.Anything, "pi_closed").Return(nil).Once()
		var out drawOutcome
		f.recordsOutcome(sale.ID, &out)

		require.NoError(t, f.uc.RunDraw(ctx, sale.ID))

		assert.Equal(t, []entity.LotteryEntryID{"fine"}, out.winnerIDs(), "no loser is promoted")
		require.Len(t, out.losers, 1)
		assert.Equal(t, entity.LotteryEntryID("closed-card"), out.losers[0].LotteryEntryID)
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
		sale := f.closedSaleWith("sale-l", drawStock{stockWith("stock-1", 2, 0), []*entity.LotteryEntry{
			lotteryEntry("e-1", "stock-1", "fan-1", entity.LotteryEntryStateEntered, 2, "pi_1"),
			lotteryEntry("e-2", "stock-1", "fan-2", entity.LotteryEntryStateEntered, 2, "pi_2"),
			lotteryEntry("e-3", "stock-1", "fan-3", entity.LotteryEntryStateEntered, 2, "pi_3"),
		}})
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
		// The use case has no Ticket writer and does not touch Order issuance,
		// so the draw cannot issue anything; the Won entry is only recorded.
		f := newLotteryFixture(t, afterWindow, 1)
		sale := f.closedSaleWith("sale-l", drawStock{stockWith("stock-1", 4, 0), []*entity.LotteryEntry{
			lotteryEntry("e-1", "stock-1", "fan-1", entity.LotteryEntryStateEntered, 2, "pi_1"),
		}})
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, "pi_1").Return(nil)
		var out drawOutcome
		f.recordsOutcome(sale.ID, &out)

		require.NoError(t, f.uc.RunDraw(ctx, sale.ID))

		assert.Equal(t, []entity.LotteryEntryID{"e-1"}, out.winnerIDs())
	})

	t.Run("unknown sale", func(t *testing.T) {
		t.Parallel()
		f := newLotteryFixture(t, afterWindow, 1)
		f.sales.EXPECT().Get(mock.Anything, entity.TicketSaleID("missing")).Return(nil, apperr.ErrNotFound)

		assert.ErrorIs(t, f.uc.RunDraw(ctx, "missing"), apperr.ErrNotFound)
	})
}

func TestLotteryEntryUseCase_DrawDueSales(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	entered := func(id entity.LotteryEntryID) drawStock {
		return drawStock{stockWith("stock-1", 4, 0), []*entity.LotteryEntry{
			lotteryEntry(id, "stock-1", "fan-1", entity.LotteryEntryStateEntered, 2, "pi_"+string(id)),
		}}
	}

	t.Run("window closes", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/lottery-entry/draw-due-sales "Window closes"
		f := newLotteryFixture(t, afterWindow, 1)
		sale := f.closedSaleWith("sale-l", entered("e-1"))
		f.sales.EXPECT().ListDueForDraw(mock.Anything, afterWindow).Return([]*entity.TicketSale{sale}, nil)
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, "pi_e-1").Return(nil)
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
		// A drawn sale has no Entered entry left, so it is no longer listed due
		// and a later run draws nothing.
		f := newLotteryFixture(t, afterWindow, 1)
		sale := f.closedSaleWith("sale-l", entered("e-1"))
		f.sales.EXPECT().ListDueForDraw(mock.Anything, afterWindow).Return([]*entity.TicketSale{sale}, nil).Once()
		f.sales.EXPECT().ListDueForDraw(mock.Anything, afterWindow).Return([]*entity.TicketSale{}, nil).Once()
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, "pi_e-1").Return(nil)
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
		failing := lotterySale(entity.VerificationRequirementNone)
		failing.ID = "sale-failing"
		other := f.closedSaleWith("sale-other", entered("e-1"))
		f.sales.EXPECT().ListDueForDraw(mock.Anything, afterWindow).Return([]*entity.TicketSale{failing, other}, nil).Once()
		f.sales.EXPECT().Get(mock.Anything, failing.ID).Return(nil, errors.New("connection reset")).Once()
		f.cardAuthorizations.EXPECT().Capture(mock.Anything, "pi_e-1").Return(nil)
		var otherOut drawOutcome
		f.recordsOutcome(other.ID, &otherOut)

		require.NoError(t, f.uc.DrawDueSales(ctx))
		assert.True(t, otherOut.called, "the other due sale is still drawn")
	})

	t.Run("listing fails", func(t *testing.T) {
		t.Parallel()
		f := newLotteryFixture(t, afterWindow, 1)
		listErr := apperr.New(codes.Internal, "db down")
		f.sales.EXPECT().ListDueForDraw(mock.Anything, afterWindow).Return(nil, listErr)

		assert.Equal(t, listErr, f.uc.DrawDueSales(ctx))
	})
}
