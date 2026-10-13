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
	entries    *entitymocks.MockLotteryEntryRepository
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
		entries:    entitymocks.NewMockLotteryEntryRepository(t),
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
	f.uc = usecase.NewTicketSaleUseCase(f.sales, f.entries, f.series, f.organizers, f.events, eventStart, fixedClock(now))
	return f
}

// ownSeries makes series-1 org-1's, with the given events, all published.
func (f *ticketSaleFixture) ownSeries(eventIDs ...string) {
	f.seriesOwnedBy("org-1", eventIDs...)
	for _, id := range eventIDs {
		f.events.EXPECT().IsEventPublished(mock.Anything, id).Return(true, nil).Maybe()
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
	f.sales.EXPECT().ListByEvent(mock.Anything, eventID, mock.Anything).Return(sales, nil).Maybe()
}

// stores makes TicketSale.Create return the sale it is given.
func (f *ticketSaleFixture) stores() {
	f.sales.EXPECT().Create(mock.Anything, mock.Anything).RunAndReturn(
		func(_ context.Context, s *entity.TicketSale) (*entity.TicketSale, error) { return s, nil })
}

// existingSale returns a sale of series-1 with one TicketType per event.
func existingSale(name string, method entity.TicketSaleMethod, start, end time.Time, eventIDs ...string) *entity.TicketSale {
	sale := entity.NewTicketSale("series-1", name, method, start, end)
	for _, id := range eventIDs {
		entity.NewTicketType(sale, id, 3000, 150, 4)
	}
	return sale
}

func lotteryInput() usecase.CreateTicketSaleInput {
	end := saleTime(11, 11, 10, 0)
	return usecase.CreateTicketSaleInput{
		SeriesID:    "series-1",
		Name:        "ファンクラブ先行",
		Method:      entity.TicketSaleMethodLottery,
		SaleStart:   saleTime(11, 1, 10, 0),
		SaleEnd:     &end,
		TicketTypes: []usecase.TicketTypeInput{{EventID: "event-1", Price: 8000, Quantity: 100}},
	}
}

func firstComeInput() usecase.CreateTicketSaleInput {
	return usecase.CreateTicketSaleInput{
		SeriesID:    "series-1",
		Name:        "一般発売",
		Method:      entity.TicketSaleMethodFirstCome,
		SaleStart:   saleTime(11, 1, 10, 0),
		TicketTypes: []usecase.TicketTypeInput{{EventID: "event-1", Price: 3000, Quantity: 150}},
	}
}

// TicketSaleUseCase has no EventPublisher, so creating an Organizer's sale
// never requests an announcement: only discovery does.
//
// @spec components/usecase/ticket-sale/announce-discovered-sale "Organizer's sale created"
func TestTicketSaleUseCase_Create(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := saleTime(10, 20, 12, 0)

	t.Run("another organizer's series", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/create "Another organizer's series"
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
		in.TicketTypes = append(in.TicketTypes, usecase.TicketTypeInput{EventID: "event-of-other-series", Price: 8000, Quantity: 100})

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
		in.SaleEnd = &end

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
		in.SaleEnd = &end

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
		in.SaleEnd = &end

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
		assert.Equal(t, "ファンクラブ先行", view.Sale.Name)
	})

	t.Run("invalid configuration", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/create "Invalid configuration"
		fifteenDays := saleTime(11, 16, 10, 0)
		cases := map[string]func(*usecase.CreateTicketSaleInput){
			"window of 15 days": func(in *usecase.CreateTicketSaleInput) { in.SaleEnd = &fifteenDays },
			"limit over the quantity": func(in *usecase.CreateTicketSaleInput) {
				in.TicketTypes[0].Quantity, in.TicketTypes[0].PerAccountLimit = 2, 4
			},
			"price of 0":          func(in *usecase.CreateTicketSaleInput) { in.TicketTypes[0].Price = 0 },
			"no ticket type":      func(in *usecase.CreateTicketSaleInput) { in.TicketTypes = nil },
			"lottery without end": func(in *usecase.CreateTicketSaleInput) { in.SaleEnd = nil },
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
		assert.Equal(t, saleTime(11, 20, 10, 0), view.Sale.SaleStartTime)
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
		in.TicketTypes = []usecase.TicketTypeInput{
			{EventID: "event-1", Price: 8000, Quantity: 100},
			{EventID: "event-2", Price: 8000, Quantity: 100},
		}

		view, err := f.uc.Create(ctx, "org-1", in)

		require.NoError(t, err)
		assert.False(t, view.Sale.IsDrawn())
		assert.Equal(t, entity.VerificationRequirementNone, view.Sale.VerificationRequirement)
		assert.Equal(t, 10*24*time.Hour, view.Sale.SaleEndTime.Sub(view.Sale.SaleStartTime))
		require.Len(t, view.TicketTypes, 2)
		for _, ttv := range view.TicketTypes {
			assert.Equal(t, 100, ttv.TicketType.Quantity)
			assert.Equal(t, int64(8000), ttv.TicketType.Price)
		}
	})

	t.Run("first-come sale without an end time", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/create "First-come sale without an end time"
		f := newTicketSaleFixture(t, now)
		f.ownSeries("event-1")
		f.organizer(true)
		f.otherSales("event-1")
		f.stores()

		view, err := f.uc.Create(ctx, "org-1", firstComeInput())

		require.NoError(t, err)
		assert.Equal(t, saleTime(11, 20, 19, 0), view.Sale.SaleEndTime)
		require.Len(t, view.TicketTypes, 1)
		assert.Equal(t, 4, view.TicketTypes[0].TicketType.PerAccountLimit)
		assert.Equal(t, 0, view.TicketTypes[0].TicketType.SoldCount)
		assert.Equal(t, entity.TicketTypeStateNotYetOnSale, view.TicketTypes[0].State)
	})

	t.Run("first-come sale ends at the earliest event start", func(t *testing.T) {
		t.Parallel()
		f := newTicketSaleFixture(t, now)
		f.ownSeries("event-1", "event-2")
		early := saleTime(11, 18, 18, 0)
		f.starts["event-2"] = &early
		f.organizer(true)
		f.otherSales("event-1")
		f.otherSales("event-2")
		f.stores()
		in := firstComeInput()
		in.TicketTypes = append(in.TicketTypes, usecase.TicketTypeInput{EventID: "event-2", Price: 3000, Quantity: 150})

		view, err := f.uc.Create(ctx, "org-1", in)

		require.NoError(t, err)
		assert.Equal(t, early, view.Sale.SaleEndTime)
	})
}

