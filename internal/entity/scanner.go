package entity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"time"
)

// ScannerID is the identifier of a [Scanner] (UUIDv7).
//
// Mirrors liverty_music.entity.v1.ScannerId.value.
type ScannerID string

// ScannerStatus is the lifecycle of a [Scanner]:
// Unused → InUse → Revoked, or Unused → Revoked. Values mirror the proto enum
// liverty_music.entity.v1.ScannerStatus.
type ScannerStatus int16

const (
	// ScannerStatusUnspecified is the zero value and is never persisted.
	ScannerStatusUnspecified ScannerStatus = 0
	// ScannerStatusUnused means no device has opened the Scanner's link yet.
	ScannerStatusUnused ScannerStatus = 1
	// ScannerStatusInUse means a device opened the link and bound its key.
	ScannerStatusInUse ScannerStatus = 2
	// ScannerStatusRevoked means the Organizer revoked the Scanner. Terminal.
	ScannerStatusRevoked ScannerStatus = 3
)

const (
	// scannerLinkTokenBytes is the random size of a link token: 32 bytes
	// (256 bits, above the 128-bit minimum), 43 base64url characters.
	scannerLinkTokenBytes = 32

	// ScannerCallDomain is the first line of every scanner call signature
	// input.
	ScannerCallDomain = "liverty-music.scanner.v1"

	// admissionWindowLead is how long before the open (or start) time the
	// admission window opens.
	admissionWindowLead = 3 * time.Hour
	// admissionWindowCloseHour is the hour (Japan time) on the day after the
	// event's local date at which the admission window closes.
	admissionWindowCloseHour = 4
)

// japanTime is Japan Standard Time. Japan observes no daylight saving time,
// so a fixed +09:00 zone is exact and needs no tzdata.
var japanTime = time.FixedZone("Asia/Tokyo", 9*60*60)

// Scanner is one device's right to admit fans to one event during the
// event's admission window, issued by the Organizer for venue staff without
// an account. The Organizer hands it over as a link that carries its link
// token; the first device that opens the link binds its public key, and every
// later call must be signed with that device's private key.
//
// Mirrors liverty_music.entity.v1.Scanner.
type Scanner struct {
	// ID is the Scanner's identity (UUIDv7).
	ID ScannerID
	// EventID is the one event the Scanner admits to.
	EventID string
	// Number is the Scanner's number within its event in issue order
	// (1, 2 ...), never reused; shown as 受付1, 受付2.
	Number int
	// LinkToken is the secret part of the Scanner's link. Set only while the
	// Scanner is Unused (and on creation); empty once bound or revoked.
	LinkToken string
	// BoundPublicKey is the key of the device the Scanner is bound to; nil
	// until bound.
	BoundPublicKey PublicKey
	// Status is the lifecycle status.
	Status ScannerStatus
	// BoundTime is when a device first opened the link; nil until bound.
	BoundTime *time.Time
	// RevokedTime is when the Organizer revoked the Scanner; nil until
	// revoked.
	RevokedTime *time.Time
}

// NewScanner returns a new Unused Scanner for eventID with a fresh id and a
// fresh random link token. Its Number is assigned when it is stored.
func NewScanner(eventID string) *Scanner {
	buf := make([]byte, scannerLinkTokenBytes)
	// crypto/rand.Read never returns an error (it panics if the system
	// entropy source fails), so the error is not checked.
	_, _ = rand.Read(buf)
	return &Scanner{
		ID:        ScannerID(NewID()),
		EventID:   eventID,
		LinkToken: base64.RawURLEncoding.EncodeToString(buf),
		Status:    ScannerStatusUnused,
	}
}

// HashScannerLinkToken returns the SHA-256 of a link token, the only form
// in which link tokens are looked up, so that lookups compare fixed-size
// digests rather than the secret itself.
func HashScannerLinkToken(linkToken string) []byte {
	sum := sha256.Sum256([]byte(linkToken))
	return sum[:]
}

// IsUsable reports whether the Scanner can be used: Unused or InUse.
func (s *Scanner) IsUsable() bool {
	return s.Status == ScannerStatusUnused || s.Status == ScannerStatusInUse
}

// ScannerCall is one signed call from a [Scanner]'s device.
type ScannerCall struct {
	// Procedure is the full Connect procedure path, e.g.
	// /liverty_music.rpc.scanner.v1.ScannerService/Admit.
	Procedure string
	// LinkToken is the link token the call carries.
	LinkToken string
	// Content is the call's content: for Open the device public key in
	// base64url without padding, for Admit the scanned text.
	Content string
	// SignTime is when the device signed the call, by its clock.
	SignTime time.Time
	// Signature is the device's signature over [ScannerCall.SignatureInput].
	Signature Signature
}

// SignatureInput returns the bytes a scanner call is signed over: the UTF-8
// lines domain, procedure, link token, sign time in decimal Unix seconds and
// content, joined by "\n" with no trailing newline.
func (c ScannerCall) SignatureInput() []byte {
	return []byte(strings.Join([]string{
		ScannerCallDomain,
		c.Procedure,
		c.LinkToken,
		strconv.FormatInt(c.SignTime.Unix(), 10),
		c.Content,
	}, "\n"))
}

