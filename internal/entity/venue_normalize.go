package entity

import (
	"regexp"
	"strings"
	"time"
	"unicode"

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

	// japanesePrefectureBases are the 47 prefecture names without their
	// 都/道/府/県 suffix (北海道 keeps its 道 as it has no bare form). The
	// admin-area prefix strip fires ONLY for these tokens, so a venue whose own
	// name embeds a middle dot after a non-prefecture token — e.g.
	// "東京文化会館・大ホール" or "杉並公会堂・大ホール" — is preserved rather than
	// truncated to a generic residue like "大ホール". Truncating those would
	// silently collapse distinct same-date venues into one dedup key and, on the
	// pending-candidate path (which ignores start time), drop a genuinely-new concert
	// with no DB-level safety net.
	japanesePrefectureBases = []string{
		"北海道",
		"青森", "岩手", "宮城", "秋田", "山形", "福島",
		"茨城", "栃木", "群馬", "埼玉", "千葉", "東京", "神奈川",
		"新潟", "富山", "石川", "福井", "山梨", "長野",
		"岐阜", "静岡", "愛知", "三重",
		"滋賀", "京都", "大阪", "兵庫", "奈良", "和歌山",
		"鳥取", "島根", "岡山", "広島", "山口",
		"徳島", "香川", "愛媛", "高知",
		"福岡", "佐賀", "長崎", "熊本", "大分", "宮崎", "鹿児島", "沖縄",
	}

	// venueAreaPrefixRE strips a single leading "〈prefecture〉・" prefix, e.g.
	// "大阪・フェスティバルホール" → "フェスティバルホール" and
	// "大阪府・フェスティバルホール" → "フェスティバルホール". The optional
	// [都道府県] suffix lets a base match with or without its administrative
	// suffix; the strip requires the prefecture token to be immediately followed
	// by the middle dot, so non-prefecture facility names are never touched.
	venueAreaPrefixRE = regexp.MustCompile(`^(?:` + strings.Join(japanesePrefectureBases, "|") + `)[都道府県]?・`)
)

// unannouncedVenueMarkers are the names that only say the venue is not yet
// announced, in the form [unannouncedVenueKey] gives them.
var unannouncedVenueMarkers = map[string]struct{}{
	"tba":           {},
	"tbd":           {},
	"tobeannounced": {},
	"comingsoon":    {},
	"staytuned":     {},
	"未定":            {},
	"後日発表":          {},
}

// undisclosedPlace marks a venue that is decided but not made public, as in
// "横浜某所": any name containing it names no venue.
const undisclosedPlace = "某所"

// unannouncedVenueKey returns name without whitespace, punctuation and
// symbols and in lower case, the form in which a whole name is compared with
// [unannouncedVenueMarkers]: "-STAY TUNED-" becomes "staytuned".
func unannouncedVenueKey(name string) string {
	var b strings.Builder
	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// isUnannouncedVenue reports whether a normalized name only says that the
// venue is not yet announced: the whole name is one of the markers, or it
// contains 某所. A real venue whose name contains a marker word, such as
// "TBA Hall Osaka", is not one.
func isUnannouncedVenue(normalized string) bool {
	if strings.Contains(normalized, undisclosedPlace) {
		return true
	}
	_, ok := unannouncedVenueMarkers[unannouncedVenueKey(normalized)]
	return ok
}

// NormalizeVenueName is the one normalization of a listed venue name: every
// comparison of venue names (an Event's listed venue name, a
// ConcertCandidate's, and the events Concert.Search returns) starts from it,
// so that formatting drift across discovery runs collapses to one identity.
// Full/half-width forms are NFKC-folded, whitespace is collapsed, and a leading
// location prefix ("大阪・…" or "大阪公演 ＠…") is stripped. It is a best-effort
// heuristic — the event natural key `(venue_id, local_event_date, start_at)`
// remains the DB-level safety net for anything it fails to collapse.
//
// It returns "" for a name that is missing: a blank or whitespace-only name,
// and a name that only says the venue is not yet announced ("TBA",
// "-STAY TUNED-", "未定", "横浜某所"; see [isUnannouncedVenue]). An event with
// such a name is skipped like one with no venue name.
func NormalizeVenueName(name string) string {
	// NFKC folds full-width kana/ASCII and the full-width ＠ to canonical forms.
	s := norm.NFKC.String(name)
	// Collapse internal/edge whitespace so spacing drift does not defeat the match.
	s = strings.TrimSpace(venueWhitespaceRE.ReplaceAllString(s, " "))
	// Strip a leading "〈city〉公演 ＠" prefix before the "〈area〉・" prefix, since the
	// former can itself contain a middle dot the latter would otherwise mis-strip.
	s = venuePerformancePrefixRE.ReplaceAllString(s, "")
	s = strings.TrimSpace(venueAreaPrefixRE.ReplaceAllString(s, ""))
	if isUnannouncedVenue(s) {
		return ""
	}
	return s
}

// SameListedVenue reports whether two listed venue names name the same venue:
// they are equal once each goes through [NormalizeVenueName]. A name that is
// missing normalizes to "" and so equals another missing one.
func SameListedVenue(a, b string) bool {
	return NormalizeVenueName(a) == NormalizeVenueName(b)
}

// DateVenueKey is the key under which an event on date at the listed venue is
// matched with another: the local date and the normalized listed venue name.
// Every match of a discovered event against an existing, pending or other
// discovered event on (date, venue) uses it.
func DateVenueKey(date time.Time, listedVenueName string) string {
	return date.Format("2006-01-02") + "|" + NormalizeVenueName(listedVenueName)
}

// venueMiddleDots are the middle-dot variants two notations of one venue name
// differ in; NFKC has already folded the half-width ･ to ・.
var venueMiddleDots = strings.NewReplacer("・", "", "·", "", "•", "", "‧", "", "∙", "", "⋅", "")

// VenueRepeatKey returns the form in which two listed venue names are the same
// venue for the events of one search: the normalization
// ([NormalizeVenueName]) with whitespace, middle-dot variants and Latin letter
// case ignored, so "渋谷CLUB QUATTRO" and "渋谷 CLUB QUATTRO", or "Zepp Haneda"
// and "ZEPP HANEDA", give one key. No other rule makes two names the same. It
// is "" for a venue not yet announced.
func VenueRepeatKey(listedVenueName string) string {
	s := strings.ToLower(NormalizeVenueName(listedVenueName))
	s = venueMiddleDots.Replace(s)
	return strings.Join(strings.Fields(s), "")
}
