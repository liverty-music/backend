package entity

import "time"

// Event subject constants for the domain events announced through the outbox.
//
// An event is named <Entity>.<What> in the specs (LotteryEntry.Won), its
// subject <ENTITY>.<what> (LOTTERY_ENTRY.won) and its constant
// Subject<Entity><What>. Each subject has two tokens, so the stream named
// after the entity captures it with <ENTITY>.* (messaging streams). The
// analytics consumer forwards some of them to PostHog under the subject in
// lower case (usecase.AnalyticsEventName).
const (
	// SubjectConcertCreated announces new concerts as created
	// (Concert.Created): Concert.Create once per Series and newly listed
	// Artist, and Series.PublishEvents once per performing Artist of the
	// Events it published, each carrying those Concerts.
	SubjectConcertCreated = "CONCERT.created"
	// SubjectEventCancelled announces an Event as cancelled (Event.Cancelled):
	// Series.CancelEvents, once per Event it made CANCELLED, with the change.
	SubjectEventCancelled = "EVENT.cancelled"
	// SubjectArtistCreated announces an Artist as created (Artist.Created):
	// Artist.Create, once per artist it registers.
	SubjectArtistCreated = "ARTIST.created"
	// SubjectArtistRepresented announces an Artist as represented
	// (Artist.Represented): Organizer.AssociateArtist, when it links the
	// Artist to the Organizer, with the Organizer as its organizer.
	SubjectArtistRepresented = "ARTIST.represented"
	// SubjectFollowCreated announces a Follow as created (Follow.Created):
	// Follow.Follow, when it stores a new Follow, at hype level Nearby.
	SubjectFollowCreated = "FOLLOW.created"
	// SubjectFollowRemoved announces a Follow as removed (Follow.Removed):
	// Follow.Unfollow, carrying the Follow as it was.
	SubjectFollowRemoved = "FOLLOW.removed"
	// SubjectFollowHypeChanged announces a Follow's hype as changed
	// (Follow.HypeChanged), with its new level and who set it: Follow.SetHype
	// (the fan) and Follow.RaiseHype (inference).
	SubjectFollowHypeChanged = "FOLLOW.hype_changed"
	// SubjectUserCreated announces a User as created (User.Created):
	// User.Create, when it stores a new User.
	SubjectUserCreated = "USER.created"
	// SubjectUserSignedIn announces a User as signed in (User.SignedIn):
	// User.RecordLogin, when it changes the last login time, with the new one.
	// The login-event webhook records only user-initiated logins (Zitadel
	// session.user.checked); a silent token refresh never reaches it.
	SubjectUserSignedIn = "USER.signed_in"
	// SubjectPasskeyAdded announces a Passkey as added (Passkey.Added):
	// Passkey.Create, carrying the stored Passkey.
	SubjectPasskeyAdded = "PASSKEY.added"
	// SubjectPasskeyRemoved announces a Passkey as removed (Passkey.Removed):
	// Passkey.Delete, carrying the Passkey as it was stored.
	SubjectPasskeyRemoved = "PASSKEY.removed"
	// SubjectPushSubscriptionSubscribed announces a PushSubscription as
	// subscribed (PushSubscription.Subscribed): PushSubscription.Create, when
	// it stores a new one or changes the keys or the owner of one, carrying
	// it after the change.
	SubjectPushSubscriptionSubscribed = "PUSH_SUBSCRIPTION.subscribed"
	// SubjectPushSubscriptionUnsubscribed announces a PushSubscription as
	// unsubscribed (PushSubscription.Unsubscribed): PushSubscription.Delete,
	// carrying it as it was.
	SubjectPushSubscriptionUnsubscribed = "PUSH_SUBSCRIPTION.unsubscribed"
	// SubjectTicketSaleDiscovered announces a TicketSale as discovered
	// (TicketSale.Discovered): TicketSale.UpsertDiscovered, when it creates a
	// discovered sale (UpsertOutcomeInserted). An update of a known one
	// announces nothing.
	SubjectTicketSaleDiscovered = "TICKET_SALE.discovered"
	// SubjectTicketJourneyStatusChanged announces a journey's status as
	// changed (TicketJourney.StatusChanged): TicketJourney.Upsert and
	// TicketJourney.UpsertIfReplaceable, carrying the journey with its status
	// and who set it. One event covers every status: its one consumer treats
	// them the same.
	SubjectTicketJourneyStatusChanged = "TICKET_JOURNEY.status_changed"
	// SubjectOrganizerActivated announces an Organizer as activated
	// (Organizer.Activated): Organizer.CompareAndSetStatus, when it moves the
	// Organizer from provisioning to active.
	SubjectOrganizerActivated = "ORGANIZER.activated"
	// SubjectSeriesImageUploaded announces a SeriesImage as uploaded
	// (SeriesImage.Uploaded), the request to process it: SeriesImage.Create,
	// with the SeriesImage in the payload and the Series in ce_subject.
	SubjectSeriesImageUploaded = "SERIES_IMAGE.uploaded"
	// SubjectOrderPaid announces an Order as paid (Order.Paid): Order.Issue,
	// carrying it in status Paid, for a won lottery or resale entry or a
	// completed checkout.
	SubjectOrderPaid = "ORDER.paid"
	// SubjectOrderRefunded announces an Order as refunded (Order.Refunded):
	// Order.CommitRefund, with the refund.
	SubjectOrderRefunded = "ORDER.refunded"
	// SubjectResaleListingSold announces a ResaleListing as sold
	// (ResaleListing.Sold): Order.Issue, for each listing a won resale entry's
	// Order buys.
	SubjectResaleListingSold = "RESALE_LISTING.sold"
	// SubjectResaleListingRefunded announces a ResaleListing as refunded
	// (ResaleListing.Refunded): ResaleListing.RecordRefund, with the refund
	// its user was paid from the listing's Order's charge.
	SubjectResaleListingRefunded = "RESALE_LISTING.refunded"
	// SubjectLotteryEntryEntered announces a lottery entry as entered
	// (LotteryEntry.Entered): LotteryEntry.Create, carrying the entry's
	// Pending Order.
	SubjectLotteryEntryEntered = "LOTTERY_ENTRY.entered"
	// SubjectLotteryEntryWon announces a lottery entry as won
	// (LotteryEntry.Won): LotteryEntry.PersistDrawOutcome, with the entry as
	// it is after the draw.
	SubjectLotteryEntryWon = "LOTTERY_ENTRY.won"
	// SubjectLotteryEntryLost announces a lottery entry as lost
	// (LotteryEntry.Lost), with the entry as it is after the change:
	// LotteryEntry.PersistDrawOutcome (not drawn), LotteryEntry.RevertWin and
	// LotteryEntry.EndEnteredByEvent (concert cancelled).
	SubjectLotteryEntryLost = "LOTTERY_ENTRY.lost"
	// SubjectResaleEntryWon announces a resale entry as won (ResaleEntry.Won):
	// ResaleEntry.PersistDrawOutcome.
	SubjectResaleEntryWon = "RESALE_ENTRY.won"
	// SubjectResaleEntryLapsed announces a resale entry as lapsed
	// (ResaleEntry.Lapsed): ResaleEntry.PersistDrawOutcome.
	SubjectResaleEntryLapsed = "RESALE_ENTRY.lapsed"
	// SubjectResaleEntryLost announces a resale entry as lost
	// (ResaleEntry.Lost): ResaleEntry.PersistDrawOutcome and
	// ResaleEntry.RevertWin.
	SubjectResaleEntryLost = "RESALE_ENTRY.lost"
	// SubjectResaleEntryWithdrawn announces a resale entry as withdrawn
	// (ResaleEntry.Withdrawn): ResaleEntry.Withdraw.
	SubjectResaleEntryWithdrawn = "RESALE_ENTRY.withdrawn"
	// SubjectCheckoutHeld announces a Checkout as held (Checkout.Held):
	// Checkout.GetOrCreateHeld, when it creates a Held Checkout.
	SubjectCheckoutHeld = "CHECKOUT.held"
	// SubjectCheckoutExpired announces a Checkout as expired
	// (Checkout.Expired), carrying it as Expired: Checkout.Release and
	// Checkout.GetOrCreateHeld.
	SubjectCheckoutExpired = "CHECKOUT.expired"
	// SubjectCheckoutReleased announces a Checkout as released
	// (Checkout.Released), carrying it as Released: Checkout.GetOrCreateHeld
	// (a replaced hold) and Checkout.RevertCommit.
	SubjectCheckoutReleased = "CHECKOUT.released"
	// SubjectTicketAdmitted announces a Ticket as admitted (Ticket.Admitted):
	// Ticket.Admit, carrying it with its admit time.
	SubjectTicketAdmitted = "TICKET.admitted"
)

