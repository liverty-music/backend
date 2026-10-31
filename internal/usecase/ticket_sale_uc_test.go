package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

var tsJST = time.FixedZone("JST", 9*60*60)

// saleTime returns the time on the given day of 2026 in JST.
func saleTime(month time.Month, day, hour, minute int) time.Time {
	return time.Date(2026, month, day, hour, minute, 0, 0, tsJST)
}

// ticketSaleFixture holds the mocks of a TicketSaleUseCase under test. Every
// event starts at 19:00 on 20 November unless starts says otherwise.
type ticketSaleFixture struct {
	sales      *entitymocks.MockTicketSaleRepository
	stocks     *entitymocks.MockTicketStockRepository
	eventRepo  *entitymocks.MockEventRepository
	entries    *entitymocks.MockLotteryEntryRepository
	orders     *entitymocks.MockOrderRepository
	series     *entitymocks.MockSeriesRepository
	organizers *entitymocks.MockOrganizerRepository
	events     *ucmocks.MockEventPublishStatePort
	starts     map[string]*time.Time
	uc         usecase.TicketSaleUseCase
}

func newTicketSaleFixture(t *testing.T, now time.Time) *ticketSaleFixture {
	t.Helper()
	f := &ticketSaleFixture{
		sales:      entitymocks.NewMockTicketSaleRepository(t),
		stocks:     entitymocks.NewMockTicketStockRepository(t),
		eventRepo:  entitymocks.NewMockEventRepository(t),
		entries:    entitymocks.NewMockLotteryEntryRepository(t),
		orders:     entitymocks.NewMockOrderRepository(t),
		series:     entitymocks.NewMockSeriesRepository(t),
		organizers: entitymocks.NewMockOrganizerRepository(t),
		events:     ucmocks.NewMockEventPublishStatePort(t),
		starts:     map[string]*time.Time{},
	}
	eventStart := &stubEventStartTimeRepo{getFn: func(_ context.Context, id string) (*time.Time, error) {
		if s, ok := f.starts[id]; ok {
			return s, nil
		}
		s := saleTime(11, 20, 19, 0)
		return &s, nil
	}}
	f.uc = usecase.NewTicketSaleUseCase(f.sales, f.stocks, f.entries, f.orders, f.series, f.organizers, f.eventRepo, f.events, eventStart, fixedClock(now))
	return f
}

// ownSeries makes series-1 org-1's, with the given events, all published and
// stocked with S席.
func (f *ticketSaleFixture) ownSeries(eventIDs ...string) {
	f.seriesOwnedBy("org-1", eventIDs...)
	for _, id := range eventIDs {
		f.events.EXPECT().IsEventPublished(mock.Anything, id).Return(true, nil).Maybe()
		f.stocks.EXPECT().ListByEvent(mock.Anything, id, mock.Anything).Return([]*entity.TicketStock{tsStock(id, 150, 0, 0)}, nil).Maybe()
	}
}

// tsSeatS is the S席 seat type of series-1 at 8000 yen.
var tsSeatS = &entity.SeatType{ID: "seat-s", SeriesID: "series-1", Name: "S席", Price: 8000}

// tsStock returns the S席 stock of the event with the given counts.
func tsStock(eventID string, quantity, sold, held int) *entity.TicketStock {
	return &entity.TicketStock{
		ID: entity.TicketStockID("stock-" + eventID), EventID: eventID, SeatTypeID: tsSeatS.ID,
		Quantity: quantity, SoldCount: sold, HeldCount: held, SeatType: tsSeatS,
	}
}

// seriesOwnedBy makes series-1 the given Organizer's, with the given events.
func (f *ticketSaleFixture) seriesOwnedBy(organizerID string, eventIDs ...string) {
	events := make([]*entity.Event, 0, len(eventIDs))
	for _, id := range eventIDs {
		events = append(events, &entity.Event{ID: id, SeriesID: "series-1"})
	}
	f.series.EXPECT().GetAuthored(mock.Anything, "series-1").
		Return(&entity.Series{ID: "series-1", OrganizerID: &organizerID}, concertsOf(events, nil), nil).Maybe()
}

// organizer makes org-1 have complete business details, or none.
func (f *ticketSaleFixture) organizer(complete bool) {
	org := &entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusActive}
	if complete {
		org.BusinessDetails = &entity.BusinessDetails{
			LegalName: "株式会社リバティ", RepresentativeName: "代表 太郎", BusinessAddress: "東京都渋谷区1-2-3",
			BusinessPhoneNumber: "+81312345678", ContactEmail: "contact@example.com",
		}
	}
	f.organizers.EXPECT().Get(mock.Anything, "org-1").Return(org, nil).Maybe()
}

