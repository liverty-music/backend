package entity

import (
	"encoding/binary"
	"errors"
	"math"
	"time"

	"uuid"
)

const (
	// AdmissionCodeVersion is the version byte of the AdmissionCode payload
	// layout documented on liverty_music.entity.v1.AdmissionCode.
	AdmissionCodeVersion byte = 0x01
	// MaxAdmissionCodeTickets is the most Tickets one AdmissionCode presents.
	MaxAdmissionCodeTickets = 10

	// AdmissionCodeMaxAge is how long before the check time a code's signed
	// time may lie for the code to be fresh.
	AdmissionCodeMaxAge = 30 * time.Second
	// AdmissionCodeMaxClockAhead is how far after the check time a code's
	// signed time may lie (a device clock running slightly fast).
	AdmissionCodeMaxClockAhead = 15 * time.Second

	// admissionCodeHeaderLen is version (1) + user id (16) + event id (16) +
	// ticket count (1).
	admissionCodeHeaderLen = 1 + 16 + 16 + 1
	// admissionCodeSignedTimeLen is the signed time, unsigned Unix seconds.
	admissionCodeSignedTimeLen = 4
)

// AdmissionCode is what a fan's device shows as the entry QR code: a
// statement that one User presents 1 to 10 of their Tickets for one event,
// signed on the device with the private half of the User's [WalletPublicKey].
// It is made offline, renewed every 15 seconds and never stored.
//
// On the wire it is the compact binary payload documented on
// liverty_music.entity.v1.AdmissionCode, encoded as Base45 (RFC 9285):
//
//	version 0x01 (1) | user id (16) | event id (16) | ticket count N (1) |
//	N ticket ids (16 each) | signed time, Unix seconds (4) | signature (64)
//
// The signature is ECDSA P-256 / SHA-256 (P1363 r || s) over every byte
// before it.
type AdmissionCode struct {
	// UserID is the User presenting the Tickets.
	UserID UserID
	// EventID is the event the Tickets admit to.
	EventID string
	// TicketIDs are the presented Tickets, distinct, in the fan's order.
	TicketIDs []TicketID
	// SignedTime is when the device signed the code, by its own clock (whole
	// seconds).
	SignedTime time.Time
	// Signature is the device's signature over the payload before it.
	Signature Signature
}

// AdmissionCodeVerdict is the outcome of [AdmissionCode.Verify].
type AdmissionCodeVerdict int

const (
	// AdmissionCodeValid means the code is signed by the user's active key and
	// fresh.
	AdmissionCodeValid AdmissionCodeVerdict = iota + 1
	// AdmissionCodeForged means the key is not the user's or the signature does
	// not verify over the code's content.
	AdmissionCodeForged
	// AdmissionCodeExpired means the signature verifies but the code is not
	// fresh.
	AdmissionCodeExpired
)

// errInvalidAdmissionCode is returned by [AdmissionCode.Validate].
var errInvalidAdmissionCode = errors.New("invalid admission code")

// Validate reports an error unless the code presents 1 to 10 distinct
// Tickets and names a user and an event as UUIDs. That every presented Ticket
// is for the code's event cannot be told from the code alone (it carries only
// ids); admission refuses such a Ticket on its own.
func (c *AdmissionCode) Validate() error {
	if _, err := uuid.Parse(string(c.UserID)); err != nil {
		return errInvalidAdmissionCode
	}
	if _, err := uuid.Parse(c.EventID); err != nil {
		return errInvalidAdmissionCode
	}
	if len(c.TicketIDs) < 1 || len(c.TicketIDs) > MaxAdmissionCodeTickets {
		return errInvalidAdmissionCode
	}
	seen := make(map[uuid.UUID]struct{}, len(c.TicketIDs))
	for _, id := range c.TicketIDs {
		u, err := uuid.Parse(string(id))
		if err != nil {
			return errInvalidAdmissionCode
		}
		if _, dup := seen[u]; dup {
			return errInvalidAdmissionCode
		}
		seen[u] = struct{}{}
	}
	if c.SignedTime.Unix() < 0 || c.SignedTime.Unix() > math.MaxUint32 {
		return errInvalidAdmissionCode
	}
	return nil
}

// SignedPayload returns the bytes the signature covers: every byte of the
// payload before the signature. The code must be valid.
func (c *AdmissionCode) SignedPayload() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	out := make([]byte, 0, admissionCodeHeaderLen+16*len(c.TicketIDs)+admissionCodeSignedTimeLen)
	out = append(out, AdmissionCodeVersion)
	user := uuid.MustParse(string(c.UserID))
	event := uuid.MustParse(c.EventID)
	out = append(out, user[:]...)
	out = append(out, event[:]...)
	out = append(out, byte(len(c.TicketIDs)))
	for _, id := range c.TicketIDs {
		t := uuid.MustParse(string(id))
		out = append(out, t[:]...)
	}
	out = binary.BigEndian.AppendUint32(out, uint32(c.SignedTime.Unix()))
	return out, nil
}

