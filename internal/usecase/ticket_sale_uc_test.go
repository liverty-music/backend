package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var tsJST = time.FixedZone("JST", 9*60*60)

// ticketSaleFixture holds the mocks of a TicketSaleUseCase under test.
type ticketSaleFixture struct {
	sales      *entitymocks.MockTicketSaleRepository
	organizers *entitymocks.MockOrganizerRepository
	events     *ucmocks.MockEventPublishStatePort
	eventStart *time.Time
	uc         usecase.TicketSaleUseCase
}

func newTicketSaleFixture(t *testing.T, now time.Time) *ticketSaleFixture {
	t.Helper()
	f := &ticketSaleFixture{
		sales:      entitymocks.NewMockTicketSaleRepository(t),
		organizers: entitymocks.NewMockOrganizerRepository(t),
		events:     ucmocks.NewMockEventPublishStatePort(t),
	}
	start := time.Date(2026, 11, 20, 19, 0, 0, 0, tsJST)
	f.eventStart = &start
	eventStart := &stubEventStartTimeRepo{getFn: func(context.Context, string) (*time.Time, error) { return f.eventStart, nil }}
	f.uc = usecase.NewTicketSaleUseCase(f.sales, f.organizers, f.events, eventStart, fixedClock(now))
	return f
}

// ownedPublishedEvent makes event-1 belong to org-1, published.
func (f *ticketSaleFixture) ownedPublishedEvent() {
	f.events.EXPECT().GetEventOrganizerID(mock.Anything, "event-1").Return("org-1", nil).Maybe()
	f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil).Maybe()
}

func (f *ticketSaleFixture) organizerWithDetails(complete bool) {
	org := &entity.Organizer{ID: "org-1", Status: entity.OrganizerStatusActive, PlatformFeeRateBps: 800}
	if complete {
		org.SellerDetails = &entity.SellerDetails{
			LegalName: "株式会社リバティ", RepresentativeName: "代表 太郎", Address: "東京都渋谷区1-2-3",
			PhoneNumber: "+81312345678", ContactEmail: "contact@example.com",
		}
	}
	f.organizers.EXPECT().Get(mock.Anything, "org-1").Return(org, nil).Maybe()
}

func configureInput() usecase.ConfigureTicketSaleInput {
	return usecase.ConfigureTicketSaleInput{
		EventID:   "event-1",
		SaleStart: time.Date(2026, 11, 1, 10, 0, 0, 0, tsJST),
		Price:     3000,
		Quantity:  150,
	}
}

func TestTicketSaleUseCase_Configure(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, tsJST)

	t.Run("first configuration", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/configure "First configuration"
		f := newTicketSaleFixture(t, now)
		f.ownedPublishedEvent()
		f.organizerWithDetails(true)
		f.sales.EXPECT().GetByEvent(mock.Anything, "event-1", now).Return(nil, apperr.ErrNotFound)
		f.sales.EXPECT().Create(mock.Anything, mock.Anything).RunAndReturn(
			func(_ context.Context, s *entity.TicketSale) (*entity.TicketSale, error) { return s, nil })

		sale, err := f.uc.Configure(context.Background(), "org-1", configureInput())

		require.NoError(t, err)
		assert.Equal(t, *f.eventStart, sale.SaleEndTime, "the sale ends at the event's start")
		assert.Equal(t, 4, sale.PerAccountLimit)
		assert.Equal(t, 150, sale.Quantity)
	})

	t.Run("more tickets later", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/configure "More tickets later"
		f := newTicketSaleFixture(t, now)
		f.ownedPublishedEvent()
		f.organizerWithDetails(true)
		current := entity.NewTicketSale("event-1", configureInput().SaleStart, *f.eventStart, 3000, 150, 6, now)
		f.sales.EXPECT().GetByEvent(mock.Anything, "event-1", now).Return(current, nil)
		f.sales.EXPECT().Update(mock.Anything, mock.MatchedBy(func(s *entity.TicketSale) bool {
			return s.ID == current.ID && s.Quantity == 180 && s.PerAccountLimit == 6
		}), now).RunAndReturn(func(_ context.Context, s *entity.TicketSale, _ time.Time) (*entity.TicketSale, error) { return s, nil })
		in := configureInput()
		in.Quantity = 180

		sale, err := f.uc.Configure(context.Background(), "org-1", in)

		require.NoError(t, err)
		assert.Equal(t, 180, sale.Quantity)
	})

	t.Run("another organizer's event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/configure "Another organizer's event"
		f := newTicketSaleFixture(t, now)
		f.events.EXPECT().GetEventOrganizerID(mock.Anything, "event-1").Return("org-2", nil)

		_, err := f.uc.Configure(context.Background(), "org-1", configureInput())

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})

	t.Run("unknown event", func(t *testing.T) {
		t.Parallel()
		f := newTicketSaleFixture(t, now)
		f.events.EXPECT().GetEventOrganizerID(mock.Anything, "event-1").Return("", apperr.ErrNotFound)

		_, err := f.uc.Configure(context.Background(), "org-1", configureInput())

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied, "does not reveal whether the event exists")
	})

	t.Run("start time not announced", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/configure "Start time not announced"
		f := newTicketSaleFixture(t, now)
		f.ownedPublishedEvent()
		f.eventStart = nil

		_, err := f.uc.Configure(context.Background(), "org-1", configureInput())

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("seller details missing", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/configure "Seller details missing"
		f := newTicketSaleFixture(t, now)
		f.ownedPublishedEvent()
		f.organizerWithDetails(false)

		_, err := f.uc.Configure(context.Background(), "org-1", configureInput())

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})

	t.Run("sale ends after the show starts", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/configure "Sale ends after the show starts"
		f := newTicketSaleFixture(t, now)
		f.ownedPublishedEvent()
		f.organizerWithDetails(true)
		in := configureInput()
		end := time.Date(2026, 11, 20, 19, 30, 0, 0, tsJST)
		in.SaleEnd = &end

		_, err := f.uc.Configure(context.Background(), "org-1", in)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("draft event", func(t *testing.T) {
		t.Parallel()
		f := newTicketSaleFixture(t, now)
		f.events.EXPECT().GetEventOrganizerID(mock.Anything, "event-1").Return("org-1", nil)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(false, nil)

		_, err := f.uc.Configure(context.Background(), "org-1", configureInput())

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
	})
}