// otherSales makes the event's existing sales the given ones.
func (f *ticketSaleFixture) otherSales(eventID string, sales ...*entity.TicketSale) {
	f.sales.EXPECT().ListByEvent(mock.Anything, eventID).Return(sales, nil).Maybe()
}

// stores makes TicketSale.Create return the sale it is given.
func (f *ticketSaleFixture) stores() {
	f.sales.EXPECT().Create(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, s *entity.TicketSale) (*entity.TicketSale, error) { return s, nil })
}

// existingSale returns a sale of series-1 naming the events.
func existingSale(name string, method entity.TicketSaleMethod, start, end time.Time, eventIDs ...string) *entity.TicketSale {
	return entity.NewTicketSale("series-1", name, method, start, end, eventIDs...)
}

func lotteryInput() usecase.CreateTicketSaleInput {
	end := saleTime(11, 11, 10, 0)
	return usecase.CreateTicketSaleInput{
		SeriesID:  "series-1",
		Name:      "ファンクラブ先行",
		Method:    entity.TicketSaleMethodLottery,
		SaleStart: saleTime(11, 1, 10, 0),
		SaleEnd:   end,
		EventIDs:  []string{"event-1"},
	}
}

func firstComeInput() usecase.CreateTicketSaleInput {
	return usecase.CreateTicketSaleInput{
		SeriesID:  "series-1",
		Name:      "一般発売",
		Method:    entity.TicketSaleMethodFirstCome,
		SaleStart: saleTime(11, 1, 10, 0),
		SaleEnd:   saleTime(11, 20, 19, 0),
		EventIDs:  []string{"event-1"},
	}
}

