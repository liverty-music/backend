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

// ReceptionLinkID is the identifier of a [ReceptionLink] (UUIDv7).
//
// Mirrors liverty_music.entity.v1.ReceptionLinkId.value.
type ReceptionLinkID string

// ReceptionLinkStatus is the lifecycle of a [ReceptionLink]:
// Unused → InUse → Revoked, or Unused → Revoked. Values mirror the proto enum
// liverty_music.entity.v1.ReceptionLinkStatus.
type ReceptionLinkStatus int16

const (
	// ReceptionLinkStatusUnspecified is the zero value and is never persisted.
	ReceptionLinkStatusUnspecified ReceptionLinkStatus = 0
	// ReceptionLinkStatusUnused means no device has opened the link yet.
	ReceptionLinkStatusUnused ReceptionLinkStatus = 1
	// ReceptionLinkStatusInUse means a device opened the link and bound its key.
	ReceptionLinkStatusInUse ReceptionLinkStatus = 2
	// ReceptionLinkStatusRevoked means the Organizer revoked the link. Terminal.
	ReceptionLinkStatusRevoked ReceptionLinkStatus = 3
)

const (
	// receptionLinkTokenBytes is the random size of a link token: 32 bytes
	// (256 bits, above the 128-bit minimum), 43 base64url characters.
	receptionLinkTokenBytes = 32

	// ReceptionCallDomain is the first line of every reception call signature
	// input.
	ReceptionCallDomain = "liverty-music.reception.v1"

	// receptionWindowLead is how long before the open (or start) time the
	// reception window opens.
	receptionWindowLead = 3 * time.Hour
	// receptionWindowCloseHour is the hour (Japan time) on the day after the
	// event's local date at which the reception window closes.
	receptionWindowCloseHour = 4
)

// japanTime is Japan Standard Time. Japan observes no daylight saving time,
// so a fixed +09:00 zone is exact and needs no tzdata.
var japanTime = time.FixedZone("Asia/Tokyo", 9*60*60)

// ReceptionLink is the link an Organizer issues so that one device, held by
// venue staff without an account, can admit fans to one event during the
// event's reception window. The first device that opens it binds its public
// key; every later call must be signed with that device's private key.
//
// Mirrors liverty_music.entity.v1.ReceptionLink.
type ReceptionLink struct {
	// ID is the link's identity (UUIDv7).
	ID ReceptionLinkID
	// EventID is the one event the link admits to.
	EventID string
	// Number is the link's number within its event in issue order (1, 2 ...),
	// never reused; shown as 受付1, 受付2.
	Number int
	// Token is the secret part of the link's URL. Set only while the link is
	// Unused (and on creation); empty once bound or revoked.
	Token string
	// BoundPublicKey is the key of the device the link is bound to; nil until
	// bound.
	BoundPublicKey PublicKey
	// Status is the lifecycle status.
	Status ReceptionLinkStatus
	// BoundTime is when a device first opened the link; nil until bound.
	BoundTime *time.Time
	// RevokedTime is when the Organizer revoked the link; nil until revoked.
	RevokedTime *time.Time
}

// NewReceptionLink returns a new Unused link for eventID with a fresh id and a
// fresh random token. Its Number is assigned when it is stored.
func NewReceptionLink(eventID string) *ReceptionLink {
	buf := make([]byte, receptionLinkTokenBytes)
	// crypto/rand.Read never returns an error (it panics if the system
	// entropy source fails), so the error is not checked.
	_, _ = rand.Read(buf)
	return &ReceptionLink{
		ID:      ReceptionLinkID(NewID()),
		EventID: eventID,
		Token:   base64.RawURLEncoding.EncodeToString(buf),
		Status:  ReceptionLinkStatusUnused,
	}
}

// HashReceptionLinkToken returns the SHA-256 of a link token, the only form
// in which tokens are looked up, so that lookups compare fixed-size digests
// rather than the secret itself.
func HashReceptionLinkToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// IsUsable reports whether the link can be used: Unused or InUse.
func (l *ReceptionLink) IsUsable() bool {
	return l.Status == ReceptionLinkStatusUnused || l.Status == ReceptionLinkStatusInUse
}

// ReceptionCall is one signed call through a [ReceptionLink].
type ReceptionCall struct {
	// Procedure is the full Connect procedure path, e.g.
	// /liverty_music.rpc.organizer.reception.v1.ReceptionService/Admit.
	Procedure string
	// Token is the link token the call carries.
	Token string
	// Content is the call's content: for Open the device public key in
	// base64url without padding, for Admit the scanned text.
	Content string
	// SignTime is when the device signed the call, by its clock.
	SignTime time.Time
	// Signature is the device's signature over [ReceptionCall.SignatureInput].
	Signature Signature
}

// SignatureInput returns the bytes a reception call is signed over: the UTF-8
// lines domain, procedure, token, sign time in decimal Unix seconds and
// content, joined by "\n" with no trailing newline.
func (c ReceptionCall) SignatureInput() []byte {
	return []byte(strings.Join([]string{
		ReceptionCallDomain,
		c.Procedure,
		c.Token,
		strconv.FormatInt(c.SignTime.Unix(), 10),
		c.Content,
	}, "\n"))
}

