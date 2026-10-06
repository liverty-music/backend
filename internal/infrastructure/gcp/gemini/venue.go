package gemini

import (
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

// venuePunctStripper is the punctuation set NormalizeVenue strips.
var venuePunctStripper = strings.NewReplacer(
	",", " ",
	".", " ",
	"・", " ",
	"·", " ",
	"–", " ",
	"-", " ",
	"(", " ",
	")", " ",
	"「", " ",
	"」", " ",
	"『", " ",
	"』", " ",
)

// prefectureAlt is the regex alternation of every Japanese prefecture name
// (47 都道府県). Used to strip prefecture mentions that artist sites add as
// venue prefixes ("大阪府・Billborad Live OSAKA") or suffixes
// ("幕張メッセ 9・11ホール（千葉県）"). Without this, identical venues
// fail to match across fixture and model output because the source pages
// inconsistently include or omit the prefecture qualifier.
const prefectureAlt = `北海道|青森県|岩手県|宮城県|秋田県|山形県|福島県|茨城県|栃木県|群馬県|埼玉県|千葉県|東京都|神奈川県|新潟県|富山県|石川県|福井県|山梨県|長野県|岐阜県|静岡県|愛知県|三重県|滋賀県|京都府|大阪府|兵庫県|奈良県|和歌山県|鳥取県|島根県|岡山県|広島県|山口県|徳島県|香川県|愛媛県|高知県|福岡県|佐賀県|長崎県|熊本県|大分県|宮崎県|鹿児島県|沖縄県`

var (
	// Prefix pattern: "PREFECTURE・" anchored at the start of the string.
	prefecturePrefixRe = regexp.MustCompile(`\A(?:` + prefectureAlt + `)・`)
	// Parenthesised mention anywhere: "（千葉県）" or "(東京都)" (the parens
	// may also include extra text like "（千葉県）" alone or "（東京都内）").
	prefectureParenRe = regexp.MustCompile(`[（(](?:` + prefectureAlt + `)[）)]`)
)

// tbdVenueMarkers are the strings that artist sites use to signal "venue
// not yet announced". Normalised representations are compared post-
// punctuation-strip and whitespace removal, so a marker like "-STAY TUNED-"
// becomes "staytuned" before this check fires.
var tbdVenueMarkers = map[string]struct{}{
	"":              {},
	"staytuned":     {},
	"tba":           {},
	"tbd":           {},
	"未定":            {},
	"後日発表":          {},
	"comingsoon":    {},
	"announced":     {}, // "to be announced"-style truncated remainders
	"tobeannounced": {},
}

// NormalizeVenue folds notation variants (NFKC), strips prefecture
// qualifiers, lowercases, strips a fixed punctuation set (including the
// Latin-1 middle dot), drops all whitespace, and collapses "venue TBD"
// markers to the empty string. ConcertSearcher uses it as the venue part of
// the repeat-removal key, and the A/B harness uses it to match returned
// events against the ground truth on (date, venue) key.
//
// Prefecture stripping rationale: artist sites quote venues in two
// inconsistent forms — prefixed ("大阪府・Billborad Live OSAKA") or
// suffixed in parens ("幕張メッセ 9・11ホール（千葉県）"). The model
// reproduces whichever form is on the page, while our fixture may have
// either. Stripping the prefecture from both sides before comparison
// resolves the mismatch without changing either source of truth.
func NormalizeVenue(s string) string {
	// NFKC first: official pages mix full-width and half-width forms
	// （（）vs ()）, compatibility characters (the CJK radical ⽇ U+2F47 for 日)
	// and half-width katakana (･), all of which NFKC folds together.
	s = norm.NFKC.String(s)
	// Strip prefecture markers BEFORE lowercasing so the alternation matches
	// the original-case Japanese characters.
	s = prefecturePrefixRe.ReplaceAllString(s, "")
	s = prefectureParenRe.ReplaceAllString(s, "")
	s = strings.ToLower(s)
	s = venuePunctStripper.Replace(s)
	// Drop all whitespace: pages disagree on spacing inside names
	// ("渋谷CLUB QUATTRO" vs "渋谷 CLUB QUATTRO").
	s = strings.Join(strings.Fields(s), "")
	// "某所" (an undisclosed place, e.g. "横浜某所") is a venue-TBD placeholder the
	// model emits for announced dates whose venue is not yet public; collapse it
	// like the other TBD markers so it matches an empty fixture venue.
	if strings.Contains(s, "某所") {
		return ""
	}
	if _, ok := tbdVenueMarkers[s]; ok {
		return ""
	}
	// Canonicalize known venue renames so a model emitting either the old or
	// the new name matches the fixture regardless of which it uses. 日本ガイシ
	// ホール was renamed クロコくんホール in 2026; the fixture carries the new name
	// with the old one in parentheses.
	if strings.Contains(s, "日本ガイシ") || strings.Contains(s, "クロコくん") {
		return "日本ガイシホール"
	}
	return s
}