// AllSubjects is the canonical catalogue of every domain-event NATS subject
// the system publishes and consumes. It is the single source of truth for
// coverage checks: a subject added above MUST be added here too, and the
// stream-coverage test then guarantees each one is captured by a configured
// JetStream stream (see messaging.SubjectCoveredByStream). This closes the
// recurring "added a publisher/subscription without its paired stream" gap
// that fails a consumer at startup with "no stream matches subject".
var AllSubjects = []string{
	SubjectConcertCreated,
	SubjectEventCancelled,
	SubjectArtistCreated,
	SubjectArtistRepresented,
	SubjectFollowCreated,
	SubjectFollowRemoved,
	SubjectFollowHypeChanged,
	SubjectUserCreated,
	SubjectUserSignedIn,
	SubjectPasskeyAdded,
	SubjectPasskeyRemoved,
	SubjectPushSubscriptionSubscribed,
	SubjectPushSubscriptionUnsubscribed,
	SubjectTicketSaleDiscovered,
	SubjectTicketJourneyStatusChanged,
	SubjectOrganizerActivated,
	SubjectSeriesImageUploaded,
	SubjectOrderPaid,
	SubjectOrderRefunded,
	SubjectResaleListingSold,
	SubjectResaleListingRefunded,
	SubjectLotteryEntryEntered,
	SubjectLotteryEntryWon,
	SubjectLotteryEntryLost,
	SubjectResaleEntryWon,
	SubjectResaleEntryLapsed,
	SubjectResaleEntryLost,
	SubjectResaleEntryWithdrawn,
	SubjectCheckoutHeld,
	SubjectCheckoutExpired,
	SubjectCheckoutReleased,
	SubjectTicketAdmitted,
}

