package usecase

// AnalyticsEventName identifies a product-analytics event recorded against a
// user's behavioral profile in PostHog.
//
// Every event emitted from the backend MUST use a constant defined here.
// Event names follow the convention domain.action[.outcome] in dot.case;
// see specification/docs/analytics/event-catalog.md for the catalogue and
// per-event property contracts.
//
// Frontend-only events are NOT listed here; they live in
// frontend/src/services/analytics-events.ts. Trust-critical events whose
// accuracy must survive client tampering (ticket purchases, ZK proof
// verification, notification delivery, account state changes) are emitted
// only from the backend through these constants.
type AnalyticsEventName string

// Events forwarded by the analytics consumer from domain events. Each is
// named by its subject in lower case (USER.signed_in is user.signed_in), so a
// PostHog event, its subject and its event in the specs share one name.
const (
	// EventUserCreated is recorded for each USER.created announcement: a User
	// the backend created, through self-signup or admin provisioning.
	// Properties: signup_month.
	EventUserCreated AnalyticsEventName = "user.created"

	// EventUserSignedIn is recorded for each USER.signed_in announcement, at
	// the User's last login time: one user-initiated sign-in (Zitadel
	// session.user.checked), never a silent token refresh, so it is a
	// returning/active-user retention signal. No properties.
	EventUserSignedIn AnalyticsEventName = "user.signed_in"

	// EventFollowCreated is recorded for each FOLLOW.created announcement: a
	// Follow the backend stored. The frontend emits a paired
	// artist.follow.requested event at the moment the user submits the
	// action. Properties: artist_id.
	EventFollowCreated AnalyticsEventName = "follow.created"

	// EventFollowRemoved is recorded for each FOLLOW.removed announcement: a
	// Follow the backend removed. Properties: artist_id.
	EventFollowRemoved AnalyticsEventName = "follow.removed"

	// EventFollowHypeChanged is recorded for each FOLLOW.hype_changed
	// announcement: a hype level the fan set (also the level the Follow
	// already had when the fan had not set it) or the app raised.
	// Properties: artist_id, hype, set_by.
	EventFollowHypeChanged AnalyticsEventName = "follow.hype_changed"

	// EventTicketJourneyStatusChanged is recorded for each
	// TICKET_JOURNEY.status_changed announcement: a journey whose status or
	// who set it changed, by the fan, the platform or inference. A write that
	// changes neither announces nothing. Properties: event_id, to_status,
	// set_by.
	EventTicketJourneyStatusChanged AnalyticsEventName = "ticket_journey.status_changed"

	// EventPushSubscriptionSubscribed is recorded for each
	// PUSH_SUBSCRIPTION.subscribed announcement: a Web Push subscription
	// stored against the user. Properties: device_type.
	EventPushSubscriptionSubscribed AnalyticsEventName = "push_subscription.subscribed"

	// EventPushSubscriptionUnsubscribed is recorded for each
	// PUSH_SUBSCRIPTION.unsubscribed announcement: a Web Push subscription
	// the user removed. Auto-cleanup on 410 Gone (send-loop expiry) announces
	// nothing: that is subscription expiry, not user churn.
	// Properties: device_type.
	EventPushSubscriptionUnsubscribed AnalyticsEventName = "push_subscription.unsubscribed"

	// EventOrganizerActivated is recorded for each ORGANIZER.activated
	// announcement: an Organizer whose Organization is fully provisioned. It
	// is an admin-actor / group event keyed on organizer_id in PostHog; no
	// fan distinct_id is present. Properties: organizer_id.
	EventOrganizerActivated AnalyticsEventName = "organizer.activated"

	// EventArtistRepresented is recorded for each ARTIST.represented
	// announcement: an Artist linked to an Organizer. It is an admin-actor /
	// group event keyed on organizer_id in PostHog; no fan distinct_id is
	// present. Properties: organizer_id, artist_id.
	EventArtistRepresented AnalyticsEventName = "artist.represented"

	// EventOrderPaid is recorded for each ORDER.paid announcement, with the
	// CloudEvent id (a UUIDv7) as the event id: a redelivery of the same
	// message is stored once, but a charge reported again announces a new
	// message and records a second event.
	// Properties: order_id, event_id, ticket_count, amount, currency, source
	// ("lottery_entry" or "checkout"), lottery_entry_id or checkout_id.
	EventOrderPaid AnalyticsEventName = "order.paid"

	// EventLotteryEntryWon is recorded for each LOTTERY_ENTRY.won
	// announcement. Properties: lottery_entry_id, ticket_sale_id,
	// ticket_stock_id, result ("won"), capture_failed.
	EventLotteryEntryWon AnalyticsEventName = "lottery_entry.won"

	// EventLotteryEntryLost is recorded for each LOTTERY_ENTRY.lost
	// announcement: an entry not drawn, whose win was reverted, or ended by a
	// cancelled concert. Properties: lottery_entry_id, ticket_sale_id,
	// ticket_stock_id, result ("lost"), capture_failed.
	EventLotteryEntryLost AnalyticsEventName = "lottery_entry.lost"

	// EventCheckoutExpired is recorded for each CHECKOUT.expired
	// announcement: a Checkout whose hold lapsed. Properties: checkout_id,
	// ticket_stock_id, ticket_count.
	EventCheckoutExpired AnalyticsEventName = "checkout.expired"

	// EventTicketAdmitted is recorded for each TICKET.admitted announcement:
	// a Ticket a scan admitted. Properties: ticket_id, event_id.
	EventTicketAdmitted AnalyticsEventName = "ticket.admitted"
)

