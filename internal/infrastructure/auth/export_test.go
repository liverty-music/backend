package auth

// JWTValidatorIssuer returns the issuer field of v for black-box tests.
func JWTValidatorIssuer(v *JWTValidator) string {
	return v.issuer
}
