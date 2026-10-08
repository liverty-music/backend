package usecase

import (
	"context"
	"errors"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/entity"
)

// ErrUnknownReceptionLinkToken is the cause of the PermissionDenied returned
// when no ReceptionLink holds the call's token. Callers see the same
// PermissionDenied as for a revoked link or an unproven call; the boundary
// uses this cause only to throttle clients that guess tokens.
var ErrUnknownReceptionLinkToken = errors.New("reception link refused")

// errReceptionLinkRefused is the cause of every other reception refusal
// (revoked link, unproven call). Its text equals that of
// [ErrUnknownReceptionLinkToken] so the client cannot tell them apart.
var errReceptionLinkRefused = errors.New("reception link refused")

// receptionRefused returns the non-revealing PermissionDenied of a reception
// call.
func receptionRefused(cause error) error {
	return apperr.Wrap(cause, codes.PermissionDenied, "reception link refused")
}

// OpenReceptionLinkInput is one Open call from a reception device.
type OpenReceptionLinkInput struct {
	// Procedure is the full Connect procedure path the call was signed for.
	Procedure string
	// LinkToken is the secret part of the link's URL.
	LinkToken string
	// PublicKey is the public key of the key pair the device created.
	PublicKey entity.PublicKey
	// SignTime is when the device signed the call, by its clock.
	SignTime time.Time
	// Signature is the device's call signature.
	Signature entity.Signature
	// Now is the current time.
	Now time.Time
}

// OpenReceptionLinkResult tells the device what it can admit and when.
type OpenReceptionLinkResult struct {
	// Link is the link, InUse and bound to the calling device; its token is
	// never set.
	Link *entity.ReceptionLink
	// Window is the reception window from the event's current date and times;
	// nil when the event has no start time.
	Window *entity.ReceptionWindow
	// InsideWindow reports whether Now is inside Window (false without one).
	InsideWindow bool
}

// ReceptionLinkUseCase lets an organizer operator issue, list and revoke the
// ReceptionLinks of their own events, and lets a reception device open one.
type ReceptionLinkUseCase interface {
	// Issue creates an Unused ReceptionLink, numbered after the event's earlier
	// links, for one of organizerID's published events with a start time, and
	// returns it with its token, whether or not the reception window has
	// opened.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the event does not exist or is not owned by
	//    organizerID (non-revealing).
	//  - FailedPrecondition: the event is not published, or has no start time.
	//  - Internal: database failure.
	Issue(ctx context.Context, organizerID, eventID string) (*entity.ReceptionLink, error)

	// ListByEvent returns every ReceptionLink of one of organizerID's events
	// by number, with the token only on Unused links.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the event does not exist or is not owned by
	//    organizerID (non-revealing).
	//  - Internal: database failure.
	ListByEvent(ctx context.Context, organizerID, eventID string) ([]*entity.ReceptionLink, error)

	// Revoke revokes one of organizerID's links at now and returns it; an
	// already Revoked link is returned unchanged.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the link does not exist or its event is not owned
	//    by organizerID (non-revealing).
	//  - Internal: database failure.
	Revoke(ctx context.Context, organizerID string, linkID entity.ReceptionLinkID, now time.Time) (*entity.ReceptionLink, error)

	// Open binds the link to the device's public key on first use and tells
	// the device its reception window. Opening outside the window still binds.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the public key is not a valid P-256 key.
	//  - PermissionDenied: no link holds the token (cause
	//    [ErrUnknownReceptionLinkToken]), the call is not proven by the given
	//    public key (nothing is bound), or the link is Revoked. Not told apart.
	//  - FailedPrecondition: the link is bound to another device.
	//  - Internal: database failure.
	Open(ctx context.Context, in OpenReceptionLinkInput) (*OpenReceptionLinkResult, error)
}

// receptionLinkUseCase implements [ReceptionLinkUseCase].
type receptionLinkUseCase struct {
	links          entity.ReceptionLinkRepository
	events         entity.EventRepository
	eventOrganizer EventOrganizerRepository
	eventState     EventPublishStatePort
	logger         *logging.Logger
}

// Compile-time interface compliance check.
var _ ReceptionLinkUseCase = (*receptionLinkUseCase)(nil)

// NewReceptionLinkUseCase constructs a ReceptionLinkUseCase. All parameters
// are required.
func NewReceptionLinkUseCase(
	links entity.ReceptionLinkRepository,
	events entity.EventRepository,
	eventOrganizer EventOrganizerRepository,
	eventState EventPublishStatePort,
	logger *logging.Logger,
) ReceptionLinkUseCase {
	return &receptionLinkUseCase{
		links:          links,
		events:         events,
		eventOrganizer: eventOrganizer,
		eventState:     eventState,
		logger:         logger,
	}
}