// TicketSaleUseCase starts no workflow, so creating an Organizer's sale
// never requests an announcement: only discovery does.
//
// @spec components/usecase/ticket-sale/announce-discovered-sale "Organizer's sale created"
func TestTicketSaleUseCase_Create(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := saleTime(10, 20, 12, 0)

	t.Run("another organizer's series", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/create "Another Organizer's series"
		f := newTicketSaleFixture(t, now)
		f.seriesOwnedBy("org-2", "event-1")

		_, err := f.uc.Create(ctx, "org-1", lotteryInput())

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("unknown series is not revealed", func(t *testing.T) {
		t.Parallel()
		f := newTicketSaleFixture(t, now)
		f.series.EXPECT().GetAuthored(mock.Anything, "series-1").Return(nil, nil, apperr.ErrNotFound)

		_, err := f.uc.Create(ctx, "org-1", lotteryInput())

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("event of another series", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/create "Event of another series"
		f := newTicketSaleFixture(t, now)
		f.ownSeries("event-1")
		in := lotteryInput()
		in.EventIDs = append(in.EventIDs, "event-of-other-series")

		_, err := f.uc.Create(ctx, "org-1", in)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("event not published", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/create "Event not published"
		f := newTicketSaleFixture(t, now)
		f.seriesOwnedBy("org-1", "event-1")
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(false, nil)

		_, err := f.uc.Create(ctx, "org-1", lotteryInput())

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("start time not yet announced", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/create "Start time not yet announced"
		f := newTicketSaleFixture(t, now)
		f.ownSeries("event-1")
		f.starts["event-1"] = nil

		_, err := f.uc.Create(ctx, "org-1", lotteryInput())

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("sale ends after the event starts", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/create "Sale ends after the event starts"
		f := newTicketSaleFixture(t, now)
		f.ownSeries("event-1")
		in := lotteryInput()
		in.SaleStart = saleTime(11, 10, 20, 0)
		end := saleTime(11, 20, 20, 0)
		in.SaleEnd = end

		_, err := f.uc.Create(ctx, "org-1", in)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("lottery ending at the event start", func(t *testing.T) {
		t.Parallel()
		f := newTicketSaleFixture(t, now)
		f.ownSeries("event-1")
		in := lotteryInput()
		in.SaleStart = saleTime(11, 10, 19, 0)
		end := saleTime(11, 20, 19, 0)
		in.SaleEnd = end

		_, err := f.uc.Create(ctx, "org-1", in)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "a lottery must end before the event starts")
	})

	t.Run("first-come sale ends after the show starts", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/create "First-come sale ends after the show starts"
		f := newTicketSaleFixture(t, now)
		f.ownSeries("event-1")
		in := firstComeInput()
		end := saleTime(11, 20, 19, 30)
		in.SaleEnd = end

		_, err := f.uc.Create(ctx, "org-1", in)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("doors-open time not announced", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/create "Doors-open time not announced"
		f := newTicketSaleFixture(t, now)
		f.ownSeries("event-1")
		f.organizer(true)
		f.otherSales("event-1")
		f.stores()

		view, err := f.uc.Create(ctx, "org-1", lotteryInput())

		require.NoError(t, err)
		assert.Equal(t, "ファンクラブ先行", view.Name)
	})

	t.Run("invalid configuration", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/create "Invalid configuration"
		fifteenDays := saleTime(11, 16, 10, 0)
		cases := map[string]func(*usecase.CreateTicketSaleInput){
			"window of 15 days": func(in *usecase.CreateTicketSaleInput) { in.SaleEnd = fifteenDays },
			"empty name":        func(in *usecase.CreateTicketSaleInput) { in.Name = "" },
			"no event":          func(in *usecase.CreateTicketSaleInput) { in.EventIDs = nil },
			"event named twice": func(in *usecase.CreateTicketSaleInput) { in.EventIDs = []string{"event-1", "event-1"} },
		}
		for name, change := range cases {
			t.Run(name, func(t *testing.T) {
				t.Parallel()
				f := newTicketSaleFixture(t, now)
				in := lotteryInput()
				change(&in)

				_, err := f.uc.Create(ctx, "org-1", in)

				assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
			})
		}
	})

	t.Run("business details missing", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/create "Business details missing"
		f := newTicketSaleFixture(t, now)
		f.ownSeries("event-1")
		f.organizer(false)

		_, err := f.uc.Create(ctx, "org-1", firstComeInput())

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("lottery without business details", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/create "Lottery without business details"
		f := newTicketSaleFixture(t, now)
		f.ownSeries("event-1")
		f.organizer(false)

		_, err := f.uc.Create(ctx, "org-1", lotteryInput())

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("general sale after the presale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/create "General sale after the presale"
		f := newTicketSaleFixture(t, now)
		f.ownSeries("event-1")
		f.organizer(true)
		f.otherSales("event-1", existingSale("ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(11, 1, 10, 0), saleTime(11, 10, 10, 0), "event-1"))
		f.stores()
		in := firstComeInput()
		in.SaleStart = saleTime(11, 20, 10, 0)

		view, err := f.uc.Create(ctx, "org-1", in)

		require.NoError(t, err)
		assert.Equal(t, saleTime(11, 20, 10, 0), view.SaleStartTime)
	})

	t.Run("overlapping sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/create "Overlapping sale"
		f := newTicketSaleFixture(t, now)
		f.ownSeries("event-1")
		f.organizer(true)
		f.otherSales("event-1", existingSale("ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(11, 1, 0, 0), saleTime(11, 10, 0, 0), "event-1"))
		in := firstComeInput()
		in.SaleStart = saleTime(11, 8, 10, 0)

		_, err := f.uc.Create(ctx, "org-1", in)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("other events are not affected", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/create "Other events are not affected"
		f := newTicketSaleFixture(t, now)
		f.ownSeries("event-1", "event-2")
		f.organizer(true)
		// The overlapping sale covers only event-2, so event-1 lists none.
		f.otherSales("event-1")
		f.stores()

		_, err := f.uc.Create(ctx, "org-1", firstComeInput())

		require.NoError(t, err)
	})

	t.Run("tour presale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/create "Tour presale"
		f := newTicketSaleFixture(t, now)
		f.ownSeries("event-1", "event-2")
		f.organizer(true)
		f.otherSales("event-1")
		f.otherSales("event-2")
		f.stores()
		in := lotteryInput()
		in.EventIDs = []string{"event-1", "event-2"}

		view, err := f.uc.Create(ctx, "org-1", in)

		require.NoError(t, err)
		assert.False(t, view.IsDrawnAt(now, 0), "not drawn: the window has not even opened")
		assert.Equal(t, entity.VerificationRequirementNone, view.VerificationRequirement)
		assert.Equal(t, 10*24*time.Hour, view.SaleEndTime.Sub(view.SaleStartTime))
		assert.Equal(t, []string{"event-1", "event-2"}, view.EventIDs)
	})

	t.Run("no end time", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/create "No end time"
		f := newTicketSaleFixture(t, now)
		in := firstComeInput()
		in.SaleEnd = time.Time{}

		_, err := f.uc.Create(ctx, "org-1", in)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		f.sales.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	})

	t.Run("event without a ticket stock", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/create "Event without a ticket stock"
		f := newTicketSaleFixture(t, now)
		f.seriesOwnedBy("org-1", "event-1")
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.stocks.EXPECT().ListByEvent(mock.Anything, "event-1", mock.Anything).Return([]*entity.TicketStock{}, nil)

		_, err := f.uc.Create(ctx, "org-1", lotteryInput())

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		f.sales.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
	})
}

func TestTicketSaleUseCase_ListOwnByEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := saleTime(11, 12, 12, 0)

	// ownsEvent makes event-1 org-1's.
	ownsEvent := func(f *ticketSaleFixture) {
		f.events.EXPECT().GetEventOrganizerID(mock.Anything, "event-1").Return("org-1", nil)
	}

	t.Run("drawn presale and upcoming general sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/list-own-by-event "Drawn presale and upcoming general sale"
		f := newTicketSaleFixture(t, now)
		ownsEvent(f)
		presale := existingSale("ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(11, 1, 10, 0), saleTime(11, 10, 10, 0), "event-1")
		general := existingSale("一般発売", entity.TicketSaleMethodFirstCome, saleTime(11, 15, 10, 0), saleTime(11, 20, 19, 0), "event-1")
		f.otherSales("event-1", presale, general)
		stockS := tsStock("event-1", 150, 120, 0)
		stockA := &entity.TicketStock{ID: "stock-a", EventID: "event-1", SeatTypeID: "seat-a", Quantity: 200, SeatType: &entity.SeatType{ID: "seat-a", Name: "A席", Price: 6000}}
		f.stocks.EXPECT().ListByEvent(mock.Anything, "event-1", now).Return([]*entity.TicketStock{stockS, stockA}, nil)
		sStats := entity.LotteryEntryStats{EntryCount: 412, TicketCount: 1200, WonEntryCount: 318, WonTicketCount: 120, LostEntryCount: 94}
		f.entries.EXPECT().GetStatsByTicketSaleAndTicketStock(mock.Anything, presale.ID, stockS.ID).Return(sStats, nil)
		f.entries.EXPECT().GetStatsByTicketSaleAndTicketStock(mock.Anything, presale.ID, stockA.ID).Return(entity.LotteryEntryStats{}, nil)
		f.orders.EXPECT().ListReferralStatsByEvent(mock.Anything, "event-1").Return([]entity.ReferralCodeStats{}, nil)

		got, err := f.uc.ListOwnByEvent(ctx, "org-1", "event-1")

		require.NoError(t, err)
		require.Len(t, got.Sales, 2)
		assert.Equal(t, presale.ID, got.Sales[0].ID)
		assert.Equal(t, general.ID, got.Sales[1].ID)
		require.Len(t, got.LotteryEntryStats, 2, "one per TicketStock of the presale, none for the first-come sale")
		assert.Equal(t, presale.ID, got.LotteryEntryStats[0].SaleID)
		assert.Equal(t, stockS.ID, got.LotteryEntryStats[0].Stock.ID)
		assert.Equal(t, entity.SeatTypeID("seat-s"), got.LotteryEntryStats[0].Stock.SeatTypeID)
		assert.Equal(t, sStats, got.LotteryEntryStats[0].Stats)
		assert.True(t, got.Sales[0].IsDrawnAt(now, sStats.EntryCount-sStats.WonEntryCount-sStats.LostEntryCount), "the presale is drawn")
	})

	t.Run("event without a sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/list-own-by-event "Event without a sale"
		f := newTicketSaleFixture(t, now)
		ownsEvent(f)
		f.otherSales("event-1")
		f.stocks.EXPECT().ListByEvent(mock.Anything, "event-1", now).Return([]*entity.TicketStock{}, nil)
		f.orders.EXPECT().ListReferralStatsByEvent(mock.Anything, "event-1").Return([]entity.ReferralCodeStats{}, nil)

		got, err := f.uc.ListOwnByEvent(ctx, "org-1", "event-1")

		require.NoError(t, err)
		assert.Empty(t, got.Sales)
		assert.Empty(t, got.LotteryEntryStats)
	})

	t.Run("purchases per referral code", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/list-own-by-event "Purchases per referral code"
		f := newTicketSaleFixture(t, now)
		ownsEvent(f)
		f.otherSales("event-1", existingSale("一般発売", entity.TicketSaleMethodFirstCome, saleTime(11, 1, 10, 0), saleTime(11, 20, 19, 0), "event-1"))
		f.stocks.EXPECT().ListByEvent(mock.Anything, "event-1", now).Return([]*entity.TicketStock{tsStock("event-1", 150, 52, 0)}, nil)
		code := entity.ReferralCode("member-a")
		stats := []entity.ReferralCodeStats{
			{OrderCount: 40, TicketCount: 80, Amount: 320000},
			{Code: &code, OrderCount: 12, TicketCount: 24, Amount: 192000},
		}
		f.orders.EXPECT().ListReferralStatsByEvent(mock.Anything, "event-1").Return(stats, nil)

		got, err := f.uc.ListOwnByEvent(ctx, "org-1", "event-1")

		require.NoError(t, err)
		assert.Equal(t, stats, got.ReferralCodeStats)
	})

	t.Run("another organizer's event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/list-own-by-event "Another Organizer's event"
		f := newTicketSaleFixture(t, now)
		f.events.EXPECT().GetEventOrganizerID(mock.Anything, "event-1").Return("org-2", nil)
		f.events.EXPECT().GetEventOrganizerID(mock.Anything, "missing").Return("", apperr.ErrNotFound)

		_, err := f.uc.ListOwnByEvent(ctx, "org-1", "event-1")
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
		_, err = f.uc.ListOwnByEvent(ctx, "org-1", "missing")
		assert.ErrorIs(t, err, apperr.ErrPermissionDenied, "does not reveal whether it exists")
	})
}

func TestTicketSaleUseCase_ListByEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := saleTime(11, 5, 12, 0)
	start := saleTime(11, 20, 19, 0)

	// published makes event-1 published with its start time, a limit of 4,
	// its sales and its stocks.
	published := func(f *ticketSaleFixture, sales []*entity.TicketSale, stocks ...*entity.TicketStock) {
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.otherSales("event-1", sales...)
		limit := 4
		f.eventRepo.EXPECT().Get(mock.Anything, "event-1").Return(&entity.Event{ID: "event-1", StartTime: &start, PerAccountLimit: &limit}, nil).Maybe()
		f.stocks.EXPECT().ListByEvent(mock.Anything, "event-1", now).Return(stocks, nil).Maybe()
	}
	general := func() *entity.TicketSale {
		return existingSale("一般発売", entity.TicketSaleMethodFirstCome, saleTime(11, 1, 10, 0), saleTime(11, 20, 19, 0), "event-1")
	}

	t.Run("available with few left", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/list-by-event "Available with few left"
		f := newTicketSaleFixture(t, now)
		published(f, []*entity.TicketSale{general()}, tsStock("event-1", 150, 135, 0))

		got, err := f.uc.ListByEvent(ctx, "event-1")

		require.NoError(t, err)
		require.Len(t, got.Sales, 1)
		require.Len(t, got.Stocks, 1)
		stock := got.Stocks[0].Stock
		assert.Equal(t, entity.TicketStockStateAvailable, stock.State())
		assert.True(t, stock.IsLowStock())
		assert.Equal(t, int64(8000), stock.SeatType.Price)
		assert.False(t, got.Stocks[0].ResaleOpen)
		require.NotNil(t, got.PerAccountLimit)
		assert.Equal(t, 4, *got.PerAccountLimit)
	})

	t.Run("last tickets in checkouts", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/list-by-event "Last tickets in checkouts"
		f := newTicketSaleFixture(t, now)
		published(f, []*entity.TicketSale{general()}, tsStock("event-1", 150, 147, 3))

		got, err := f.uc.ListByEvent(ctx, "event-1")

		require.NoError(t, err)
		assert.Equal(t, entity.TicketStockStateAllHeld, got.Stocks[0].Stock.State())
	})

	t.Run("sold out before the resale deadline", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/list-by-event "Sold out before the resale deadline"
		f := newTicketSaleFixture(t, now)
		published(f, []*entity.TicketSale{general()}, tsStock("event-1", 150, 150, 0))

		got, err := f.uc.ListByEvent(ctx, "event-1")

		require.NoError(t, err)
		assert.Equal(t, entity.TicketStockStateSoldOut, got.Stocks[0].Stock.State())
		assert.True(t, got.Stocks[0].ResaleOpen)
	})

	t.Run("sold out after the resale deadline", func(t *testing.T) {
		t.Parallel()
		f := newTicketSaleFixture(t, start.Add(-time.Hour))
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.otherSales("event-1", general())
		f.eventRepo.EXPECT().Get(mock.Anything, "event-1").Return(&entity.Event{ID: "event-1", StartTime: &start}, nil)
		f.stocks.EXPECT().ListByEvent(mock.Anything, "event-1", mock.Anything).Return([]*entity.TicketStock{tsStock("event-1", 150, 150, 0)}, nil)

		got, err := f.uc.ListByEvent(ctx, "event-1")

		require.NoError(t, err)
		assert.False(t, got.Stocks[0].ResaleOpen)
	})

	t.Run("tour presale seen from one date", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/list-by-event "Tour presale seen from one date"
		f := newTicketSaleFixture(t, now)
		tour := existingSale("ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(11, 1, 10, 0), saleTime(11, 10, 10, 0), "event-1", "event-2", "event-3")
		// Only event-1's stocks are read.
		published(f, []*entity.TicketSale{tour}, tsStock("event-1", 150, 0, 0))

		got, err := f.uc.ListByEvent(ctx, "event-1")

		require.NoError(t, err)
		require.Len(t, got.Stocks, 1)
		assert.Equal(t, "event-1", got.Stocks[0].Stock.EventID)
	})

	t.Run("presale and general sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/list-by-event "Presale and general sale"
		f := newTicketSaleFixture(t, now)
		presale := existingSale("ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(11, 1, 10, 0), saleTime(11, 10, 10, 0), "event-1")
		later := existingSale("一般発売", entity.TicketSaleMethodFirstCome, saleTime(11, 20, 10, 0), saleTime(11, 20, 19, 0), "event-1")
		published(f, []*entity.TicketSale{presale, later}, tsStock("event-1", 150, 0, 0))

		got, err := f.uc.ListByEvent(ctx, "event-1")

		require.NoError(t, err)
		require.Len(t, got.Sales, 2)
		assert.Equal(t, presale.ID, got.Sales[0].ID)
		assert.Len(t, got.Stocks, 1, "the same stocks for both sales")
	})

	t.Run("concert cancelled", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/list-by-event "Concert cancelled"
		f := newTicketSaleFixture(t, now)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(false, nil)

		got, err := f.uc.ListByEvent(ctx, "event-1")

		require.NoError(t, err)
		assert.Empty(t, got.Sales)
		assert.Empty(t, got.Stocks)
	})

	t.Run("event without a sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/list-by-event "Event without a sale"
		f := newTicketSaleFixture(t, now)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.otherSales("event-1")

		got, err := f.uc.ListByEvent(ctx, "event-1")

		require.NoError(t, err)
		assert.Empty(t, got.Sales)
		assert.Empty(t, got.Stocks)
		assert.Nil(t, got.PerAccountLimit)
	})
}

