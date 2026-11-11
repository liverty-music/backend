package geo

import (
	"regexp"
	"strings"

	"github.com/liverty-music/backend/internal/entity"
)

// iso31662Re matches an ISO 3166-2 subdivision code (e.g. "JP-13", "TW-TPE",
// "US-CA"), case-insensitively.
var iso31662Re = regexp.MustCompile(`^[A-Za-z]{2}-[A-Za-z0-9]{1,3}$`)

// NormalizeAdminArea converts a free-text administrative area string into
// the corresponding ISO 3166-2 subdivision code. It passes through input that
// is already an ISO 3166-2 code (any country) and maps Japanese prefecture
// names (with or without suffix) and English names, case-insensitively.
//
// Returns nil when the input is empty, whitespace-only, or unrecognized.
// Callers should treat nil as "admin area unknown" and store NULL in the database.
func NormalizeAdminArea(text string) *string {
	s := strings.TrimSpace(text)
	if s == "" {
		return nil
	}
	// Already an ISO 3166-2 code (any country): pass it through upper-cased.
	if iso31662Re.MatchString(s) {
		code := strings.ToUpper(s)
		return &code
	}
	if code, ok := entity.PrefectureCode(s); ok {
		return &code
	}
	return nil
}