// assertOwnsEvent fails with PermissionDenied, without revealing whether the
// event exists, unless organizerID owns the event.
func (uc *receptionLinkUseCase) assertOwnsEvent(ctx context.Context, organizerID, eventID string) error {
	owner, err := uc.eventOrganizer.GetOrganizerID(ctx, eventID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return apperr.New(codes.PermissionDenied, "permission denied")
		}
		return err
	}
	if owner != organizerID {
		return apperr.New(codes.PermissionDenied, "permission denied")
	}
	return nil
}

// Issue implements [ReceptionLinkUseCase].
func (uc *receptionLinkUseCase) Issue(ctx context.Context, organizerID, eventID string) (*entity.ReceptionLink, error) {
	if err := uc.assertOwnsEvent(ctx, organizerID, eventID); err != nil {
		return nil, err
	}
	published, err := uc.eventState.IsEventPublished(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if !published {
		return nil, apperr.New(codes.FailedPrecondition, "a reception link can be issued only for a published event")
	}
	event, err := uc.events.Get(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if event.StartTime == nil {
		return nil, apperr.New(codes.FailedPrecondition, "a reception link can be issued only for an event with a start time")
	}
	return uc.links.Create(ctx, entity.NewReceptionLink(eventID))
}

// ListByEvent implements [ReceptionLinkUseCase].
func (uc *receptionLinkUseCase) ListByEvent(ctx context.Context, organizerID, eventID string) ([]*entity.ReceptionLink, error) {
	if err := uc.assertOwnsEvent(ctx, organizerID, eventID); err != nil {
		return nil, err
	}
	links, err := uc.links.ListByEvent(ctx, eventID)
	if err != nil {
		return nil, err
	}
	for _, l := range links {
		if l.Status != entity.ReceptionLinkStatusUnused {
			l.Token = ""
		}
	}
	return links, nil
}

// Revoke implements [ReceptionLinkUseCase].
func (uc *receptionLinkUseCase) Revoke(ctx context.Context, organizerID string, linkID entity.ReceptionLinkID, now time.Time) (*entity.ReceptionLink, error) {
	link, err := uc.links.Get(ctx, linkID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, apperr.New(codes.PermissionDenied, "permission denied")
		}
		return nil, err
	}
	if err := uc.assertOwnsEvent(ctx, organizerID, link.EventID); err != nil {
		return nil, err
	}
	revoked, err := uc.links.Revoke(ctx, linkID, now)
	if err != nil {
		return nil, err
	}
	revoked.Token = ""
	return revoked, nil
}

// Open implements [ReceptionLinkUseCase].
func (uc *receptionLinkUseCase) Open(ctx context.Context, in OpenReceptionLinkInput) (*OpenReceptionLinkResult, error) {
	link, err := uc.links.GetByToken(ctx, in.LinkToken)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, receptionRefused(ErrUnknownReceptionLinkToken)
		}
		return nil, err
	}
	if err := in.PublicKey.Validate(); err != nil {
		return nil, err
	}

	call := entity.ReceptionCall{
		Procedure: in.Procedure,
		Token:     in.LinkToken,
		Content:   in.PublicKey.Base64URL(),
		SignTime:  in.SignTime,
		Signature: in.Signature,
	}
	// The call must be proven by the given key before anything is bound, so a
	// link is only ever bound to a key whose private half the device holds.
	if !call.IsSignedBy(in.PublicKey, in.Now) {
		return nil, receptionRefused(errReceptionLinkRefused)
	}

	outcome, bound, err := uc.links.BindDevice(ctx, link.ID, in.PublicKey, in.Now)
	if err != nil {
		return nil, err
	}
	switch outcome {
	case entity.BindOutcomeRevoked:
		return nil, receptionRefused(errReceptionLinkRefused)
	case entity.BindOutcomeOtherDevice:
		return nil, apperr.New(codes.FailedPrecondition, "the reception link is already in use on another device")
	}

	event, err := uc.events.Get(ctx, bound.EventID)
	if err != nil {
		return nil, err
	}
	bound.Token = ""
	result := &OpenReceptionLinkResult{Link: bound}
	if window, ok := entity.ReceptionWindowOf(event); ok {
		result.Window = &window
		result.InsideWindow = window.Contains(in.Now)
	}
	return result, nil
}
