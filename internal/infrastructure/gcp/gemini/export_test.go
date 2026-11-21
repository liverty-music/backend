package gemini

// IsRetryable exports isRetryable for testing.
var IsRetryable = isRetryable

// SetPrompt overrides the prompt of s for A/B variant runs.
func SetPrompt(s *ConcertSearcher, p Prompt) { s.prompt = p }

// ParseSingleStepJSON exports parseSingleStepJSON for testing.
var ParseSingleStepJSON = parseSingleStepJSON

// PageLink exports pageLink for testing.
type PageLink = pageLink

// OfficialPageLinks exports officialPageLinks for testing.
var OfficialPageLinks = officialPageLinks

// NewOfficialPageClientWithDial exports newOfficialPageClient so tests can
// route hosts to local servers.
var NewOfficialPageClientWithDial = newOfficialPageClient

// DefaultPrompt exports the production prompt for A/B variants built on it.
var DefaultPrompt = defaultPrompt

// SetWithoutGoogleSearch leaves the Google Search tool out of the grounded
// call of s, for A/B variant runs.
func SetWithoutGoogleSearch(s *ConcertSearcher, without bool) { s.withoutGoogleSearch = without }