// IsSignedBy reports whether the call's signature verifies with key and its
// sign time is at most 30 seconds before and 15 seconds after now.
func (c ReceptionCall) IsSignedBy(key PublicKey, now time.Time) bool {
	age := now.Sub(c.SignTime)
	if age > AdmissionCodeMaxAge || age < -AdmissionCodeMaxClockAhead {
		return false
	}
	return VerifySignature(key, c.SignatureInput(), c.Signature)
}

// Proves reports whether call is proven by the link's bound device at now:
// the link is InUse and the call is signed with its bound public key over the
// token, the content and a sign time at most 30 seconds before and 15 seconds
// after now. A call through an Unused or Revoked link is never proven.
func (l *ReceptionLink) Proves(call ReceptionCall, now time.Time) bool {
	if l.Status != ReceptionLinkStatusInUse || l.BoundPublicKey == nil {
		return false
	}
	return call.IsSignedBy(l.BoundPublicKey, now)
}

// ReceptionWindow is when a ReceptionLink can admit: from OpenTime
// (inclusive) to CloseTime (exclusive).
//
// Mirrors liverty_music.entity.v1.ReceptionWindow.
type ReceptionWindow struct {
	// OpenTime is when reception opens (inclusive).
	OpenTime time.Time
	// CloseTime is when reception closes (exclusive).
	CloseTime time.Time
}

// Contains reports whether t is at or after the opening and before the
// closing.
func (w ReceptionWindow) Contains(t time.Time) bool {
	return !t.Before(w.OpenTime) && t.Before(w.CloseTime)
}

// ReceptionWindowOf derives the reception window from the event's current
// local date, open time and start time, in Japan time: it opens 3 hours
// before the open time, or before the start time when the event has no open
// time, and closes at 04:00 on the day after the local date. ok is false
// when the event has no start time (then it has no window).
func ReceptionWindowOf(event *Event) (window ReceptionWindow, ok bool) {
	if event == nil || event.StartTime == nil {
		return ReceptionWindow{}, false
	}
	anchor := *event.StartTime
	if event.OpenTime != nil {
		anchor = *event.OpenTime
	}
	y, m, d := event.LocalDate.Date()
	return ReceptionWindow{
		OpenTime:  anchor.Add(-receptionWindowLead),
		CloseTime: time.Date(y, m, d+1, receptionWindowCloseHour, 0, 0, 0, japanTime),
	}, true
}

// BindOutcome is the outcome of [ReceptionLinkRepository.BindDevice].
type BindOutcome int

const (
	// BindOutcomeBound means the link is now (or already was) bound to the key.
	BindOutcomeBound BindOutcome = iota + 1
	// BindOutcomeOtherDevice means the link is bound to another key.
	BindOutcomeOtherDevice
	// BindOutcomeRevoked means the link is Revoked.
	BindOutcomeRevoked
)

// ReceptionLinkRepository defines the persistence contract for
// [ReceptionLink]. Implementations live in internal/infrastructure/database/rdb/.
type ReceptionLinkRepository interface {
	// Create stores link (built by [NewReceptionLink]) as a new Unused link of
	// its event with the next number of that event — one more than the
	// highest number among the event's links, Revoked included — and returns
	// it with its token. Choosing the number and storing are one indivisible
	// step.
	//
	// # Possible errors
	//
	//  - NotFound: the event does not exist.
	//  - Internal: database failure.
	Create(ctx context.Context, link *ReceptionLink) (*ReceptionLink, error)

	// Get returns the link with the id, whatever its status.
	//
	// # Possible errors
	//
	//  - NotFound: no link has the id.
	//  - Internal: database failure.
	Get(ctx context.Context, id ReceptionLinkID) (*ReceptionLink, error)

	// GetByToken returns the link holding token, whatever its status. The
	// comparison takes the same time whatever the given token is.
	//
	// # Possible errors
	//
	//  - NotFound: no link holds the token.
	//  - Internal: database failure.
	GetByToken(ctx context.Context, token string) (*ReceptionLink, error)

	// ListByEvent returns every link of the event, Revoked included, by
	// number; empty when there is none.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListByEvent(ctx context.Context, eventID string) ([]*ReceptionLink, error)

	// BindDevice binds the link to key at boundTime when it is Unused and
	// reports Bound; reports Bound without change when it is InUse with key,
	// OtherDevice when InUse with another key, Revoked when Revoked. Checking
	// and binding are one indivisible step. It returns the link as it is
	// afterwards.
	//
	// # Possible errors
	//
	//  - InvalidArgument: key is not a valid P-256 key.
	//  - NotFound: no link has the id.
	//  - Internal: database failure.
	BindDevice(ctx context.Context, id ReceptionLinkID, key PublicKey, boundTime time.Time) (BindOutcome, *ReceptionLink, error)

	// Revoke makes an Unused or InUse link Revoked with revokedTime; a Revoked
	// link is left unchanged. It returns the link as it is afterwards.
	//
	// # Possible errors
	//
	//  - NotFound: no link has the id.
	//  - Internal: database failure.
	Revoke(ctx context.Context, id ReceptionLinkID, revokedTime time.Time) (*ReceptionLink, error)
}
