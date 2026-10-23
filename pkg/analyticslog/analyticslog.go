// Package analyticslog marks the log lines that are kept for analysis beyond
// operations. A marked line carries the LogEntry labels sink=analytics and
// topic=<topic>, and one Log Router sink filtered on labels.sink="analytics"
// routes every marked line to the analytics log bucket, whatever its topic.
package analyticslog

import (
	"log/slog"
	"regexp"
	"slices"
	"strings"
)

// Topic is what a marked line is about.
type Topic string

// The topics a marked line may carry. A new topic needs no change to the sink.
const (
	TopicJobResult            Topic = "job_result"
	TopicNotificationDelivery Topic = "notification_delivery"
	TopicCatalogReview        Topic = "catalog_review"
	TopicCatalogSync          Topic = "catalog_sync"
	TopicPayment              Topic = "payment"
)

// labelsKey is the structured-logging field the logging agent turns into
// LogEntry labels.
const labelsKey = "logging.googleapis.com/labels"

// sinkAnalytics is the value of the sink label the Log Router sink filters on.
const sinkAnalytics = "analytics"

// Attrs returns attrs followed by the analytics mark for topic. When any attr
// carries a fan's personal data (an email, name, phone number or address, by
// key or an email-shaped value), it returns attrs unmarked, so the line stays
// in the operational logs only. Attributes added through logging.SetAttrs or
// Logger.With are not inspected.
func Attrs(topic Topic, attrs ...slog.Attr) []slog.Attr {
	if hasPersonalData(attrs) {
		return attrs
	}
	// Clip so append allocates instead of writing into the caller's array.
	return append(slices.Clip(attrs), slog.Group(labelsKey,
		slog.String("sink", sinkAnalytics),
		slog.String("topic", string(topic)),
	))
}

// personalKeySuffixes match keys such as email, owner_email or postal_address.
var personalKeySuffixes = []string{"email", "phone", "phone_number", "address"}

// personalNameKeys are the keys that hold a person's name. Keys such as
// artist_name or venue_name are catalog data and stay markable.
var personalNameKeys = map[string]bool{
	"name": true, "user_name": true, "fan_name": true, "display_name": true,
	"full_name": true, "given_name": true, "family_name": true,
}

var emailValue = regexp.MustCompile(`[^\s@]+@[^\s@]+\.[^\s@]+`)

func hasPersonalData(attrs []slog.Attr) bool {
	for _, a := range attrs {
		key := strings.ToLower(a.Key)
		if personalNameKeys[key] {
			return true
		}
		for _, s := range personalKeySuffixes {
			if key == s || strings.HasSuffix(key, "_"+s) {
				return true
			}
		}
		v := a.Value.Resolve()
		switch v.Kind() {
		case slog.KindGroup:
			if hasPersonalData(v.Group()) {
				return true
			}
		case slog.KindString:
			if emailValue.MatchString(v.String()) {
				return true
			}
		}
	}
	return false
}
