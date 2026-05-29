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
// verification, push delivery, account state changes) are emitted only
// from the backend through these constants.
type AnalyticsEventName string

// Account lifecycle events emitted from the backend.
const (
	// EventAccountSignupCompleted is recorded when a new user record has
	// been persisted after Zitadel returns a successful signup callback.
	EventAccountSignupCompleted AnalyticsEventName = "account.signup.completed"

	// EventAccountLogin is recorded when an existing user successfully
	// completes the OIDC authentication callback.
	EventAccountLogin AnalyticsEventName = "account.login"

	// EventAccountPreferredLanguageUpdated is recorded when a user's
	// preferred display language is changed via UpdatePreferredLanguage.
	EventAccountPreferredLanguageUpdated AnalyticsEventName = "account.preferred_language.updated"

	// EventUserCreated is recorded when the backend creates a User record
	// for the first time, either through self-signup or admin provisioning.
	EventUserCreated AnalyticsEventName = "user.created"

	// EventUserDeleted is recorded when a User record is permanently
	// removed from the platform.
	EventUserDeleted AnalyticsEventName = "user.deleted"
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

// Concert and recommendation events emitted from the backend.
const (
	// EventConcertRecommendationServed is recorded when the backend
	// computes a recommendation set for a user. This is the truth source
	// for recommendation impressions; the frontend records click-through
	// separately via concert.recommendation.clicked.
	EventConcertRecommendationServed AnalyticsEventName = "concert.recommendation.served"
)

// Ticket lottery and purchase events emitted from the backend.
const (
	// EventTicketLotteryEntryAccepted is recorded after a lottery entry
	// passes validation and is persisted. Paired with the frontend
	// ticket.lottery.entry.submitted to measure the intent-to-acceptance gap.
	EventTicketLotteryEntryAccepted AnalyticsEventName = "ticket.lottery.entry.accepted"

	// EventTicketLotteryEntryRejected is recorded when a lottery entry
	// fails validation (duplicate, closed window, etc.). Paired with
	// ticket.lottery.entry.submitted.
	EventTicketLotteryEntryRejected AnalyticsEventName = "ticket.lottery.entry.rejected"

	// EventTicketLotteryResultAssigned is recorded when the lottery draw
	// completes and a result (WON or LOST) is recorded against the entry.
	EventTicketLotteryResultAssigned AnalyticsEventName = "ticket.lottery.result.assigned"

	// EventTicketPurchaseCompleted is the trust-critical event recorded
	// when a payment provider confirms a successful ticket purchase. The
	// frontend emits a paired ticket.purchase.initiated at the moment the
	// user starts the purchase flow.
	EventTicketPurchaseCompleted AnalyticsEventName = "ticket.purchase.completed"

	// EventTicketPurchaseFailed is recorded when a purchase attempt is
	// rejected by the payment provider or backend validation.
	EventTicketPurchaseFailed AnalyticsEventName = "ticket.purchase.failed"
)

// Entry verification events emitted from the backend, including the
// zero-knowledge-proof check at venue gates.
const (
	// EventEntryZkProofVerified is recorded when a fan's Groth16 proof
	// passes verification against the published Merkle root for the event.
	EventEntryZkProofVerified AnalyticsEventName = "entry.zk_proof.verified"

	// EventEntryZkProofRejected is recorded when proof verification fails
	// for any reason (invalid proof, wrong event, expired commitment).
	EventEntryZkProofRejected AnalyticsEventName = "entry.zk_proof.rejected"
)

// Push notification lifecycle events emitted from the backend.
const (
	// EventPushSubscriptionCompleted is recorded after a Web Push
	// subscription has been persisted against the user record.
	EventPushSubscriptionCompleted AnalyticsEventName = "push.subscription.completed"

	// EventPushNotificationDelivered is recorded when the push provider
	// accepts a notification for delivery. The frontend records the
	// downstream open/dismiss separately.
	EventPushNotificationDelivered AnalyticsEventName = "push.notification.delivered"
)

// knownBackendEvents is the allowlist of AnalyticsEventName constants
// emitted from the backend. AnalyticsClient implementations consult this
// via IsKnownEvent to reject typos at the call site, satisfying the
// "unknown eventName" contract on Enqueue.
//
// The map is populated lazily-once at package init via the const groups
// above; every new constant MUST be added here in the same commit.
var knownBackendEvents = map[AnalyticsEventName]struct{}{
	EventAccountSignupCompleted:          {},
	EventAccountLogin:                    {},
	EventAccountPreferredLanguageUpdated: {},
	EventUserCreated:                     {},
	EventUserDeleted:                     {},
	EventArtistFollowCompleted:           {},
	EventArtistUnfollowCompleted:         {},
	EventConcertRecommendationServed:     {},
	EventTicketLotteryEntryAccepted:      {},
	EventTicketLotteryEntryRejected:      {},
	EventTicketLotteryResultAssigned:     {},
	EventTicketPurchaseCompleted:         {},
	EventTicketPurchaseFailed:            {},
	EventEntryZkProofVerified:            {},
	EventEntryZkProofRejected:            {},
	EventPushSubscriptionCompleted:       {},
	EventPushNotificationDelivered:       {},
}

// IsKnownEvent reports whether name is registered in the backend event
// catalogue. AnalyticsClient implementations call this on Enqueue to
// fail fast on typos like AnalyticsEventName("ticket.purcase.completed")
// that would otherwise silently fragment downstream dashboards.
func IsKnownEvent(name AnalyticsEventName) bool {
	_, ok := knownBackendEvents[name]
	return ok
}
