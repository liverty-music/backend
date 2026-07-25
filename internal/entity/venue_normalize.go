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

	// japanesePrefectureBases are the 47 prefecture names without their
	// 都/道/府/県 suffix (北海道 keeps its 道 as it has no bare form). The
	// admin-area prefix strip fires ONLY for these tokens, so a venue whose own
	// name embeds a middle dot after a non-prefecture token — e.g.
	// "東京文化会館・大ホール" or "杉並公会堂・大ホール" — is preserved rather than
	// truncated to a generic residue like "大ホール". Truncating those would
	// silently collapse distinct same-date venues into one dedup key and, on the
	// pending-staged path (which ignores start time), drop a genuinely-new concert
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