func TestTicketSaleUseCase_ListOwnByEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := saleTime(11, 15, 12, 0)

	t.Run("drawn presale and upcoming general sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/list-own-by-event "Drawn presale and upcoming general sale"
		f := newTicketSaleFixture(t, now)
		f.events.EXPECT().GetEventOrganizerID(mock.Anything, "event-1").Return("org-1", nil)
		presale := existingSale("ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(11, 1, 10, 0), saleTime(11, 10, 10, 0), "event-1")
		drawTime := saleTime(11, 10, 10, 1)
		presale.DrawTime = &drawTime
		general := existingSale("一般発売", entity.TicketSaleMethodFirstCome, saleTime(11, 18, 10, 0), saleTime(11, 20, 19, 0), "event-1")
		f.sales.EXPECT().ListByEvent(mock.Anything, "event-1", now).Return([]*entity.TicketSale{presale, general}, nil)
		stats := entity.LotteryEntryStats{EntryCount: 120, TicketCount: 200, WonEntryCount: 80, WonTicketCount: 150, LostEntryCount: 40}
		f.entries.EXPECT().GetStatsByTicketType(mock.Anything, presale.TicketTypes[0].ID).Return(stats, nil)

		views, err := f.uc.ListOwnByEvent(ctx, "org-1", "event-1")

		require.NoError(t, err)
		require.Len(t, views, 2)
		assert.Equal(t, presale.ID, views[0].Sale.ID)
		assert.True(t, views[0].Sale.IsDrawn())
		require.NotNil(t, views[0].TicketTypes[0].Stats)
		assert.Equal(t, stats, *views[0].TicketTypes[0].Stats)
		assert.Equal(t, general.ID, views[1].Sale.ID)
		assert.Nil(t, views[1].TicketTypes[0].Stats)
		assert.Equal(t, entity.TicketTypeStateNotYetOnSale, views[1].TicketTypes[0].State)
	})

	t.Run("owner checks first-come sales", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/list-own-by-event "Owner checks first-come sales"
		f := newTicketSaleFixture(t, now)
		f.events.EXPECT().GetEventOrganizerID(mock.Anything, "event-1").Return("org-1", nil)
		sale := existingSale("一般発売", entity.TicketSaleMethodFirstCome, saleTime(11, 1, 10, 0), saleTime(11, 20, 19, 0), "event-1")
		sale.TicketTypes[0].SoldCount, sale.TicketTypes[0].HeldCount = 100, 6
		f.sales.EXPECT().ListByEvent(mock.Anything, "event-1", now).Return([]*entity.TicketSale{sale}, nil)

		views, err := f.uc.ListOwnByEvent(ctx, "org-1", "event-1")

		require.NoError(t, err)
		require.Len(t, views, 1)
		got := views[0].TicketTypes[0]
		assert.Equal(t, 150, got.TicketType.Quantity)
		assert.Equal(t, 100, got.TicketType.SoldCount)
		assert.Equal(t, 6, got.TicketType.HeldCount)
		assert.Equal(t, entity.TicketTypeStateOnSale, got.State)
	})

	t.Run("event without a sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/list-own-by-event "Event without a sale"
		f := newTicketSaleFixture(t, now)
		f.events.EXPECT().GetEventOrganizerID(mock.Anything, "event-1").Return("org-1", nil)
		f.sales.EXPECT().ListByEvent(mock.Anything, "event-1", now).Return([]*entity.TicketSale{}, nil)

		views, err := f.uc.ListOwnByEvent(ctx, "org-1", "event-1")

		require.NoError(t, err)
		assert.Empty(t, views)
	})

	t.Run("another organizer's event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/list-own-by-event "Another organizer's event"
		f := newTicketSaleFixture(t, now)
		f.events.EXPECT().GetEventOrganizerID(mock.Anything, "event-1").Return("org-2", nil)

		_, err := f.uc.ListOwnByEvent(ctx, "org-1", "event-1")

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("unknown event is not revealed", func(t *testing.T) {
		t.Parallel()
		f := newTicketSaleFixture(t, now)
		f.events.EXPECT().GetEventOrganizerID(mock.Anything, "event-1").Return("", apperr.ErrNotFound)

		_, err := f.uc.ListOwnByEvent(ctx, "org-1", "event-1")

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})
}

