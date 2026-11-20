package gemini

// IsRetryable exports isRetryable for testing.
var IsRetryable = isRetryable

// SetPrompt overrides the prompt of s for A/B variant runs.
func SetPrompt(s *ConcertSearcher, p Prompt) { s.prompt = p }

// ParseSingleStepJSON exports parseSingleStepJSON for testing.
var ParseSingleStepJSON = parseSingleStepJSON

// LinkProfile exports linkProfile for testing.
type LinkProfile = linkProfile

// NewLinkProfile builds a linkProfile for testing.
func NewLinkProfile(max int, tiers ...[]string) LinkProfile {
	return linkProfile{tiers: tiers, max: max}
}

// ConcertSearchProfile exports concertSearchProfile for testing.
var ConcertSearchProfile = concertSearchProfile

// OfficialPageLinks exports officialPageLinks for testing.
var OfficialPageLinks = officialPageLinks

// NewOfficialPageClientWithDial exports newOfficialPageClient so tests can
// route hosts to local servers.
var NewOfficialPageClientWithDial = newOfficialPageClient

// SetLinkProfile overrides the link profile of s for A/B variant runs.
func SetLinkProfile(s *ConcertSearcher, p LinkProfile) { s.linkProfile = p }

// DefaultPrompt exports the production prompt for A/B variants built on it.
var DefaultPrompt = defaultPrompt

// SetWithoutGoogleSearch leaves the Google Search tool out of the grounded
// call of s, for A/B variant runs.
func SetWithoutGoogleSearch(s *ConcertSearcher, without bool) { s.withoutGoogleSearch = without }
