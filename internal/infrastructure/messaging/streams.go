package messaging

import (
	"strings"
	"time"

	"github.com/nats-io/nats.go"
)

// PoisonQueueSubject is the NATS subject for messages that exceeded max retries.
const PoisonQueueSubject = "POISON.queue"

// streams is the registry of JetStream streams and their subject filters:
// one stream per entity that has events, named after the entity, capturing
// its two-token subjects (<ENTITY>.<what>) with <ENTITY>.*, plus POISON.
// NACK (the external operator) owns stream lifecycle; the declarations are in
// cloud-provisioning k8s/namespaces/nats/base/jetstream/. The application only
// reads this registry to validate subject coverage and to look up which
// stream a given subject belongs to (used by the pull subscriber to bind a
// durable).
var streams = []nats.StreamConfig{
	entityStream("ARTIST"),
	entityStream("CHECKOUT"),
	entityStream("CONCERT"),
	entityStream("EVENT"),
	entityStream("FOLLOW"),
	entityStream("LOTTERY_ENTRY"),
	entityStream("ORDER"),
	entityStream("ORGANIZER"),
	entityStream("PASSKEY"),
	entityStream("PUSH_SUBSCRIPTION"),
	entityStream("RESALE_ENTRY"),
	entityStream("RESALE_LISTING"),
	entityStream("SERIES_IMAGE"),
	entityStream("TICKET"),
	entityStream("TICKET_JOURNEY"),
	entityStream("TICKET_SALE"),
	entityStream("USER"),
	{
		Name:       "POISON",
		Subjects:   []string{"POISON.*"},
		Retention:  nats.LimitsPolicy,
		MaxAge:     30 * 24 * time.Hour,
		Storage:    nats.FileStorage,
		Discard:    nats.DiscardOld,
		Replicas:   1,
		Duplicates: 2 * time.Minute,
	},
}

// entityStream returns the configuration every entity stream shares, as
// declared in cloud-provisioning: name.* kept for 7 days.
func entityStream(name string) nats.StreamConfig {
	return nats.StreamConfig{
		Name:       name,
		Subjects:   []string{name + ".*"},
		Retention:  nats.LimitsPolicy,
		MaxAge:     7 * 24 * time.Hour,
		Storage:    nats.FileStorage,
		Discard:    nats.DiscardOld,
		Replicas:   1,
		Duplicates: 2 * time.Minute,
	}
}

// SubjectCoveredByStream reports whether the given concrete NATS subject is
// captured by at least one configured JetStream stream. A JetStream consumer
// can only bind to a subject that some stream's subject filter matches; a
// subscription to an uncovered subject fails at startup with "no stream
// matches subject" and crashloops the consumer. This is the recurring class of
// bug that occurs when a new publisher/subscription is added for a fresh event
// domain without adding its paired stream to the streams list above.
//
// Matching follows NATS token semantics: subjects are '.'-delimited; a '*'
// token matches exactly one token, and a trailing '>' matches one or more
// remaining tokens.
func SubjectCoveredByStream(subject string) bool {
	for _, s := range streams {
		for _, filter := range s.Subjects {
			if subjectMatches(filter, subject) {
				return true
			}
		}
	}
	return false
}

// StreamForSubject returns the name of the JetStream stream whose subject
// filters cover the given concrete subject, and true. If no stream covers the
// subject it returns ("", false). The pull subscriber uses this to resolve the
// stream name needed by js.Consumer(ctx, stream, durable) when binding a
// pre-existing durable.
func StreamForSubject(subject string) (string, bool) {
	for _, s := range streams {
		for _, filter := range s.Subjects {
			if subjectMatches(filter, subject) {
				return s.Name, true
			}
		}
	}
	return "", false
}

// subjectMatches implements NATS subject-filter matching for a single filter
// against a concrete subject.
func subjectMatches(filter, subject string) bool {
	filterTokens := strings.Split(filter, ".")
	subjectTokens := strings.Split(subject, ".")

	for i, ft := range filterTokens {
		// A trailing '>' matches all remaining subject tokens (at least one).
		if ft == ">" {
			return i < len(subjectTokens)
		}
		if i >= len(subjectTokens) {
			return false
		}
		// '*' matches exactly one token; otherwise require an exact match.
		if ft != "*" && ft != subjectTokens[i] {
			return false
		}
	}

	// No '>' wildcard consumed the tail, so token counts must match exactly.
	return len(filterTokens) == len(subjectTokens)
}
