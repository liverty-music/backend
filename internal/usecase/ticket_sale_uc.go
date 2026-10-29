package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/entity"
)

// TicketStockView is a TicketStock of an event as a fan sees it, with whether
// its official resale is open at the time it was read. Its state and LowStock
// come from [entity.TicketStock.State] and [entity.TicketStock.IsLowStock].
type TicketStockView struct {
	// Stock is the stock with its SeatType and its counts at the read.
	Stock *entity.TicketStock
	// ResaleOpen reports whether the stock's official resale is open.
	ResaleOpen bool
}

// EventSales is what a fan sees of an event's sales: the sales, and once for
// the event the TicketStocks every sale sells and the per-account limit.
type EventSales struct {
	// Sales are the event's sales in start order.
	Sales []*entity.TicketSale
	// Stocks are the event's TicketStocks, highest price first.
	Stocks []*TicketStockView
	// PerAccountLimit is the event's per-account limit; nil when Sales is
	// empty.
	PerAccountLimit *int
}

// StockEntryStats are the entry stats of one Lottery sale for one TicketStock
// of the event.
type StockEntryStats struct {
	// SaleID is the Lottery sale.
	SaleID entity.TicketSaleID
	// Stock is the TicketStock, with its SeatType.
	Stock *entity.TicketStock
	// Stats are the counts of the sale's entries for the stock.
	Stats entity.LotteryEntryStats
}

// CreateTicketSaleInput is what an Organizer sets on a new sale.
type CreateTicketSaleInput struct {
	// SeriesID is the Series whose events the sale covers.
	SeriesID string
	// Name tells the sale apart from the event's other sales.
	Name string
	// Method is Lottery or FirstCome.
	Method entity.TicketSaleMethod
	// SaleStart is when the window opens.
	SaleStart time.Time
	// SaleEnd is when the window closes; required.
	SaleEnd time.Time
	// VerificationRequirement is whether a fan must hold an Active IdentityVerification; the
	// zero value is None.
	VerificationRequirement entity.VerificationRequirement
	// EventIDs are the events the sale sells; at least one.
	EventIDs []string
}

// OwnEventSales is what an event's Organizer sees of its sales: the sales,
// the entry stats of each Lottery sale for each TicketStock of the event, and
// beside them the event's purchases per referral code.
type OwnEventSales struct {
	// Sales are the event's sales in start order.
	Sales []*entity.TicketSale
	// LotteryEntryStats are the entry stats of the Lottery sales above, one
	// per sale and TicketStock of the event.
	LotteryEntryStats []*StockEntryStats
	// ReferralCodeStats are the event's Paid Orders per referral code, as
	// [entity.OrderRepository.ListReferralStatsByEvent] gives them.
	ReferralCodeStats []entity.ReferralCodeStats
}

// TicketSaleUseCase lets an Organizer open sales, by lottery or first come,
// on the published events of its Series and change them, and shows the
// sales of an event to fans and to its Organizer.
type TicketSaleUseCase interface {
	// Create stores a new sale naming its events for the Organizer's Series
	// and returns it, not drawn. Every event must be the Series', published,
	// timed and stocked; a Lottery sale must end before every event starts
	// and a FirstCome sale no later than any; the Organizer must have
	// complete business details; and the sale must not overlap another sale
	// of any of its events.
	//
	// # Possible errors
	//
	//  - InvalidArgument: no event, no end time, an event outside the Series,
	//    or values that break the TicketSale rules.
	//  - PermissionDenied: the Series does not exist or is another
	//    Organizer's.
	//  - FailedPrecondition: an event is not published, has no start time or
	//    has no TicketStock, the end time is too late for an event, the
	//    Organizer has no complete business details, or the window overlaps
	//    another sale of an event.
	Create(ctx context.Context, organizerID string, in CreateTicketSaleInput) (*entity.TicketSale, error)

	// ListOwnByEvent returns every sale of one of the Organizer's events, in
	// start order, and for each Lottery sale the entry stats of each
	// TicketStock of the event. Beside the sales it returns the event's
	// purchases per referral code. The sales are empty when the event has no
	// sale, and the purchases per code when the event has no Paid Order.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the event does not exist or is another Organizer's.
	ListOwnByEvent(ctx context.Context, organizerID, eventID string) (*OwnEventSales, error)

	// ListByEvent returns, for anyone, the sales of a published event in start
	// order and, once for the event, its TicketStocks with their SeatTypes,
	// states, LowStock and whether their resale is open now, and the event's
	// per-account limit. It returns no sale and no stock when the event has no
	// sale, is not published, or does not exist.
	ListByEvent(ctx context.Context, eventID string) (*EventSales, error)

	// SetWindow moves the window of one of the Organizer's sales and returns
	// the updated sale. A Lottery sale's window moves only before it opens.
	//
	// # Possible errors
	//
	//  - NotFound: the sale does not exist.
	//  - PermissionDenied: the sale's Series is another Organizer's.
	//  - InvalidArgument: the window breaks the TicketSale rules.
	//  - FailedPrecondition: a Lottery sale has opened, the end time is too
	//    late for an event, or the window overlaps another sale of an event.
	SetWindow(ctx context.Context, organizerID string, saleID entity.TicketSaleID, saleStart, saleEnd time.Time) (*entity.TicketSale, error)

	// SetVerificationRequirement changes whether fans taking part in one of
	// the Organizer's sales must hold an Active IdentityVerification, at any time, and returns
	// the updated sale. Entries already made are not re-checked.
	//
	// # Possible errors
	//
	//  - InvalidArgument: no sale is given.
	//  - NotFound: the sale does not exist.
	//  - PermissionDenied: the sale's Series is another Organizer's.
	SetVerificationRequirement(ctx context.Context, organizerID string, saleID entity.TicketSaleID, req entity.VerificationRequirement) (*entity.TicketSale, error)
}