// saleWith returns a sale of event-1 open 2026-11-01 10:00 to 11-20 19:00.
func saleWith(quantity, sold, held int) *entity.TicketSale {
	return &entity.TicketSale{
		ID: "sale-1", EventID: "event-1", Method: entity.TicketSaleMethodFirstCome,
		SaleStartTime: time.Date(2026, 11, 1, 10, 0, 0, 0, tsJST),
		SaleEndTime:   time.Date(2026, 11, 20, 19, 0, 0, 0, tsJST),
		Price:         3000, Quantity: quantity, PerAccountLimit: 4, SoldCount: sold, HeldCount: held,
	}
}

func TestTicketSaleUseCase_Get(t *testing.T) {
	t.Parallel()
	noon := time.Date(2026, 11, 5, 12, 0, 0, 0, tsJST)

	t.Run("on sale with few left", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/get "On sale with few left"
		f := newTicketSaleFixture(t, noon)
		f.sales.EXPECT().GetByEvent(mock.Anything, "event-1", noon).Return(saleWith(150, 135, 0), nil)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)

		view, err := f.uc.Get(context.Background(), "event-1")

		require.NoError(t, err)
		assert.Equal(t, entity.TicketSaleStateOnSale, view.State)
		assert.True(t, view.LowStock)
	})

	t.Run("last tickets in checkouts", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/get "Last tickets in checkouts"
		f := newTicketSaleFixture(t, noon)
		f.sales.EXPECT().GetByEvent(mock.Anything, "event-1", noon).Return(saleWith(150, 147, 3), nil)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(true, nil)

		view, err := f.uc.Get(context.Background(), "event-1")

		require.NoError(t, err)
		assert.Equal(t, entity.TicketSaleStateAllHeld, view.State)
	})

	t.Run("concert cancelled", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/get "Concert cancelled"
		f := newTicketSaleFixture(t, noon)
		f.sales.EXPECT().GetByEvent(mock.Anything, "event-1", noon).Return(saleWith(150, 0, 0), nil)
		f.events.EXPECT().IsEventPublished(mock.Anything, "event-1").Return(false, nil)

		_, err := f.uc.Get(context.Background(), "event-1")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})

	t.Run("event without a sale", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/get "Event without a sale"
		f := newTicketSaleFixture(t, noon)
		f.sales.EXPECT().GetByEvent(mock.Anything, "event-1", noon).Return(nil, apperr.ErrNotFound)

		_, err := f.uc.Get(context.Background(), "event-1")

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}

func TestTicketSaleUseCase_GetOwn(t *testing.T) {
	t.Parallel()
	noon := time.Date(2026, 11, 5, 12, 0, 0, 0, tsJST)

	t.Run("owner checks sales", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/get-own "Owner checks sales"
		f := newTicketSaleFixture(t, noon)
		f.events.EXPECT().GetEventOrganizerID(mock.Anything, "event-1").Return("org-1", nil)
		sale := saleWith(150, 100, 6)
		sale.HasReservations = true
		f.sales.EXPECT().GetByEvent(mock.Anything, "event-1", noon).Return(sale, nil)

		view, err := f.uc.GetOwn(context.Background(), "org-1", "event-1")

		require.NoError(t, err)
		assert.Equal(t, 150, view.Sale.Quantity)
		assert.Equal(t, 100, view.Sale.SoldCount)
		assert.Equal(t, 6, view.Sale.HeldCount)
		assert.True(t, view.Sale.HasReservations)
		assert.Equal(t, entity.TicketSaleStateOnSale, view.State)
	})

	t.Run("another organizer's event", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/ticket-sale/get-own "Another organizer's event"
		f := newTicketSaleFixture(t, noon)
		f.events.EXPECT().GetEventOrganizerID(mock.Anything, "event-1").Return("org-2", nil)

		_, err := f.uc.GetOwn(context.Background(), "org-1", "event-1")

		assert.ErrorIs(t, err, apperr.ErrPermissionDenied)
	})
}