// Organizer events the backend records directly, not from a domain event.
const (
	// EventOrganizerConcertPublished is recorded after an organizer successfully
	// publishes a first-party series. It is an admin-actor / group event keyed
	// on organizer_id in PostHog; no fan distinct_id is present.
	// Properties: organizer_id, series_id, new_event_count.
	EventOrganizerConcertPublished AnalyticsEventName = "organizer.concert.published"
)

// Pipeline events without a fan, sent as personless AnalyticsClient.Capture events.
const (
	// EventConcertSearchCompleted is recorded once per SearchNewConcerts run
	// that performed the external search. Properties: artist_id, trigger,
	// outcome ("failed", "nothing_new" or "found"), series_count,
	// event_count, and on a failed search error_code (the error's apperr
	// code, or "unknown").
	EventConcertSearchCompleted AnalyticsEventName = "concert.search.completed"

	// EventConcertIngestCompleted is recorded once per CreateFromDiscovered
	// batch, a failed one included with what it had done before failing.
	// Properties: artist_id, outcome ("completed" or "failed"),
	// published_count, staged_count, filled_count, suppressed_count,
	// conflict_count.
	EventConcertIngestCompleted AnalyticsEventName = "concert.ingest.completed"

	// EventTicketSaleSearchCompleted is recorded once per DiscoverForArtist
	// run that called the published-sale search. Properties: artist_id,
	// outcome ("failed" or "completed"), series_count, returned_count,
	// new_count, and on a failed search error_code (the error's apperr
	// code, or "unknown").
	EventTicketSaleSearchCompleted AnalyticsEventName = "ticket_sale.search.completed"

	// EventAIGeneration is PostHog's LLM-analytics generation event, recorded
	// once per request to the external search model.
	EventAIGeneration AnalyticsEventName = "$ai_generation"
)

// knownBackendEvents is the allowlist of AnalyticsEventName constants
// emitted from the backend. AnalyticsClient implementations consult this
// via IsKnownEvent to reject typos at the call site, satisfying the
// "unknown Name" contract on Capture.
//
// The map is populated lazily-once at package init via the const groups
// above; every new constant MUST be added here in the same commit.
var knownBackendEvents = map[AnalyticsEventName]struct{}{
	EventUserCreated:                  {},
	EventUserSignedIn:                 {},
	EventFollowCreated:                {},
	EventFollowRemoved:                {},
	EventFollowHypeChanged:            {},
	EventTicketJourneyStatusChanged:   {},
	EventPushSubscriptionSubscribed:   {},
	EventPushSubscriptionUnsubscribed: {},
	EventOrganizerActivated:           {},
	EventArtistRepresented:            {},
	EventOrderPaid:                    {},
	EventLotteryEntryWon:              {},
	EventLotteryEntryLost:             {},
	EventCheckoutExpired:              {},
	EventTicketAdmitted:               {},
	EventOrganizerConcertPublished:    {},
	EventConcertSearchCompleted:       {},
	EventConcertIngestCompleted:       {},
	EventTicketSaleSearchCompleted:    {},
	EventAIGeneration:                 {},
}

// IsKnownEvent reports whether name is registered in the backend event
// catalogue. AnalyticsClient implementations call this on Capture to
// fail fast on typos like AnalyticsEventName("ticket.purcase.completed")
// that would otherwise silently fragment downstream dashboards.
func IsKnownEvent(name AnalyticsEventName) bool {
	_, ok := knownBackendEvents[name]
	return ok
}