// ticketSaleUseCase implements [TicketSaleUseCase].
type ticketSaleUseCase struct {
	saleRepo      entity.TicketSaleRepository
	stockRepo     entity.TicketStockRepository
	entryRepo     entity.LotteryEntryRepository
	orderRepo     entity.OrderRepository
	seriesRepo    entity.SeriesRepository
	organizerRepo entity.OrganizerRepository
	eventRepo     entity.EventRepository
	eventState    EventPublishStatePort
	eventStart    EventStartTimeRepository
	clock         Clock
}

// Compile-time interface compliance check.
var _ TicketSaleUseCase = (*ticketSaleUseCase)(nil)

// NewTicketSaleUseCase constructs a TicketSaleUseCase.
func NewTicketSaleUseCase(
	saleRepo entity.TicketSaleRepository,
	stockRepo entity.TicketStockRepository,
	entryRepo entity.LotteryEntryRepository,
	orderRepo entity.OrderRepository,
	seriesRepo entity.SeriesRepository,
	organizerRepo entity.OrganizerRepository,
	eventRepo entity.EventRepository,
	eventState EventPublishStatePort,
	eventStart EventStartTimeRepository,
	clock Clock,
) TicketSaleUseCase {
	return &ticketSaleUseCase{
		saleRepo:      saleRepo,
		stockRepo:     stockRepo,
		entryRepo:     entryRepo,
		orderRepo:     orderRepo,
		seriesRepo:    seriesRepo,
		organizerRepo: organizerRepo,
		eventRepo:     eventRepo,
		eventState:    eventState,
		eventStart:    eventStart,
		clock:         clock,
	}
}

// errNotOwnSeries is returned, without revealing whether the Series exists,
// when it is not the calling Organizer's.
func errNotOwnSeries() error {
	return apperr.New(codes.PermissionDenied, "the series is not the organizer's")
}

// authoredEvents reads the Series with Series.GetAuthored and returns its
// events, failing with PermissionDenied when the Series does not exist or is
// not the Organizer's.
func authoredEvents(ctx context.Context, seriesRepo entity.SeriesRepository, organizerID, seriesID string) ([]*entity.Event, error) {
	series, concerts, err := seriesRepo.GetAuthored(ctx, seriesID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, errNotOwnSeries()
		}
		return nil, err
	}
	if series.OrganizerID == nil || *series.OrganizerID != organizerID {
		return nil, errNotOwnSeries()
	}
	return entity.EventsOf(concerts), nil
}

// eventStartTimes reads the start time of each event, failing with
// FailedPrecondition when one has none.
func (uc *ticketSaleUseCase) eventStartTimes(ctx context.Context, eventIDs []string) ([]time.Time, error) {
	starts := make([]time.Time, 0, len(eventIDs))
	for _, id := range eventIDs {
		start, err := uc.eventStart.GetEventStartTime(ctx, id)
		if err != nil {
			return nil, err
		}
		if start == nil {
			return nil, apperr.New(codes.FailedPrecondition, "an event of the sale has no start time")
		}
		starts = append(starts, *start)
	}
	return starts, nil
}

