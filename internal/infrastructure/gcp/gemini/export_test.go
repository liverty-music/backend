package gemini

// IsRetryable exports isRetryable for testing.
var IsRetryable = isRetryable

// ErrInvalidJSON exports errInvalidJSON for testing.
var ErrInvalidJSON = errInvalidJSON

// ParseStep1Envelope exports parseStep1Envelope for testing.
var ParseStep1Envelope = parseStep1Envelope

// SetStep1Slices overrides the Step 1 slices of s for A/B variant runs.
func SetStep1Slices(s *ConcertSearcher, slices []Step1Slice) { s.slices = slices }
