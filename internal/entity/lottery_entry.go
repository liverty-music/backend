package entity

import (
	"context"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// LotteryEntryID is the opaque identifier for a single LotteryEntry.
//
// Mirrors liverty_music.entity.v1.LotteryEntryId.value (a UUID string).
type LotteryEntryID string

// LotteryEntryState is the lifecycle state of a [LotteryEntry]. Values mirror
// the proto enum liverty_music.entity.v1.LotteryEntryState and the stored
// state.
//
//	[*] --> Entered --> Won | Lost
//	Entered --> Lost (the concert was cancelled)
//	Won --> Lost (the card could not be charged, or the concert was cancelled)
type LotteryEntryState int8

const (
	// LotteryEntryStateUnspecified is the zero value and is never persisted.
	LotteryEntryStateUnspecified LotteryEntryState = 0

	// LotteryEntryStateEntered means the fan confirmed the entry with its card
	// authorization in place; it takes part in the draw.
	LotteryEntryStateEntered LotteryEntryState = 1

	// LotteryEntryStateWon means the draw allocated tickets to the entry. Its
	// card authorization is captured afterwards, so its Order is Pending until
	// it is Paid.
	LotteryEntryStateWon LotteryEntryState = 2

	// LotteryEntryStateLost means the draw did not allocate tickets to the
	// entry, its win was reverted because its card could not be charged, or
	// its concert was cancelled before it was charged (see
	// [LotteryEntryLostReason]). Its Order is Void and its card authorization
	// is released.
	LotteryEntryStateLost LotteryEntryState = 3
)

// LotteryEntryLostReason is why a Lost LotteryEntry lost. The numbers are
// the stored values; a LotteryEntry that is not Lost has no reason.
type LotteryEntryLostReason int16

const (
	// LotteryEntryLostReasonUnspecified is the zero value: the entry is not
	// Lost. Never persisted.
	LotteryEntryLostReasonUnspecified LotteryEntryLostReason = 0

	// LotteryEntryLostReasonNotDrawn means the draw did not select the entry.
	LotteryEntryLostReasonNotDrawn LotteryEntryLostReason = 1

	// LotteryEntryLostReasonCardNotCharged means the draw selected the entry
	// but its Order's card authorization could not be captured.
	LotteryEntryLostReasonCardNotCharged LotteryEntryLostReason = 2

	// LotteryEntryLostReasonConcertCancelled means the entry's Event was
	// cancelled (中止) while the entry was Entered, so it ended without being
	// drawn, or after it won but before its card authorization was captured.
	// Nothing was charged.
	LotteryEntryLostReasonConcertCancelled LotteryEntryLostReason = 3
)

// String returns the lowercase state name.
func (s LotteryEntryState) String() string {
	switch s {
	case LotteryEntryStateEntered:
		return "entered"
	case LotteryEntryStateWon:
		return "won"
	case LotteryEntryStateLost:
		return "lost"
	default:
		return "UNSPECIFIED"
	}
}

// LotteryEntry is how one fan's Order takes part in a Lottery TicketSale: a
// request for a companion group of tickets of the Order's TicketStock,
// allocated all-or-nothing, made when the fan confirms an authorized card
// authorization for the Order's amount, which holds the amount until the
// draw. A fan enters a sale at most once for each event it names. The fan,
// the TicketStock, the ticket count, the card authorization and the referral
// code are the Order's; the fan's 本人確認 (identity check) details are the
// User's full name and phone number.
//
// Mirrors liverty_music.entity.v1.LotteryEntry.
type LotteryEntry struct {
	// ID is the surrogate primary key (UUIDv7).
	ID LotteryEntryID

	// OrderID is the Order the entry applies for; one entry per Order.
	OrderID OrderID

	// TicketSaleID is the Lottery TicketSale entered; it names the event of
	// the Order's TicketStock.
	TicketSaleID TicketSaleID

	// State is the entry's lifecycle state.
	State LotteryEntryState

	// DrawPosition is the entry's zero-based position in its sale's random
	// draw order for its TicketStock; nil before the draw and set only by the
	// draw. It orders the losers for official resale.
	DrawPosition *int

	// LostReason is why the entry lost; unspecified unless State is Lost.
	LostReason LotteryEntryLostReason

	// LossEmailSendTime is when the email telling the fan the entry lost was
	// sent; nil until then.
	LossEmailSendTime *time.Time

	// Order is the Order the entry applies for, read with every entry.
	Order *Order
}

// NewLotteryEntry returns an Entered entry of the sale for order, with a
// generated UUIDv7 id and no draw position, and names it as order's source.
// order carries the card authorization the entry is made from.
func NewLotteryEntry(order *Order, saleID TicketSaleID) *LotteryEntry {
	e := &LotteryEntry{
		ID:           LotteryEntryID(NewID()),
		OrderID:      order.ID,
		TicketSaleID: saleID,
		State:        LotteryEntryStateEntered,
		Order:        order,
	}
	order.LotteryEntryID = e.ID
	return e
}

// ValidateLotteryEntrySale reports whether an entry can belong to sale for
// stock: a Lottery sale that names the stock's event. It fails with
// FailedPrecondition, which callers return as it is.
func ValidateLotteryEntrySale(sale *TicketSale, stock *TicketStock) error {
	if sale == nil {
		return apperr.New(codes.FailedPrecondition, "a lottery entry belongs to a ticket sale")
	}
	if sale.Method != TicketSaleMethodLottery {
		return apperr.New(codes.FailedPrecondition, "a lottery entry belongs to a lottery sale")
	}
	if !sale.NamesEvent(stock.EventID) {
		return apperr.New(codes.FailedPrecondition, "a lottery entry's sale must name the event of its ticket stock")
	}
	return nil
}

// LotteryEntryStats are the counts of one sale's entries for one TicketStock,
// for the Organizer's view of the sale.
type LotteryEntryStats struct {
	// EntryCount is the number of entries.
	EntryCount int

	// TicketCount is the sum of those entries' requested ticket
	// counts.
	TicketCount int

	// WonEntryCount is the number of Won entries.
	WonEntryCount int

	// WonTicketCount is the sum of the Won entries' ticket counts.
	WonTicketCount int

	// LostEntryCount is the number of Lost entries.
	LostEntryCount int
}

// StockDrawOrder is one TicketStock's Entered entries of a sale in their
// draw order: an entry's draw position is its index in Entries. Built by the
// draw with [ShuffleDrawOrder] and consumed by
// [LotteryEntryRepository.PersistDrawOutcome], which admits the entries in
// that order.
type StockDrawOrder struct {
	// TicketStockID is the stock the entries' Orders are of.
	TicketStockID TicketStockID

	// Entries are the stock's entries in draw order, each with its ticket
	// count.
	Entries []DrawEntry
}

// LotteryLossEmail is the finished loss message for one LotteryEntry, already
// rendered in the language of its Order's user.
type LotteryLossEmail struct {
	// Subject is the email subject line.
	Subject string
	// TextBody is the plain-text email body.
	TextBody string
}

// LotteryLossEmailSender sends a Lost entry's loss email at most once. The
// implementation lives in internal/infrastructure/mail/.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type LotteryLossEmailSender interface {
	// SendLossEmail sends msg to the address of the entry's Order's user when
	// the entry has no loss email send time, then sets that time. When the
	// time is already set it sends nothing and succeeds. A failure between
	// sending and recording can lead to a second email on retry; no other
	// case sends twice.
	//
	// # Possible errors
	//
	//   - InvalidArgument: the address or the message is empty.
	//   - Unavailable: email cannot be sent; the send time stays unset.
	//   - NotFound: no entry has the id.
	SendLossEmail(ctx context.Context, id LotteryEntryID, to string, msg LotteryLossEmail) error
}