func TestTicketSaleUseCase_SetWindow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// setsWindow makes TicketSale.SetWindow return sale with the new window.
	setsWindow := func(f *ticketSaleFixture, sale *entity.TicketSale, start, end time.Time) {
		f.sales.EXPECT().SetWindow(mock.Anything, sale.ID, start, end).RunAndReturn(
			func(_ context.Context, _ entity.TicketSaleID, s, e time.Time) (*entity.TicketSale, error) {
				moved := *sale
				moved.SaleStartTime, moved.SaleEndTime = s, e
				return &moved, nil
			})
	}

	t.Run("another organizer's sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/set-window "Another Organizer's sale"
		f := newTicketSaleFixture(t, saleTime(10, 25, 12, 0))
		sale := existingSale("ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(11, 1, 10, 0), saleTime(11, 10, 10, 0), "event-1")
		f.sales.EXPECT().Get(mock.Anything, sale.ID).Return(sale, nil)
		f.seriesOwnedBy("org-2", "event-1")

		_, err := f.uc.SetWindow(ctx, "org-1", sale.ID, saleTime(11, 2, 10, 0), saleTime(11, 11, 10, 0))

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("unknown sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/set-window "Unknown sale"
		f := newTicketSaleFixture(t, saleTime(10, 25, 12, 0))
		f.sales.EXPECT().Get(mock.Anything, entity.TicketSaleID("missing")).Return(nil, apperr.ErrNotFound)

		_, err := f.uc.SetWindow(ctx, "org-1", "missing", saleTime(11, 2, 10, 0), saleTime(11, 11, 10, 0))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("lottery sale before it opens", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/set-window "Lottery sale before it opens"
		f := newTicketSaleFixture(t, saleTime(10, 25, 12, 0))
		sale := existingSale("ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(11, 1, 10, 0), saleTime(11, 10, 10, 0), "event-1")
		f.sales.EXPECT().Get(mock.Anything, sale.ID).Return(sale, nil)
		f.seriesOwnedBy("org-1", "event-1")
		f.otherSales("event-1", sale)
		setsWindow(f, sale, saleTime(11, 2, 10, 0), saleTime(11, 11, 10, 0))

		view, err := f.uc.SetWindow(ctx, "org-1", sale.ID, saleTime(11, 2, 10, 0), saleTime(11, 11, 10, 0))

		require.NoError(t, err)
		assert.Equal(t, saleTime(11, 2, 10, 0), view.SaleStartTime)
	})

	t.Run("lottery sale already open", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/set-window "Lottery sale already open"
		f := newTicketSaleFixture(t, saleTime(11, 3, 12, 0))
		sale := existingSale("ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(11, 1, 10, 0), saleTime(11, 10, 10, 0), "event-1")
		f.sales.EXPECT().Get(mock.Anything, sale.ID).Return(sale, nil)
		f.seriesOwnedBy("org-1", "event-1")

		_, err := f.uc.SetWindow(ctx, "org-1", sale.ID, saleTime(11, 1, 10, 0), saleTime(11, 12, 10, 0))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("first-come sale extended while on sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/set-window "First-come sale extended while on sale"
		f := newTicketSaleFixture(t, saleTime(11, 3, 12, 0))
		sale := existingSale("一般発売", entity.TicketSaleMethodFirstCome, saleTime(11, 1, 10, 0), saleTime(11, 15, 10, 0), "event-1")
		f.sales.EXPECT().Get(mock.Anything, sale.ID).Return(sale, nil)
		f.seriesOwnedBy("org-1", "event-1")
		f.otherSales("event-1", sale)
		setsWindow(f, sale, saleTime(11, 1, 10, 0), saleTime(11, 20, 18, 0))

		view, err := f.uc.SetWindow(ctx, "org-1", sale.ID, saleTime(11, 1, 10, 0), saleTime(11, 20, 18, 0))

		require.NoError(t, err)
		assert.Equal(t, saleTime(11, 20, 18, 0), view.SaleEndTime)
	})

	t.Run("moved onto another sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/set-window "Moved onto another sale"
		f := newTicketSaleFixture(t, saleTime(10, 25, 12, 0))
		presale := existingSale("ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(11, 1, 0, 0), saleTime(11, 10, 0, 0), "event-1")
		general := existingSale("一般発売", entity.TicketSaleMethodFirstCome, saleTime(11, 15, 10, 0), saleTime(11, 20, 19, 0), "event-1")
		f.sales.EXPECT().Get(mock.Anything, general.ID).Return(general, nil)
		f.seriesOwnedBy("org-1", "event-1")
		f.otherSales("event-1", presale, general)

		_, err := f.uc.SetWindow(ctx, "org-1", general.ID, saleTime(11, 8, 10, 0), saleTime(11, 20, 19, 0))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("moved within its own window", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/set-window "Moved within its own window"
		f := newTicketSaleFixture(t, saleTime(10, 25, 12, 0))
		sale := existingSale("ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(11, 1, 0, 0), saleTime(11, 10, 0, 0), "event-1")
		f.sales.EXPECT().Get(mock.Anything, sale.ID).Return(sale, nil)
		f.seriesOwnedBy("org-1", "event-1")
		f.otherSales("event-1", sale)
		setsWindow(f, sale, saleTime(11, 2, 0, 0), saleTime(11, 9, 0, 0))

		view, err := f.uc.SetWindow(ctx, "org-1", sale.ID, saleTime(11, 2, 0, 0), saleTime(11, 9, 0, 0))

		require.NoError(t, err)
		assert.Equal(t, saleTime(11, 9, 0, 0), view.SaleEndTime)
	})

	t.Run("ends after the show starts", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/set-window "Ends after the show starts"
		f := newTicketSaleFixture(t, saleTime(11, 3, 12, 0))
		sale := existingSale("一般発売", entity.TicketSaleMethodFirstCome, saleTime(11, 1, 10, 0), saleTime(11, 15, 10, 0), "event-1")
		f.sales.EXPECT().Get(mock.Anything, sale.ID).Return(sale, nil)
		f.seriesOwnedBy("org-1", "event-1")

		_, err := f.uc.SetWindow(ctx, "org-1", sale.ID, saleTime(11, 1, 10, 0), saleTime(11, 20, 19, 30))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("window breaking the sale rules", func(t *testing.T) {
		t.Parallel()
		f := newTicketSaleFixture(t, saleTime(10, 25, 12, 0))
		sale := existingSale("ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(11, 1, 0, 0), saleTime(11, 10, 0, 0), "event-1")
		f.sales.EXPECT().Get(mock.Anything, sale.ID).Return(sale, nil)
		f.seriesOwnedBy("org-1", "event-1")

		_, err := f.uc.SetWindow(ctx, "org-1", sale.ID, saleTime(11, 1, 0, 0), saleTime(11, 16, 0, 0))

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestTicketSaleUseCase_SetVerificationRequirement(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := saleTime(11, 5, 12, 0)

	// updatesRequirement makes the update return sale with the requirement.
	updatesRequirement := func(f *ticketSaleFixture, sale *entity.TicketSale, req entity.VerificationRequirement) {
		f.sales.EXPECT().SetVerificationRequirement(mock.Anything, sale.ID, req).RunAndReturn(
			func(context.Context, entity.TicketSaleID, entity.VerificationRequirement) (*entity.TicketSale, error) {
				updated := *sale
				updated.VerificationRequirement = req
				return &updated, nil
			})
	}

	t.Run("requirement tightened while open", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/set-verification-requirement "Requirement tightened while open"
		f := newTicketSaleFixture(t, now)
		sale := existingSale("ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(11, 1, 0, 0), saleTime(11, 10, 0, 0), "event-1")
		f.sales.EXPECT().Get(mock.Anything, sale.ID).Return(sale, nil)
		f.seriesOwnedBy("org-1", "event-1")
		updatesRequirement(f, sale, entity.VerificationRequirementJPKIOnly)

		view, err := f.uc.SetVerificationRequirement(ctx, "org-1", sale.ID, entity.VerificationRequirementJPKIOnly)

		require.NoError(t, err)
		assert.Equal(t, entity.VerificationRequirementJPKIOnly, view.VerificationRequirement)
	})

	t.Run("after the draw", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/set-verification-requirement "After the draw"
		f := newTicketSaleFixture(t, now)
		sale := existingSale("ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(10, 20, 0, 0), saleTime(10, 30, 0, 0), "event-1")
		f.sales.EXPECT().Get(mock.Anything, sale.ID).Return(sale, nil)
		f.seriesOwnedBy("org-1", "event-1")
		updatesRequirement(f, sale, entity.VerificationRequirementVerifiedAny)

		view, err := f.uc.SetVerificationRequirement(ctx, "org-1", sale.ID, entity.VerificationRequirementVerifiedAny)

		require.NoError(t, err)
		assert.Equal(t, entity.VerificationRequirementVerifiedAny, view.VerificationRequirement)
	})

	t.Run("unknown sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/set-verification-requirement "Unknown sale"
		f := newTicketSaleFixture(t, now)
		f.sales.EXPECT().Get(mock.Anything, entity.TicketSaleID("missing")).Return(nil, apperr.ErrNotFound)

		_, err := f.uc.SetVerificationRequirement(ctx, "org-1", "missing", entity.VerificationRequirementJPKIOnly)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("another organizer's sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/set-verification-requirement "Another Organizer's sale"
		f := newTicketSaleFixture(t, now)
		sale := existingSale("ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(11, 1, 0, 0), saleTime(11, 10, 0, 0), "event-1")
		f.sales.EXPECT().Get(mock.Anything, sale.ID).Return(sale, nil)
		f.seriesOwnedBy("org-2", "event-1")

		_, err := f.uc.SetVerificationRequirement(ctx, "org-1", sale.ID, entity.VerificationRequirementJPKIOnly)

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("no sale given", func(t *testing.T) {
		t.Parallel()
		f := newTicketSaleFixture(t, now)

		_, err := f.uc.SetVerificationRequirement(ctx, "org-1", "", entity.VerificationRequirementJPKIOnly)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}
