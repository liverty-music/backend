package gemini

// IsRetryable exports isRetryable for testing.
var IsRetryable = isRetryable

// ErrInvalidJSON exports errInvalidJSON for testing.
var ErrInvalidJSON = errInvalidJSON

// ErrNoCandidates exports errNoCandidates for testing.
var ErrNoCandidates = errNoCandidates

// ErrTooManyToolCalls exports errTooManyToolCalls for testing.
var ErrTooManyToolCalls = errTooManyToolCalls

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

// ErrNonPublicAddress exports errNonPublicAddress for testing.
var ErrNonPublicAddress = errNonPublicAddress

// ErrOffSiteRedirect exports errOffSiteRedirect for testing.
var ErrOffSiteRedirect = errOffSiteRedirect

// SetLinkProfile overrides the link profile of s for A/B variant runs.
func SetLinkProfile(s *ConcertSearcher, p LinkProfile) { s.linkProfile = p }
