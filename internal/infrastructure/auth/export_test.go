package auth

// ClaimsKey exposes the unexported claimsKey context key for black-box tests.
var ClaimsKey = claimsKey

// JWTValidatorIssuer returns the issuer field of v for black-box tests.
func JWTValidatorIssuer(v *JWTValidator) string {
	return v.issuer
}
