package entity

import (
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
	"golang.org/x/text/width"
)

// SameTourTitle reports whether two Series titles are the same tour title
// (Series, "Same tour title"): equal once full-width and half-width forms are
// folded to one form, Latin-script letters are put in one case, and every
// character that is neither a letter nor a number is removed.
//
// width.Fold turns a half-width katakana with a voicing mark ("ｶﾞ") into the
// katakana and a combining mark, which NFC composes into the one voiced
// katakana ("ガ"). NFKC is not used, because it would make a Roman numeral
// ("Ⅱ") equal to its letters ("II"); numbers of every form are kept as they
// are, so a numbered sequel stays a different tour.
func SameTourTitle(a, b string) bool {
	return tourTitleKey(a) == tourTitleKey(b)
}

// tourTitleKey returns the form in which two tour titles are compared by
// [SameTourTitle].
func tourTitleKey(title string) string {
	s := norm.NFC.String(width.Fold.String(title))
	var b strings.Builder
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsNumber(r) {
			continue
		}
		if unicode.Is(unicode.Latin, r) {
			r = unicode.ToLower(r)
		}
		b.WriteRune(r)
	}
	return b.String()
}
