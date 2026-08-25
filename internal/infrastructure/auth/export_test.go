package auth

// ClaimsKey exposes the unexported claimsKey context key for black-box tests.
var ClaimsKey = claimsKey

// JWTValidatorIssuer returns the issuer field of v for black-box tests.
func JWTValidatorIssuer(v *JWTValidator) string {
	return v.issuer
}

// CallerOrgIDKey exposes the unexported callerOrgIDKey type for black-box tests
// that need to inject a caller org id directly into a context without going
// through the full interceptor chain.
var CallerOrgIDKey = callerOrgIDKey{}