func TestTicketSaleUseCase_ListByEvent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := saleTime(11, 5, 12, 0)

	firstCome := func(sold, held int) *entity.TicketSale {
		sale := existingSale("一般発売", entity.TicketSaleMethodFirstCome, saleTime(11, 1, 10, 0), saleTime(11, 20, 19, 0), "event-1")
		sale.TicketTypes[0].SoldCount, sale.TicketTypes[0].HeldCount = sold, held
		return sale
	}

	t.Run("on sale with few left", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/list-by-event "On sale with few left"
		f := newTicketSaleFixture(t, now)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.sales.EXPECT().ListByEvent(mock.Anything, "event-1", now).Return([]*entity.TicketSale{firstCome(135, 0)}, nil)

		views, err := f.uc.ListByEvent(ctx, "event-1")

		require.NoError(t, err)
		require.Len(t, views, 1)
		got := views[0].TicketTypes[0]
		assert.Equal(t, entity.TicketTypeStateOnSale, got.State)
		assert.True(t, got.LowStock)
		assert.Equal(t, int64(3000), got.TicketType.Price)
	})

	t.Run("last tickets in checkouts", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/list-by-event "Last tickets in checkouts"
		f := newTicketSaleFixture(t, now)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.sales.EXPECT().ListByEvent(mock.Anything, "event-1", now).Return([]*entity.TicketSale{firstCome(140, 10)}, nil)

		views, err := f.uc.ListByEvent(ctx, "event-1")

		require.NoError(t, err)
		require.Len(t, views, 1)
		assert.Equal(t, entity.TicketTypeStateAllHeld, views[0].TicketTypes[0].State)
	})

	t.Run("tour presale seen from one date", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/list-by-event "Tour presale seen from one date"
		f := newTicketSaleFixture(t, now)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		tour := existingSale("ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(11, 1, 10, 0), saleTime(11, 10, 10, 0), "event-2", "event-1", "event-3")
		f.sales.EXPECT().ListByEvent(mock.Anything, "event-1", now).Return([]*entity.TicketSale{tour}, nil)

		views, err := f.uc.ListByEvent(ctx, "event-1")

		require.NoError(t, err)
		require.Len(t, views, 1)
		require.Len(t, views[0].TicketTypes, 1)
		assert.Equal(t, "event-1", views[0].TicketTypes[0].TicketType.EventID)
		require.Len(t, views[0].Sale.TicketTypes, 1)
		assert.Equal(t, "event-1", views[0].Sale.TicketTypes[0].EventID)
		assert.Len(t, tour.TicketTypes, 3, "the stored sale is not changed")
	})

	t.Run("presale and general sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/list-by-event "Presale and general sale"
		f := newTicketSaleFixture(t, now)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		presale := existingSale("ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(11, 1, 0, 0), saleTime(11, 10, 0, 0), "event-1")
		general := existingSale("一般発売", entity.TicketSaleMethodFirstCome, saleTime(11, 20, 0, 0), saleTime(11, 20, 19, 0), "event-1")
		f.sales.EXPECT().ListByEvent(mock.Anything, "event-1", now).Return([]*entity.TicketSale{presale, general}, nil)

		views, err := f.uc.ListByEvent(ctx, "event-1")

		require.NoError(t, err)
		require.Len(t, views, 2)
		assert.Equal(t, presale.ID, views[0].Sale.ID)
		assert.Equal(t, general.ID, views[1].Sale.ID)
	})

	t.Run("concert cancelled", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/list-by-event "Concert cancelled"
		f := newTicketSaleFixture(t, now)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(false, nil)

		views, err := f.uc.ListByEvent(ctx, "event-1")

		require.NoError(t, err)
		assert.Empty(t, views)
	})

	t.Run("event without a sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/list-by-event "Event without a sale"
		f := newTicketSaleFixture(t, now)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)
		f.sales.EXPECT().ListByEvent(mock.Anything, "event-1", now).Return([]*entity.TicketSale{}, nil)

		views, err := f.uc.ListByEvent(ctx, "event-1")

		require.NoError(t, err)
		assert.Empty(t, views)
	})
}

