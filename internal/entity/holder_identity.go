package entity

import (
	"errors"
	"regexp"
	"unicode/utf8"
)

// e164Re matches a phone number in E.164 form: "+" then 2 to 15 digits, the
// first not 0. Mirrors the protovalidate pattern on HolderIdentity.phone_number
// and SellerDetails.phone_number, and the CHECK constraints in storage.
var e164Re = regexp.MustCompile(`^\+[1-9][0-9]{1,14}$`)

// holderFullNameMaxLen is the longest holder full name accepted, in characters.
const holderFullNameMaxLen = 200

// IsE164 reports whether s is a phone number in E.164 form.
func IsE164(s string) bool {
	return e164Re.MatchString(s)
}

// HolderIdentity is the 本人確認 (identity check) a fan gives for the tickets
// they buy: the real name and contact phone printed on the covered ticket's
// face. It is given when applying to a lottery or authorizing a first-come
// checkout, is kept on the User to prefill the next checkout, and is copied
// onto each Ticket.
//
// Mirrors liverty_music.entity.v1.HolderIdentity.
type HolderIdentity struct {
	// FullName is the holder's legal name as it appears on their ID, 1 to 200
	// characters.
	FullName string

	// PhoneNumber is the contact phone number in E.164 format (e.g.
	// "+819012345678"). The proto rule rejects any other format at the RPC
	// boundary, and CHECK constraints enforce it in storage.
	PhoneNumber string
}

// Validate reports whether the identity has a full name of 1 to 200
// characters and a phone number in E.164 form. The entity layer returns
// stdlib errors; callers wrap them with an apperr code.
func (h HolderIdentity) Validate() error {
	if n := utf8.RuneCountInString(h.FullName); n < 1 || n > holderFullNameMaxLen {
		return errors.New("holder full name must be 1 to 200 characters")
	}
	if !IsE164(h.PhoneNumber) {
		return errors.New("holder phone number must be in E.164 form")
	}
	return nil
}
