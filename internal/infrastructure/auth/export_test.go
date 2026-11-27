package auth

import "time"

// JWTValidatorIssuer returns the issuer field of v for black-box tests.
func JWTValidatorIssuer(v *JWTValidator) string {
	return v.issuer
}

// SetIntrospectorClock replaces the clock of i.
func SetIntrospectorClock(i *Introspector, now func() time.Time) {
	i.now = now
}

// SetIntrospectorTimeout replaces the 5-second bound of one ask, so a test
// of an unanswered ask does not wait 5 seconds.
func SetIntrospectorTimeout(i *Introspector, d time.Duration) {
	i.timeout = d
}

// IntrospectionTimeout is the bound of one ask.
const IntrospectionTimeout = introspectionTimeout