// Encode returns the Base45 text of the code (payload and signature), the
// text a QR code carries. The code must be valid and carry a 64-byte
// signature.
func (c *AdmissionCode) Encode() (string, error) {
	payload, err := c.SignedPayload()
	if err != nil {
		return "", err
	}
	if len(c.Signature) != SignatureLen {
		return "", errInvalidAdmissionCode
	}
	return base45Encode(append(payload, c.Signature...)), nil
}

// DecodeAdmissionCode reads the scanned text into an AdmissionCode without
// trusting it: the signature is not checked. ok is false (Malformed) when the
// text is not Base45, the version is unknown, the ticket count is not 1 to 10,
// a ticket repeats or the length is not exactly 102+16N bytes. It never fails
// otherwise.
func DecodeAdmissionCode(text string) (code *AdmissionCode, ok bool) {
	raw, err := base45Decode(text)
	if err != nil {
		return nil, false
	}
	if len(raw) < admissionCodeHeaderLen || raw[0] != AdmissionCodeVersion {
		return nil, false
	}
	n := int(raw[admissionCodeHeaderLen-1])
	if n < 1 || n > MaxAdmissionCodeTickets {
		return nil, false
	}
	signedLen := admissionCodeHeaderLen + 16*n + admissionCodeSignedTimeLen
	if len(raw) != signedLen+SignatureLen {
		return nil, false
	}

	c := &AdmissionCode{
		UserID:    UserID(uuid.UUID(raw[1:17]).String()),
		EventID:   uuid.UUID(raw[17:33]).String(),
		TicketIDs: make([]TicketID, 0, n),
	}
	for i := range n {
		off := admissionCodeHeaderLen + 16*i
		c.TicketIDs = append(c.TicketIDs, TicketID(uuid.UUID(raw[off:off+16]).String()))
	}
	signed := binary.BigEndian.Uint32(raw[signedLen-admissionCodeSignedTimeLen : signedLen])
	c.SignedTime = time.Unix(int64(signed), 0).UTC()
	c.Signature = Signature(append([]byte(nil), raw[signedLen:]...))

	if err := c.Validate(); err != nil {
		return nil, false
	}
	return c, true
}

// IsFreshAt reports whether the code is fresh at now: its signed time is at
// most 30 seconds before now and at most 15 seconds after it.
func (c *AdmissionCode) IsFreshAt(now time.Time) bool {
	age := now.Sub(c.SignedTime)
	return age <= AdmissionCodeMaxAge && age >= -AdmissionCodeMaxClockAhead
}

// Verify checks the code against key and the time now. It reports Forged when
// key is nil, is not the code's user's key, or the signature does not verify
// over the code's user, event, tickets and signed time; Expired when the
// signature verifies but the code is not fresh at now; Valid otherwise. It
// never fails.
func (c *AdmissionCode) Verify(key *WalletPublicKey, now time.Time) AdmissionCodeVerdict {
	if key == nil || key.UserID != c.UserID {
		return AdmissionCodeForged
	}
	payload, err := c.SignedPayload()
	if err != nil {
		return AdmissionCodeForged
	}
	if !VerifySignature(key.PublicKey, payload, c.Signature) {
		return AdmissionCodeForged
	}
	if !c.IsFreshAt(now) {
		return AdmissionCodeExpired
	}
	return AdmissionCodeValid
}

// base45Alphabet is the RFC 9285 alphabet, the QR alphanumeric character set.
const base45Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ $%*+-./:"

var errBase45 = errors.New("invalid base45")

// base45Encode encodes data per RFC 9285.
func base45Encode(data []byte) string {
	out := make([]byte, 0, (len(data)/2)*3+2)
	for i := 0; i+1 < len(data); i += 2 {
		n := int(data[i])<<8 | int(data[i+1])
		out = append(out, base45Alphabet[n%45], base45Alphabet[(n/45)%45], base45Alphabet[n/2025])
	}
	if len(data)%2 == 1 {
		n := int(data[len(data)-1])
		out = append(out, base45Alphabet[n%45], base45Alphabet[n/45])
	}
	return string(out)
}

// base45Decode decodes RFC 9285 text, rejecting characters outside the
// alphabet, a dangling single character and groups that overflow.
func base45Decode(text string) ([]byte, error) {
	if len(text)%3 == 1 {
		return nil, errBase45
	}
	vals := make([]int, len(text))
	for i := range len(text) {
		v := base45Index(text[i])
		if v < 0 {
			return nil, errBase45
		}
		vals[i] = v
	}
	out := make([]byte, 0, len(text)/3*2+1)
	i := 0
	for ; i+2 < len(vals); i += 3 {
		n := vals[i] + vals[i+1]*45 + vals[i+2]*2025
		if n > 0xFFFF {
			return nil, errBase45
		}
		out = append(out, byte(n>>8), byte(n))
	}
	if i < len(vals) {
		n := vals[i] + vals[i+1]*45
		if n > 0xFF {
			return nil, errBase45
		}
		out = append(out, byte(n))
	}
	return out, nil
}

// base45Index returns the value of c in the Base45 alphabet, or -1.
func base45Index(c byte) int {
	for i := range len(base45Alphabet) {
		if base45Alphabet[i] == c {
			return i
		}
	}
	return -1
}