// checkEndBeforeEvents fails with FailedPrecondition when a Lottery sale does
// not end before every event start, or a FirstCome sale ends after any.
func checkEndBeforeEvents(method entity.TicketSaleMethod, end time.Time, starts []time.Time) error {
	for _, start := range starts {
		if method == entity.TicketSaleMethodLottery && !end.Before(start) {
			return apperr.New(codes.FailedPrecondition, "a lottery sale must end before every event starts")
		}
		if method == entity.TicketSaleMethodFirstCome && end.After(start) {
			return apperr.New(codes.FailedPrecondition, "a first-come sale must end no later than every event's start")
		}
	}
	return nil
}

// checkNoOverlap fails with FailedPrecondition when sale's window overlaps
// the window of another sale of any of its events. The sale's own stored
// window is not compared.
func (uc *ticketSaleUseCase) checkNoOverlap(ctx context.Context, sale *entity.TicketSale) error {
	for _, eventID := range sale.EventIDs {
		others, err := uc.saleRepo.ListByEvent(ctx, eventID)
		if err != nil {
			return err
		}
		for _, other := range others {
			if other.ID != sale.ID && sale.Overlaps(other) {
				return apperr.New(codes.FailedPrecondition, "the window overlaps another sale of an event")
			}
		}
	}
	return nil
}

// Create implements [TicketSaleUseCase].
func (uc *ticketSaleUseCase) Create(ctx context.Context, organizerID string, in CreateTicketSaleInput) (*entity.TicketSale, error) {
	if len(in.EventIDs) == 0 {
		return nil, apperr.New(codes.InvalidArgument, "a sale needs at least one event")
	}
	if in.SaleEnd.IsZero() {
		return nil, apperr.New(codes.InvalidArgument, "a sale needs an end time")
	}
	sale := entity.NewTicketSale(in.SeriesID, in.Name, in.Method, in.SaleStart, in.SaleEnd, in.EventIDs...)
	sale.VerificationRequirement = in.VerificationRequirement
	if err := sale.Validate(); err != nil {
		return nil, apperr.New(codes.InvalidArgument, err.Error())
	}

	events, err := authoredEvents(ctx, uc.seriesRepo, organizerID, in.SeriesID)
	if err != nil {
		return nil, err
	}
	if err := sale.CheckEventsOfSeries(events); err != nil {
		return nil, apperr.New(codes.InvalidArgument, err.Error())
	}
	for _, id := range sale.EventIDs {
		published, err := uc.eventState.IsEventPublished(ctx, id)
		if err != nil {
			return nil, err
		}
		if !published {
			return nil, apperr.New(codes.FailedPrecondition, "an event of the sale is not published")
		}
	}
	starts, err := uc.eventStartTimes(ctx, sale.EventIDs)
	if err != nil {
		return nil, err
	}
	now := uc.clock()
	for _, id := range sale.EventIDs {
		stocks, err := uc.stockRepo.ListByEvent(ctx, id, now)
		if err != nil {
			return nil, err
		}
		if len(stocks) == 0 {
			return nil, apperr.New(codes.FailedPrecondition, "an event of the sale has no ticket stock")
		}
	}
	if err := checkEndBeforeEvents(sale.Method, sale.SaleEndTime, starts); err != nil {
		return nil, err
	}

	organizer, err := uc.organizerRepo.Get(ctx, organizerID)
	if err != nil {
		return nil, err
	}
	if !organizer.HasCompleteBusinessDetails() {
		return nil, apperr.New(codes.FailedPrecondition, "the organizer has no complete business details")
	}

	if err := uc.checkNoOverlap(ctx, sale); err != nil {
		return nil, err
	}
	return uc.saleRepo.Create(ctx, sale)
}

