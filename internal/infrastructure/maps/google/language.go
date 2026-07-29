package google

import "strings"

// countryCodeToLanguage maps an ISO 3166-1 alpha-2 country code to a BCP 47
// language tag for use in Places API languageCode requests. It ensures Places
// returns venue names in the locale of the venue's country.
//
// Only the most common venue-bearing countries are explicitly mapped; all
// others default to "en" so results are always human-readable.
func countryCodeToLanguage(cc string) string {
	switch strings.ToUpper(cc) {
	case "JP":
		return "ja"
	case "KR":
		return "ko"
	case "CN":
		return "zh-CN"
	case "TW":
		return "zh-TW"
	default:
		return "en"
	}
}

// adminAreaToLanguage extracts the ISO 3166-1 alpha-2 country code from an
// ISO 3166-2 admin_area value (e.g. "JP-13") and returns the corresponding
// BCP 47 language tag. Returns "en" when adminArea is empty.
func adminAreaToLanguage(adminArea string) string {
	if adminArea == "" {
		return "en"
	}
	parts := strings.SplitN(adminArea, "-", 2)
	return countryCodeToLanguage(parts[0])
}
