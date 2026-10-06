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