// ListOwnByEvent implements [TicketSaleUseCase].
func (uc *ticketSaleUseCase) ListOwnByEvent(ctx context.Context, organizerID, eventID string) (*OwnEventSales, error) {
	owner, err := uc.eventState.GetEventOrganizerID(ctx, eventID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, apperr.New(codes.PermissionDenied, "the event is not the organizer's")
		}
		return nil, err
	}
	if owner != organizerID {
		return nil, apperr.New(codes.PermissionDenied, "the event is not the organizer's")
	}
	sales, err := uc.saleRepo.ListByEvent(ctx, eventID)
	if err != nil {
		return nil, err
	}
	stocks, err := uc.stockRepo.ListByEvent(ctx, eventID, uc.clock())
	if err != nil {
		return nil, err
	}
	out := &OwnEventSales{Sales: sales, LotteryEntryStats: []*StockEntryStats{}}
	for _, sale := range sales {
		if sale.Method != entity.TicketSaleMethodLottery {
			continue
		}
		for _, stock := range stocks {
			stats, err := uc.entryRepo.GetStatsByTicketSaleAndTicketStock(ctx, sale.ID, stock.ID)
			if err != nil {
				return nil, err
			}
			out.LotteryEntryStats = append(out.LotteryEntryStats, &StockEntryStats{SaleID: sale.ID, Stock: stock, Stats: stats})
		}
	}
	out.ReferralCodeStats, err = uc.orderRepo.ListReferralStatsByEvent(ctx, eventID)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListByEvent implements [TicketSaleUseCase].
func (uc *ticketSaleUseCase) ListByEvent(ctx context.Context, eventID string) (*EventSales, error) {
	empty := &EventSales{Sales: []*entity.TicketSale{}, Stocks: []*TicketStockView{}}
	published, err := uc.eventState.IsEventPublished(ctx, eventID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return empty, nil
		}
		return nil, err
	}
	if !published {
		return empty, nil
	}
	sales, err := uc.saleRepo.ListByEvent(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if len(sales) == 0 {
		return empty, nil
	}
	event, err := uc.eventRepo.Get(ctx, eventID)
	if err != nil {
		return nil, err
	}
	now := uc.clock()
	stocks, err := uc.stockRepo.ListByEvent(ctx, eventID, now)
	if err != nil {
		return nil, err
	}
	deadline, hasDeadline := entity.ResaleDeadlineOf(event)
	views := make([]*TicketStockView, 0, len(stocks))
	for _, stock := range stocks {
		views = append(views, &TicketStockView{Stock: stock, ResaleOpen: hasDeadline && stock.IsResaleOpenAt(now, deadline)})
	}
	limit := event.Limit()
	return &EventSales{Sales: sales, Stocks: views, PerAccountLimit: &limit}, nil
}

// ownSale reads the sale and fails with PermissionDenied unless its Series is
// the Organizer's.
func (uc *ticketSaleUseCase) ownSale(ctx context.Context, organizerID string, saleID entity.TicketSaleID) (*entity.TicketSale, error) {
	sale, err := uc.saleRepo.Get(ctx, saleID)
	if err != nil {
		return nil, err
	}
	if _, err := authoredEvents(ctx, uc.seriesRepo, organizerID, sale.SeriesID); err != nil {
		return nil, err
	}
	return sale, nil
}

// SetWindow implements [TicketSaleUseCase].
func (uc *ticketSaleUseCase) SetWindow(ctx context.Context, organizerID string, saleID entity.TicketSaleID, saleStart, saleEnd time.Time) (*entity.TicketSale, error) {
	sale, err := uc.ownSale(ctx, organizerID, saleID)
	if err != nil {
		return nil, err
	}
	if sale.Method == entity.TicketSaleMethodLottery && !uc.clock().Before(sale.SaleStartTime) {
		return nil, apperr.New(codes.FailedPrecondition, "a lottery sale's window cannot change once it has opened")
	}
	if err := sale.ValidateWindow(saleStart, saleEnd); err != nil {
		return nil, apperr.New(codes.InvalidArgument, err.Error())
	}
	starts, err := uc.eventStartTimes(ctx, sale.EventIDs)
	if err != nil {
		return nil, err
	}
	if err := checkEndBeforeEvents(sale.Method, saleEnd, starts); err != nil {
		return nil, err
	}
	moved := *sale
	moved.SaleStartTime, moved.SaleEndTime = saleStart, saleEnd
	if err := uc.checkNoOverlap(ctx, &moved); err != nil {
		return nil, err
	}
	return uc.saleRepo.SetWindow(ctx, saleID, saleStart, saleEnd)
}

// SetVerificationRequirement implements [TicketSaleUseCase].
func (uc *ticketSaleUseCase) SetVerificationRequirement(ctx context.Context, organizerID string, saleID entity.TicketSaleID, req entity.VerificationRequirement) (*entity.TicketSale, error) {
	if saleID == "" {
		return nil, apperr.New(codes.InvalidArgument, "ticket_sale_id is required")
	}
	if _, err := uc.ownSale(ctx, organizerID, saleID); err != nil {
		return nil, err
	}
	return uc.saleRepo.SetVerificationRequirement(ctx, saleID, req)
}