// LotteryEntryEnteredData is what the receiver of an
// LOTTERY_ENTRY.entered announcement reads from the new entry's Pending
// Order and the event of its TicketStock.
type LotteryEntryEnteredData struct {
	// LotteryEntryID is the new entry, which names the Order.
	LotteryEntryID LotteryEntryID
	// UserID is the platform-internal id of the Order's user.
	UserID string
	// EventID is the event of the Order's TicketStock.
	EventID string
}

// OrderPaidData is what the receivers of an ORDER.paid announcement read
// from the paid Order and the event of its TicketStock. At most one of
// LotteryEntryID, CheckoutID and ResaleEntryID is set: the Order's source.
type OrderPaidData struct {
	// OrderID is the paid Order; consumers are idempotent by it.
	OrderID string
	// UserID is the platform-internal id of the Order's user.
	UserID string
	// EventID is the event the Order's tickets admit to.
	EventID string
	// TicketCount is the number of Tickets the Order issued.
	TicketCount int
	// Amount is the Order's amount in the currency's smallest unit (yen).
	Amount int64
	// Currency is the ISO 4217 code of Amount.
	Currency string
	// LotteryEntryID is the won lottery entry, when that is the source.
	LotteryEntryID string
	// CheckoutID is the completed checkout, when that is the source.
	CheckoutID string
	// ResaleEntryID is the won resale entry, when that is the source.
	ResaleEntryID string
	// PayTime is the Order's pay time, the time the fact happened (the
	// analytics event time).
	PayTime time.Time
}

// ResaleListingRefundedData is what the receiver of an
// RESALE_LISTING.refunded announcement reads from the refunded
// ResaleListing: ids only; the rest is read by id.
type ResaleListingRefundedData struct {
	// ResaleListingID is the refunded listing; consumers are idempotent by it.
	ResaleListingID string
	// UserID is the platform-internal id of the listing's user.
	UserID string
	// OrderID is the Order whose charge the user was refunded from.
	OrderID string
	// EventID is the event the listed Ticket admitted to.
	EventID string
}

// SumSeriesEvents returns the total number of member events across the given
// discovered series, skipping nil entries. It is the single fold used wherever a
// flat event count over a []*DiscoveredSeries is needed (payload EventCount and
// the discovery use case's filter bookkeeping) so the two cannot drift.
func SumSeriesEvents(series []*DiscoveredSeries) int {
	n := 0
	for _, s := range series {
		if s == nil {
			continue
		}
		n += len(s.Events)
	}
	return n
}

// TicketSaleDiscoveredData is what the receiver of a TICKET_SALE.discovered
// announcement reads from the new discovered TicketSale. It is announced only
// when a new discovered TicketSale is created (UpsertOutcomeInserted); an
// update of a known one announces nothing.
type TicketSaleDiscoveredData struct {
	// TicketSaleID is the id of the newly created discovered TicketSale.
	TicketSaleID string
	// SeriesID is the sale's series. The announcement resolves its audience
	// from the Tracking ticket journeys on this series' events.
	SeriesID string
	// Method is the sale's TicketSaleMethod value; the announcement copy
	// depends on it.
	Method int16
	// SaleStartTime is when the sale opens; the announcement names it.
	SaleStartTime time.Time
}

// TicketSaleReminderDueData is the input of the deliver-reminder workflow,
// started by the reminder scan for each (user, ticket sale, stage) that became
// due in this scan window. Its JSON names, phase_id among them, are those of
// the inputs already recorded.
type TicketSaleReminderDueData struct {
	// UserID is the recipient.
	UserID string `json:"user_id"`
	// TicketSaleID is the id of the TicketSale, discovered or an Organizer's.
	TicketSaleID string `json:"phase_id"`
	// Stage is the reminder stage (ReminderStage int16 value).
	Stage int16 `json:"stage"`
	// LinkEventID is the event of the sale the fan tracks; the reminder links
	// to it.
	LinkEventID string `json:"link_event_id"`
}
