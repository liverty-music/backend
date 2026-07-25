package entity

import (
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

var (
	// venueWhitespaceRE matches runs of whitespace so they collapse to a single
	// ASCII space (NFKC has already folded full-width spaces to ASCII by then).
	venueWhitespaceRE = regexp.MustCompile(`\s+`)

	// venuePerformancePrefixRE strips a leading "〈city〉公演 ＠" location prefix,
	// e.g. "大阪公演 @フェスティバルホール" → "フェスティバルホール". NFKC has
	// already folded the full-width ＠ to an ASCII @ by the time this runs.
	venuePerformancePrefixRE = regexp.MustCompile(`^.*?公演\s*@\s*`)

	// venueAreaPrefixRE strips a single leading "〈admin_area〉・" prefix, e.g.
	// "大阪・フェスティバルホール" → "フェスティバルホール". The prefix is bounded
	// to a short token so it targets prefecture/city names and does not eat venue
	// names that legitimately embed a middle dot.
	venueAreaPrefixRE = regexp.MustCompile(`^[^・]{1,6}・`)
)

// NormalizeVenueName canonicalizes a scraped venue name for dedup comparison so
// that Gemini's formatting drift across discovery runs collapses to one identity:
// full/half-width forms are NFKC-folded, whitespace is collapsed, and a leading
// location prefix ("大阪・…" or "大阪公演 ＠…") is stripped. It is a best-effort
// heuristic — the event natural key `(venue_id, local_event_date, start_at)`
// remains the DB-level safety net for anything it fails to collapse. A blank or
// whitespace-only input returns "".
func NormalizeVenueName(name string) string {
	// NFKC folds full-width kana/ASCII and the full-width ＠ to canonical forms.
	s := norm.NFKC.String(name)
	// Collapse internal/edge whitespace so spacing drift does not defeat the match.
	s = strings.TrimSpace(venueWhitespaceRE.ReplaceAllString(s, " "))
	// Strip a leading "〈city〉公演 ＠" prefix before the "〈area〉・" prefix, since the
	// former can itself contain a middle dot the latter would otherwise mis-strip.
	s = venuePerformancePrefixRE.ReplaceAllString(s, "")
	s = venueAreaPrefixRE.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}
