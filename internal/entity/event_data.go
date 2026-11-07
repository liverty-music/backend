package entity

import "time"

// Event subject constants for domain events published via messaging.
//
// Subjects follow the UPPERCASE two-segment convention enforced by the
// pre-existing JetStream stream config (CONCERT.*, ARTIST.*, USER.*,
// VENUE.*, POISON.*). The analytics-consumer maps each subject to a
// lowercase catalogue event name (see specification/docs/analytics/
// event-catalog.md) at the Handle method that subscribes to it.
const (
	SubjectConcertCreated           = "CONCERT.created"
	SubjectArtistCreated            = "ARTIST.created"
	SubjectArtistFollowed           = "ARTIST.followed"
	SubjectArtistUnfollowed         = "ARTIST.unfollowed"
	SubjectUserCreated              = "USER.created"
	SubjectNotificationSubscribed   = "NOTIFICATION.subscribed"
	SubjectNotificationUnsubscribed = "NOTIFICATION.unsubscribed"
	// SubjectTicketSaleDiscovered is announced by TicketSale.UpsertDiscovered
	// when a new discovered TicketSale is created. Re-discovery of a known one
	// (UpsertOutcomeUpdated) announces nothing. The subject keeps
	// the stream's former name (design "Non-Goals").
	SubjectTicketSaleDiscovered = "SALES_PHASE.discovered"
	// SubjectTicketJourneyStatusChanged is announced by TicketJourney.Upsert
	// when the stored status changes (or no journey existed). It drives the
	// ticket.journey.status.changed analytics event.
	SubjectTicketJourneyStatusChanged = "TICKET_JOURNEY.status_changed"
	// SubjectUserLoggedIn is announced by User.RecordLogin for each
	// user-initiated login the login-event webhook records, bound to the
	// Zitadel session.user.checked event. It drives the account.signin
	// analytics event (returning / active-user retention cohorts). The OIDC refresh_token grant touches only
	// the oidc_session aggregate and never emits session.user.checked, so this
	// subject is never published on a silent token refresh — login-specific by
	// construction. Modelled as a USER-domain event (USER.* stream), not a
	// separate ACCOUNT stream.
	SubjectUserLoggedIn = "USER.logged_in"
	// SubjectOrganizerCreated is announced by Organizer.CompareAndSetStatus when
	// the organizer row is flipped from provisioning to active.
	// It drives the organizer.created analytics event (admin-actor / group event
	// keyed on organizer_id; no fan distinct_id). Two tokens (created, not
	// org.created) so the ORGANIZER.* stream filter matches with a plain
	// single-token wildcard — same rationale as SubjectTicketSaleDiscovered.
	SubjectOrganizerCreated = "ORGANIZER.created"
	// SubjectOrganizerArtistAssociated is announced by Organizer.AssociateArtist
	// when it stores a new artist link. It drives the
	// organizer.artist.associated analytics event (admin-actor / group event
	// keyed on organizer_id; no fan distinct_id). The event token uses an
	// underscore (artist_associated, not artist.associated) so the subject stays
	// two tokens and the plain ORGANIZER.* filter on the JetStream stream
	// captures it without a deeper wildcard.
	SubjectOrganizerArtistAssociated = "ORGANIZER.artist_associated"
	// SubjectSeriesImageUploaded is the processing request SeriesImage.Create
	// records with the series image row: the SeriesImage in the payload and the
	// Series in ce_subject. The series-image-consumer subscribes to this subject to
	// read the original from the originals bucket, generate WebP variants, and
	// cut over series_cover_images to the new series image id. Two tokens (uploaded, not
	// series_image.uploaded) so the SERIES_IMAGE.* stream filter captures it with a plain
	// single-token wildcard — same convention as SubjectTicketSaleDiscovered.
	SubjectSeriesImageUploaded = "SERIES_IMAGE.uploaded"
	// SubjectOrderPaid announces that an Order is paid, for a won lottery
	// entry or a completed checkout. OrderUseCase.FulfillPayment
	// publishes it on the payment provider's completed-charge webhook; a
	// repeated webhook announces the Order again as a new event, and the
	// consumers are idempotent by the Order id. Two consumers run from
	// it: the purchase confirmation (NotificationUseCase.SendOrderConfirmation)
	// and the ticket journey (TicketJourneyUseCase.MarkPaid).
	SubjectOrderPaid = "ORDER.paid"
	// SubjectResaleListingSold announces a ResaleListing that Order.Issue made
	// Sold for a won resale entry, with the listing as Sold, in the
	// transaction of the issuance. The workflow starter starts
	// ResaleListingUseCase.RefundSold (refund:<listing>) from it (design D10).
	SubjectResaleListingSold = "ORDER.resale_listing_sold"
	// SubjectOrderRefunded announces an Order that Order.CommitRefund made
	// Refunded, with the Order and its refund, once, in the transaction of
	// the refund. The refund notice (NotificationUseCase.SendOrderRefunded)
	// runs from it.
	SubjectOrderRefunded = "ORDER.refunded"
	// SubjectConcertCancelled announces an Event that Series.CancelEvents
	// made CANCELLED, with the Event, once per Event, in the transaction of
	// the cancellation. The cancellation notice
	// (NotificationUseCase.SendEventCancelled) runs from it.
	SubjectConcertCancelled = "CONCERT.cancelled"
	// SubjectResaleListingRefunded announces that the user of a resold
	// ResaleListing was refunded from the listing's Order's charge.
	// ResaleListing.RecordRefund announces it once, with the refund; the
	// consumer is idempotent by the listing id and waits out an unavailable
	// mail service. The sold email consumer (NotificationUseCase.SendResaleSold) runs from it.
	SubjectResaleListingRefunded = "ORDER.resale_listing_refunded"
	// SubjectUserPasskeyAdded announces a Passkey that Passkey.Create stored.
	// UserUseCase.NotifyPasskeyChange runs from it.
	SubjectUserPasskeyAdded = "USER.passkey_added"
	// SubjectUserPasskeyRemoved announces a Passkey that Passkey.Delete
	// removed, as it was. UserUseCase.NotifyPasskeyChange runs from it.
	SubjectUserPasskeyRemoved = "USER.passkey_removed"
	// SubjectArtistHypeChanged announces a changed hype level of a Follow,
	// with who set it: Follow.SetHype (the fan) and Follow.RaiseHype
	// (inference) record it with the change; the analytics consumer forwards
	// it as artist.hype.changed. The name is kept for the existing durable
	// track-artist-hype-changed.
	SubjectArtistHypeChanged = "ARTIST.hype_changed"
	// SubjectLotteryEntryDrawn announces one entry's draw result (Won or
	// Lost). LotteryEntry.PersistDrawOutcome and LotteryEntry.RevertWin
	// announce it with the change; the issue and release workflows start from
	// it, the loss notice is sent from it, and the analytics consumer forwards
	// it as ticket.lottery.result.
	SubjectLotteryEntryDrawn = "ORDER.lottery_entry_drawn"
	// SubjectResaleEntryChanged announces a resale entry that a draw made
	// Won, Lapsed or Lost, that RevertWin made Lost or that the fan withdrew,
	// with the entry as the payload; its state tells them apart. The workflow
	// starter starts the issue of a Won entry and the release of the others
	// from it, and the ended message of a Lapsed or Lost entry is sent from it
	// (design D4, D6, D8).
	SubjectResaleEntryChanged = "ORDER.resale_entry_changed"
	// SubjectLotteryEntryEntered announces a new lottery entry, with its
	// Pending Order as the payload. LotteryEntry.Create records it in the
	// transaction that stores the entry; TicketJourneyUseCase.MarkApplied
	// receives it and records the fan's journey as Applied.
	SubjectLotteryEntryEntered = "ORDER.lottery_entry_entered"
	// SubjectCheckoutHeld announces a new Held Checkout, with the Checkout as
	// the payload. Checkout.GetOrCreateHeld records it with the creation;
	// CheckoutUseCase.Expire is started from it.
	SubjectCheckoutHeld = "ORDER.checkout_held"
	// SubjectCheckoutExpired announces that a Held Checkout's hold lapsed,
	// with the Checkout as Expired. Checkout.Release and
	// Checkout.GetOrCreateHeld record it with the change; the release of its
	// Order's card authorization is started from it, and the analytics
	// consumer forwards it as checkout.expired.
	SubjectCheckoutExpired = "ORDER.checkout_expired"
	// SubjectCheckoutReleased announces a Checkout ended without a charge, with
	// the Checkout as Released. Checkout.GetOrCreateHeld (a replaced hold) and
	// Checkout.RevertCommit record it with the change; the release of its
	// Order's card authorization is started from it.
	SubjectCheckoutReleased = "ORDER.checkout_released"
	// SubjectTicketAdmitted announces that a scan admitted a Ticket.
	// TicketUseCase.Admit publishes it once per admitted Ticket; the analytics
	// consumer forwards it as ticket.admitted.
	SubjectTicketAdmitted = "ORDER.ticket_admitted"
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
	SubjectArtistCreated,
	SubjectArtistFollowed,
	SubjectArtistUnfollowed,
	SubjectUserCreated,
	SubjectNotificationSubscribed,
	SubjectNotificationUnsubscribed,
	SubjectTicketSaleDiscovered,
	SubjectTicketJourneyStatusChanged,
	SubjectUserLoggedIn,
	SubjectOrganizerCreated,
	SubjectOrganizerArtistAssociated,
	SubjectSeriesImageUploaded,
	SubjectOrderPaid,
	SubjectResaleListingRefunded,
	SubjectUserPasskeyAdded,
	SubjectUserPasskeyRemoved,
	SubjectArtistHypeChanged,
	SubjectLotteryEntryDrawn,
	SubjectLotteryEntryEntered,
	SubjectCheckoutHeld,
	SubjectCheckoutExpired,
	SubjectCheckoutReleased,
	SubjectTicketAdmitted,
	SubjectOrderRefunded,
	SubjectConcertCancelled,
}

// LotteryEntryEnteredData is what the receiver of an
// ORDER.lottery_entry_entered announcement reads from the new entry's Pending
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
// LotteryEntryID and CheckoutID is set: the Order's source.
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
	// PayTime is the Order's pay time, the time the fact happened (the
	// analytics event time).
	PayTime time.Time
}

// ResaleListingRefundedData is what the receiver of an
// ORDER.resale_listing_refunded announcement reads from the refunded
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

// TicketSaleDiscoveredData is what the receiver of a SALES_PHASE.discovered
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