func TestTicketSaleUseCase_SetWindow(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	// setsWindow makes TicketSale.SetWindow return sale with the new window.
	setsWindow := func(f *ticketSaleFixture, sale *entity.TicketSale, start, end time.Time) {
		f.sales.EXPECT().SetWindow(mock.Anything, sale.ID, start, end, mock.Anything).RunAndReturn(
			func(_ context.Context, _ entity.TicketSaleID, s, e, _ time.Time) (*entity.TicketSale, error) {
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
		f.sales.EXPECT().Get(mock.Anything, sale.ID, mock.Anything).Return(sale, nil)
		f.seriesOwnedBy("org-2", "event-1")

		_, err := f.uc.SetWindow(ctx, "org-1", sale.ID, saleTime(11, 2, 10, 0), saleTime(11, 11, 10, 0))

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("unknown sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/set-window "Unknown sale"
		f := newTicketSaleFixture(t, saleTime(10, 25, 12, 0))
		f.sales.EXPECT().Get(mock.Anything, entity.TicketSaleID("missing"), mock.Anything).Return(nil, apperr.ErrNotFound)

		_, err := f.uc.SetWindow(ctx, "org-1", "missing", saleTime(11, 2, 10, 0), saleTime(11, 11, 10, 0))

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("lottery sale before it opens", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/set-window "Lottery sale before it opens"
		f := newTicketSaleFixture(t, saleTime(10, 25, 12, 0))
		sale := existingSale("ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(11, 1, 10, 0), saleTime(11, 10, 10, 0), "event-1")
		f.sales.EXPECT().Get(mock.Anything, sale.ID, mock.Anything).Return(sale, nil)
		f.seriesOwnedBy("org-1", "event-1")
		f.otherSales("event-1", sale)
		setsWindow(f, sale, saleTime(11, 2, 10, 0), saleTime(11, 11, 10, 0))

		view, err := f.uc.SetWindow(ctx, "org-1", sale.ID, saleTime(11, 2, 10, 0), saleTime(11, 11, 10, 0))

		require.NoError(t, err)
		assert.Equal(t, saleTime(11, 2, 10, 0), view.Sale.SaleStartTime)
	})

	t.Run("lottery sale already open", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/set-window "Lottery sale already open"
		f := newTicketSaleFixture(t, saleTime(11, 3, 12, 0))
		sale := existingSale("ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(11, 1, 10, 0), saleTime(11, 10, 10, 0), "event-1")
		f.sales.EXPECT().Get(mock.Anything, sale.ID, mock.Anything).Return(sale, nil)
		f.seriesOwnedBy("org-1", "event-1")

		_, err := f.uc.SetWindow(ctx, "org-1", sale.ID, saleTime(11, 1, 10, 0), saleTime(11, 12, 10, 0))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("first-come sale extended while on sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/set-window "First-come sale extended while on sale"
		f := newTicketSaleFixture(t, saleTime(11, 3, 12, 0))
		sale := existingSale("一般発売", entity.TicketSaleMethodFirstCome, saleTime(11, 1, 10, 0), saleTime(11, 15, 10, 0), "event-1")
		f.sales.EXPECT().Get(mock.Anything, sale.ID, mock.Anything).Return(sale, nil)
		f.seriesOwnedBy("org-1", "event-1")
		f.otherSales("event-1", sale)
		setsWindow(f, sale, saleTime(11, 1, 10, 0), saleTime(11, 20, 18, 0))

		view, err := f.uc.SetWindow(ctx, "org-1", sale.ID, saleTime(11, 1, 10, 0), saleTime(11, 20, 18, 0))

		require.NoError(t, err)
		assert.Equal(t, saleTime(11, 20, 18, 0), view.Sale.SaleEndTime)
	})

	t.Run("moved onto another sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/set-window "Moved onto another sale"
		f := newTicketSaleFixture(t, saleTime(10, 25, 12, 0))
		presale := existingSale("ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(11, 1, 0, 0), saleTime(11, 10, 0, 0), "event-1")
		general := existingSale("一般発売", entity.TicketSaleMethodFirstCome, saleTime(11, 15, 10, 0), saleTime(11, 20, 19, 0), "event-1")
		f.sales.EXPECT().Get(mock.Anything, general.ID, mock.Anything).Return(general, nil)
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
		f.sales.EXPECT().Get(mock.Anything, sale.ID, mock.Anything).Return(sale, nil)
		f.seriesOwnedBy("org-1", "event-1")
		f.otherSales("event-1", sale)
		setsWindow(f, sale, saleTime(11, 2, 0, 0), saleTime(11, 9, 0, 0))

		view, err := f.uc.SetWindow(ctx, "org-1", sale.ID, saleTime(11, 2, 0, 0), saleTime(11, 9, 0, 0))

		require.NoError(t, err)
		assert.Equal(t, saleTime(11, 9, 0, 0), view.Sale.SaleEndTime)
	})

	t.Run("ends after the show starts", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/set-window "Ends after the show starts"
		f := newTicketSaleFixture(t, saleTime(11, 3, 12, 0))
		sale := existingSale("一般発売", entity.TicketSaleMethodFirstCome, saleTime(11, 1, 10, 0), saleTime(11, 15, 10, 0), "event-1")
		f.sales.EXPECT().Get(mock.Anything, sale.ID, mock.Anything).Return(sale, nil)
		f.seriesOwnedBy("org-1", "event-1")

		_, err := f.uc.SetWindow(ctx, "org-1", sale.ID, saleTime(11, 1, 10, 0), saleTime(11, 20, 19, 30))

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("window breaking the sale rules", func(t *testing.T) {
		t.Parallel()
		f := newTicketSaleFixture(t, saleTime(10, 25, 12, 0))
		sale := existingSale("ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(11, 1, 0, 0), saleTime(11, 10, 0, 0), "event-1")
		f.sales.EXPECT().Get(mock.Anything, sale.ID, mock.Anything).Return(sale, nil)
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
		f.sales.EXPECT().SetVerificationRequirement(mock.Anything, sale.ID, req, now).RunAndReturn(
			func(context.Context, entity.TicketSaleID, entity.VerificationRequirement, time.Time) (*entity.TicketSale, error) {
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
		f.sales.EXPECT().Get(mock.Anything, sale.ID, now).Return(sale, nil)
		f.seriesOwnedBy("org-1", "event-1")
		updatesRequirement(f, sale, entity.VerificationRequirementJPKIOnly)

		view, err := f.uc.SetVerificationRequirement(ctx, "org-1", sale.ID, entity.VerificationRequirementJPKIOnly)

		require.NoError(t, err)
		assert.Equal(t, entity.VerificationRequirementJPKIOnly, view.Sale.VerificationRequirement)
	})

	t.Run("after the draw", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/set-verification-requirement "After the draw"
		f := newTicketSaleFixture(t, now)
		sale := existingSale("ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(10, 20, 0, 0), saleTime(10, 30, 0, 0), "event-1")
		drawTime := saleTime(10, 30, 0, 1)
		sale.DrawTime = &drawTime
		f.sales.EXPECT().Get(mock.Anything, sale.ID, now).Return(sale, nil)
		f.seriesOwnedBy("org-1", "event-1")
		updatesRequirement(f, sale, entity.VerificationRequirementVerifiedAny)

		view, err := f.uc.SetVerificationRequirement(ctx, "org-1", sale.ID, entity.VerificationRequirementVerifiedAny)

		require.NoError(t, err)
		assert.Equal(t, entity.VerificationRequirementVerifiedAny, view.Sale.VerificationRequirement)
	})

	t.Run("unknown sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/set-verification-requirement "Unknown sale"
		f := newTicketSaleFixture(t, now)
		f.sales.EXPECT().Get(mock.Anything, entity.TicketSaleID("missing"), now).Return(nil, apperr.ErrNotFound)

		_, err := f.uc.SetVerificationRequirement(ctx, "org-1", "missing", entity.VerificationRequirementJPKIOnly)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("another organizer's sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/set-verification-requirement "Another Organizer's sale"
		f := newTicketSaleFixture(t, now)
		sale := existingSale("ファンクラブ先行", entity.TicketSaleMethodLottery, saleTime(11, 1, 0, 0), saleTime(11, 10, 0, 0), "event-1")
		f.sales.EXPECT().Get(mock.Anything, sale.ID, now).Return(sale, nil)
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