// LotteryEntryRepository persists [LotteryEntry] records together with their
// Orders. Every read returns the entry with its Order. Implementations live in
// internal/infrastructure/database/rdb/.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type LotteryEntryRepository interface {
	// Create stores the new Entered entry together with its Pending Order
	// (entry.Order, which carries the card authorization) and returns them.
	// When an Order already holds the card authorization, it stores nothing
	// and returns that Order's entry, also after the sale's end time. It locks
	// the user's users row first, so checking and storing are one indivisible
	// step with every other creation of an Order by the user, and no entry is
	// stored once the sale's end time has passed by the database clock.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the entry's Order has no card authorization; nothing
	//    is stored.
	//  - AlreadyExists: the user already has an entry in the sale for the
	//    stock's event, of any stock of that event; nothing is stored.
	//  - FailedPrecondition: the application does not fit the stock's event
	//    ([Event.Fits] with [OrderRepository.GetHeldForEvent]), the sale or
	//    the stock does not exist, or the sale's end time has passed; nothing
	//    is stored.
	//  - NotFound: the user does not exist, also when it was deleted while
	//    the call waited; nothing is stored.
	//  - Unavailable: the wait for a concurrent call or a lock timed out, or
	//    the announcement could not be recorded; nothing is stored.
	//  - Internal: database failure.
	//
	// # Announces
	//
	// LOTTERY_ENTRY.entered with the new entry's Pending Order, which
	// names the entry, the user and the TicketStock, together with the entry.
	// Nothing when it returns an existing entry or fails.
	Create(ctx context.Context, entry *LotteryEntry) (*LotteryEntry, error)

	// Get returns the entry with the given id, in any state.
	//
	// # Possible errors
	//
	//  - NotFound: no entry has the id.
	//  - Internal: database failure.
	Get(ctx context.Context, id LotteryEntryID) (*LotteryEntry, error)

	// GetByCardAuthorizationID returns the entry whose Order's card
	// authorization has the given id.
	//
	// # Possible errors
	//
	//  - NotFound: there is none.
	//  - Internal: database failure.
	GetByCardAuthorizationID(ctx context.Context, cardAuthorizationID string) (*LotteryEntry, error)

	// ListByTicketSaleAndUser returns the user's entries in the sale, at most
	// one for each event the sale names, in every state, newest first. It
	// returns an empty slice when there is none. The name is kept until the
	// rename that follows remodel-ticket-sales (design D11).
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListByTicketSaleAndUser(ctx context.Context, saleID TicketSaleID, userID UserID) ([]*LotteryEntry, error)

	// ListByUser returns every entry whose Order's user is the user, for every
	// sale and in every state, newest first. It returns an empty slice when
	// there is none.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListByUser(ctx context.Context, userID UserID) ([]*LotteryEntry, error)

	// ListEnteredByTicketSaleAndTicketStock returns every Entered entry of the
	// sale whose Order is of the stock, in no particular order: the entries the
	// draw of the stock draws. It returns an empty slice when there is none.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListEnteredByTicketSaleAndTicketStock(ctx context.Context, saleID TicketSaleID, stockID TicketStockID) ([]*LotteryEntry, error)

	// GetStatsByTicketSaleAndTicketStock counts the sale's entries whose Order
	// is of the stock. A stock without entries in the sale yields all counts
	// zero.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	GetStatsByTicketSaleAndTicketStock(ctx context.Context, saleID TicketSaleID, stockID TicketStockID) (LotteryEntryStats, error)

	// PersistDrawOutcome records one sale's draw in one transaction. It takes,
	// for each TicketStock of the sale's events, the stock's Entered entries of
	// the sale in draw order, locks every given stock's row in id order, reads
	// its remaining count under that lock, and walks its entries in that order
	// once ([AdmitInDrawOrder]): an entry whose ticket count fits the
	// remaining count is Won and deducts its whole count, every other entry is
	// Lost with the lost reason not drawn and its Order Void, each with its
	// draw position. A given entry that is no longer Entered is left unchanged
	// and deducts nothing, so recording the same draw again changes nothing,
	// including any entry made Lost since. On failure nothing is recorded.
	//
	// # Possible errors
	//
	//   - InvalidArgument: saleID is empty.
	//   - NotFound: the sale or a given stock does not exist.
	//   - Unavailable: an announcement could not be recorded; nothing
	//     changes.
	//   - Internal: database failure.
	//
	// # Announces
	//
	// LOTTERY_ENTRY.won once per entry it sets Won and LOTTERY_ENTRY.lost once
	// per entry it sets Lost, as it is after the draw, together with the
	// outcome. Nothing for an entry left unchanged.
	PersistDrawOutcome(ctx context.Context, saleID TicketSaleID, orders []StockDrawOrder) error

	// RevertWin makes a Won entry whose Order is Pending Lost with the lost
	// reason, [LotteryEntryLostReasonCardNotCharged] or
	// [LotteryEntryLostReasonConcertCancelled], keeping its draw position, and
	// makes its Order Void, in one transaction under the row lock of the
	// Order's TicketStock, so its tickets stay unallocated. An entry already
	// Lost is left as it is.
	//
	// # Possible errors
	//
	//   - FailedPrecondition: the entry is Entered, or its Order is Paid or
	//     Refunded; nothing changes.
	//   - NotFound: no entry has the id.
	//   - Unavailable: the announcement cannot be recorded; nothing changes.
	//   - Internal: database failure.
	//
	// # Announces
	//
	// LOTTERY_ENTRY.lost with the entry as it is after the change, Lost with
	// the given lost reason, together with the change. Nothing when
	// the entry was already Lost.
	RevertWin(ctx context.Context, id LotteryEntryID, reason LotteryEntryLostReason) error

	// EndEnteredByEvent makes every Entered entry whose Order's TicketStock
	// belongs to the event Lost with the lost reason
	// [LotteryEntryLostReasonConcertCancelled], with no draw position, and
	// makes each of their Orders Void, in one transaction under the row locks
	// of the event's TicketStocks taken in id order, the order
	// PersistDrawOutcome takes them in, so an entry a draw records at the same
	// moment is either recorded by the draw or ended here. Won and Lost
	// entries, and the entries of other events, are left as they are. It
	// succeeds and changes nothing when the event has no Entered entry,
	// including when no event has the id.
	//
	// # Possible errors
	//
	//   - Unavailable: an announcement cannot be recorded; nothing changes.
	//   - Internal: database failure.
	//
	// # Announces
	//
	// LOTTERY_ENTRY.lost once per entry it makes Lost, with the entry
	// as it is after the change, together with the change. Nothing when no
	// entry changed.
	EndEnteredByEvent(ctx context.Context, eventID string) error

	// MarkLossEmailSent sets the entry's loss email send time when it is
	// unset; an already set time is kept.
	//
	// # Possible errors
	//
	//   - NotFound: no entry has the id.
	//   - Internal: database failure.
	MarkLossEmailSent(ctx context.Context, id LotteryEntryID, at time.Time) error
}