// IsSignedBy reports whether the call's signature verifies with key and its
// sign time is at most 30 seconds before and 15 seconds after now.
func (c ScannerCall) IsSignedBy(key PublicKey, now time.Time) bool {
	age := now.Sub(c.SignTime)
	if age > AdmissionCodeMaxAge || age < -AdmissionCodeMaxClockAhead {
		return false
	}
	return VerifySignature(key, c.SignatureInput(), c.Signature)
}

// Proves reports whether call is proven by the Scanner's bound device at now:
// the Scanner is InUse and the call is signed with its bound public key over
// the link token, the content and a sign time at most 30 seconds before and
// 15 seconds after now. A call to an Unused or Revoked Scanner is never
// proven.
func (s *Scanner) Proves(call ScannerCall, now time.Time) bool {
	if s.Status != ScannerStatusInUse || s.BoundPublicKey == nil {
		return false
	}
	return call.IsSignedBy(s.BoundPublicKey, now)
}

// AdmissionWindow is when the Scanners of an event can admit: from OpenTime
// (inclusive) to CloseTime (exclusive).
//
// Mirrors liverty_music.entity.v1.AdmissionWindow.
type AdmissionWindow struct {
	// OpenTime is when admission opens (inclusive).
	OpenTime time.Time
	// CloseTime is when admission closes (exclusive).
	CloseTime time.Time
}

// Contains reports whether t is at or after the opening and before the
// closing.
func (w AdmissionWindow) Contains(t time.Time) bool {
	return !t.Before(w.OpenTime) && t.Before(w.CloseTime)
}

// AdmissionWindowOf derives the admission window from the event's current
// local date, open time and start time, in Japan time: it opens 3 hours
// before the open time, or before the start time when the event has no open
// time, and closes at 04:00 on the day after the local date. ok is false
// when the event has no start time (then it has no window).
func AdmissionWindowOf(event *Event) (window AdmissionWindow, ok bool) {
	if event == nil || event.StartTime == nil {
		return AdmissionWindow{}, false
	}
	anchor := *event.StartTime
	if event.OpenTime != nil {
		anchor = *event.OpenTime
	}
	y, m, d := event.LocalDate.Date()
	return AdmissionWindow{
		OpenTime:  anchor.Add(-admissionWindowLead),
		CloseTime: time.Date(y, m, d+1, admissionWindowCloseHour, 0, 0, 0, japanTime),
	}, true
}

// BindOutcome is the outcome of [ScannerRepository.BindDevice].
type BindOutcome int

const (
	// BindOutcomeBound means the Scanner is now (or already was) bound to the
	// key.
	BindOutcomeBound BindOutcome = iota + 1
	// BindOutcomeOtherDevice means the Scanner is bound to another key.
	BindOutcomeOtherDevice
	// BindOutcomeRevoked means the Scanner is Revoked.
	BindOutcomeRevoked
)

// ScannerRepository defines the persistence contract for [Scanner].
// Implementations live in internal/infrastructure/database/rdb/.
type ScannerRepository interface {
	// Create stores scanner (built by [NewScanner]) as a new Unused Scanner
	// of its event with the next number of that event — one more than the
	// highest number among the event's Scanners, Revoked included — and
	// returns it with its link token. Choosing the number and storing are one
	// indivisible step.
	//
	// # Possible errors
	//
	//  - NotFound: the event does not exist.
	//  - Internal: database failure.
	Create(ctx context.Context, scanner *Scanner) (*Scanner, error)

	// Get returns the Scanner with the id, whatever its status.
	//
	// # Possible errors
	//
	//  - NotFound: no Scanner has the id.
	//  - Internal: database failure.
	Get(ctx context.Context, id ScannerID) (*Scanner, error)

	// GetByLinkToken returns the Scanner holding linkToken, whatever its
	// status. The comparison takes the same time whatever the given link
	// token is.
	//
	// # Possible errors
	//
	//  - NotFound: no Scanner holds the link token.
	//  - Internal: database failure.
	GetByLinkToken(ctx context.Context, linkToken string) (*Scanner, error)

	// ListByEvent returns every Scanner of the event, Revoked included, by
	// number; empty when there is none.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListByEvent(ctx context.Context, eventID string) ([]*Scanner, error)

	// BindDevice binds the Scanner to key at boundTime when it is Unused and
	// reports Bound; reports Bound without change when it is InUse with key,
	// OtherDevice when InUse with another key, Revoked when Revoked. Checking
	// and binding are one indivisible step. It returns the Scanner as it is
	// afterwards.
	//
	// # Possible errors
	//
	//  - InvalidArgument: key is not a valid P-256 key.
	//  - NotFound: no Scanner has the id.
	//  - Internal: database failure.
	BindDevice(ctx context.Context, id ScannerID, key PublicKey, boundTime time.Time) (BindOutcome, *Scanner, error)

	// Revoke makes an Unused or InUse Scanner Revoked with revokedTime; a
	// Revoked Scanner is left unchanged. It returns the Scanner as it is
	// afterwards.
	//
	// # Possible errors
	//
	//  - NotFound: no Scanner has the id.
	//  - Internal: database failure.
	Revoke(ctx context.Context, id ScannerID, revokedTime time.Time) (*Scanner, error)
}
