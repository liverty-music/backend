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

// Account lifecycle events emitted from the backend.
const (
	// EventAccountSignin is recorded once per user-initiated sign-in, sourced
	// from the Zitadel session.user.checked Actions v2 event webhook (NATS
	// transport subject ACCOUNT.login). It is never recorded on a silent token
	// refresh (which touches only the oidc_session aggregate), so it is a
	// returning/active-user retention signal. Signup is represented by
	// EventUserCreated, not a separate account.signup.completed event.
	//
	// Named account.signin (renamed from account.login while it had zero
	// production history) for vocabulary consistency with the account.* namespace.
	EventAccountSignin AnalyticsEventName = "account.signin"

	// EventUserCreated is recorded when the backend creates a User record
	// for the first time, either through self-signup or admin provisioning.
	EventUserCreated AnalyticsEventName = "user.created"
)

// Artist engagement events emitted from the backend after persistence.
const (
	// EventArtistFollowCompleted is recorded after a follow relationship
	// has been persisted. The frontend emits a paired artist.follow.requested
	// event at the moment the user submits the action.
	EventArtistFollowCompleted AnalyticsEventName = "artist.follow.completed"

	// EventArtistUnfollowCompleted is recorded after the corresponding
	// follow relationship has been removed.
	EventArtistUnfollowCompleted AnalyticsEventName = "artist.unfollow.completed"
)

// Ticket journey events emitted from the backend.
const (
	// EventTicketJourneyStatusChanged is recorded after a fan's ticket
	// journey status is successfully updated via SetStatus. It is
	// suppressed when the incoming status equals the stored status
	// (no-op upsert) to avoid noise in downstream funnels.
	// Properties: event_id, from_status, to_status.
	EventTicketJourneyStatusChanged AnalyticsEventName = "ticket.journey.status.changed"
)

// Notification lifecycle events emitted from the backend. The underlying
// transport is the W3C Push API, but the analytics surface is scoped under
// the notification domain to align with the user-facing concept — see
// specification/docs/analytics/event-catalog.md.
const (
	// EventNotificationSubscribed is recorded after a Web Push
	// subscription has been persisted against the user record.
	EventNotificationSubscribed AnalyticsEventName = "notification.subscribed"

	// EventNotificationUnsubscribed is recorded after a user-initiated
	// Web Push subscription has been removed via PushSubscriptionUseCase.Delete.
	// Auto-cleanup on 410 Gone (send-loop expiry) intentionally does NOT
	// emit this event — that is subscription expiry, not user churn.
	EventNotificationUnsubscribed AnalyticsEventName = "notification.unsubscribed"

	// EventNotificationDelivered is recorded when the push provider
	// accepts a notification for delivery. The frontend records the
	// downstream open/dismiss separately.
	EventNotificationDelivered AnalyticsEventName = "notification.delivered"
)

// Organizer lifecycle events emitted from the backend.
const (
	// EventOrganizerCreated is recorded after an Organizer's Organization is fully
	// provisioned and the row flips to active. It is an admin-actor / group
	// event keyed on organizer_id in PostHog; no fan distinct_id is present.
	// Properties: organizer_id.
	EventOrganizerCreated AnalyticsEventName = "organizer.created"

	// EventOrganizerArtistAssociated is recorded after an artist is linked to
	// an organizer. It is an admin-actor / group event keyed on organizer_id in
	// PostHog; no fan distinct_id is present. Properties: organizer_id, artist_id.
	EventOrganizerArtistAssociated AnalyticsEventName = "organizer.artist.associated"

	// EventOrganizerConcertPublished is recorded after an organizer successfully
	// publishes a first-party series. It is an admin-actor / group event keyed
	// on organizer_id in PostHog; no fan distinct_id is present.
	// Properties: organizer_id, series_id, new_event_count.
	EventOrganizerConcertPublished AnalyticsEventName = "organizer.concert.published"
)

// Fan-level commerce and engagement events forwarded by the analytics
// consumer from domain events.
const (
	// EventArtistHypeChanged is recorded each time a fan sets the hype level
	// of a followed artist (ARTIST.hype_changed), including a set to the level
	// it already had. Properties: artist_id, hype.
	EventArtistHypeChanged AnalyticsEventName = "artist.hype.changed"

	// EventOrderPaid is recorded for each ORDER.paid announcement, with the
	// CloudEvent id (a UUIDv7) as the event id: a redelivery of the same
	// message is stored once, but a charge reported again announces a new
	// message and records a second event.
	// Properties: order_id, event_id, ticket_count, amount, currency, source
	// ("lottery_entry" or "checkout"), lottery_entry_id or checkout_id.
	EventOrderPaid AnalyticsEventName = "order.paid"

	// EventTicketLotteryResult is recorded for each drawn lottery entry
	// (ORDER.lottery_entry_drawn). Properties: lottery_entry_id,
	// ticket_sale_id, ticket_type_id, result ("won" or "lost"),
	// capture_failed.
	EventTicketLotteryResult AnalyticsEventName = "ticket.lottery.result"

	// EventCheckoutExpired is recorded when a Checkout's hold lapses
	// (ORDER.checkout_expired). Properties: checkout_id, ticket_type_id,
	// ticket_count.
	EventCheckoutExpired AnalyticsEventName = "checkout.expired"

	// EventTicketAdmitted is recorded for each Ticket a scan admits
	// (ORDER.ticket_admitted). Properties: ticket_id, event_id.
	EventTicketAdmitted AnalyticsEventName = "ticket.admitted"
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
	EventAccountSignin:              {},
	EventUserCreated:                {},
	EventArtistFollowCompleted:      {},
	EventArtistUnfollowCompleted:    {},
	EventTicketJourneyStatusChanged: {},
	EventNotificationSubscribed:     {},
	EventNotificationUnsubscribed:   {},
	EventNotificationDelivered:      {},
	EventOrganizerCreated:           {},
	EventOrganizerArtistAssociated:  {},
	EventOrganizerConcertPublished:  {},
	EventArtistHypeChanged:          {},
	EventOrderPaid:                  {},
	EventTicketLotteryResult:        {},
	EventCheckoutExpired:            {},
	EventTicketAdmitted:             {},
	EventConcertSearchCompleted:     {},
	EventConcertIngestCompleted:     {},
	EventTicketSaleSearchCompleted:  {},
	EventAIGeneration:               {},
}

// IsKnownEvent reports whether name is registered in the backend event
// catalogue. AnalyticsClient implementations call this on Capture to
// fail fast on typos like AnalyticsEventName("ticket.purcase.completed")
// that would otherwise silently fragment downstream dashboards.
func IsKnownEvent(name AnalyticsEventName) bool {
	_, ok := knownBackendEvents[name]
	return ok
}
