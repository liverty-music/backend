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

// ErrUnknownScannerLinkToken is the cause of the PermissionDenied returned
// when no Scanner holds the call's link token. Callers see the same
// PermissionDenied as for a revoked Scanner or an unproven call; the boundary
// uses this cause only to throttle clients that guess link tokens.
var ErrUnknownScannerLinkToken = errors.New("scanner refused")

// errScannerRefused is the cause of every other scanner refusal (revoked
// Scanner, unproven call). Its text equals that of
// [ErrUnknownScannerLinkToken] so the client cannot tell them apart.
var errScannerRefused = errors.New("scanner refused")

// scannerRefused returns the non-revealing PermissionDenied of a scanner
// call.
func scannerRefused(cause error) error {
	return apperr.Wrap(cause, codes.PermissionDenied, "scanner refused")
}

// OpenScannerInput is one Open call from a scanner device.
type OpenScannerInput struct {
	// Procedure is the full Connect procedure path the call was signed for.
	Procedure string
	// LinkToken is the secret part of the Scanner's link.
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

// OpenScannerResult tells the device what it can admit and when.
type OpenScannerResult struct {
	// Scanner is the Scanner, InUse and bound to the calling device; its link
	// token is never set.
	Scanner *entity.Scanner
	// Window is the admission window from the event's current date and times;
	// nil when the event has no start time.
	Window *entity.AdmissionWindow
	// InsideWindow reports whether Now is inside Window (false without one).
	InsideWindow bool
}

// ScannerUseCase lets an organizer operator create, list and revoke the
// Scanners of their own events, and lets a scanner device open one.
type ScannerUseCase interface {
	// Create creates an Unused Scanner, numbered after the event's earlier
	// Scanners, for one of organizerID's published events with a start time,
	// and returns it with its link token, whether or not the admission window
	// has opened.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the event does not exist or is not owned by
	//    organizerID (non-revealing).
	//  - FailedPrecondition: the event is not published, or has no start time.
	//  - Internal: database failure.
	Create(ctx context.Context, organizerID, eventID string) (*entity.Scanner, error)

	// ListByEvent returns every Scanner of one of organizerID's events by
	// number, with the link token only on Unused Scanners.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the event does not exist or is not owned by
	//    organizerID (non-revealing).
	//  - Internal: database failure.
	ListByEvent(ctx context.Context, organizerID, eventID string) ([]*entity.Scanner, error)

	// Revoke revokes one of organizerID's Scanners at now and returns it; an
	// already Revoked Scanner is returned unchanged.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the Scanner does not exist or its event is not
	//    owned by organizerID (non-revealing).
	//  - Internal: database failure.
	Revoke(ctx context.Context, organizerID string, scannerID entity.ScannerID, now time.Time) (*entity.Scanner, error)

	// Open binds the Scanner to the device's public key on first use and
	// tells the device its admission window. Opening outside the window still
	// binds.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the public key is not a valid P-256 key.
	//  - PermissionDenied: no Scanner holds the link token (cause
	//    [ErrUnknownScannerLinkToken]), the call is not proven by the given
	//    public key (nothing is bound), or the Scanner is Revoked. Not told
	//    apart.
	//  - FailedPrecondition: the Scanner is bound to another device.
	//  - Internal: database failure.
	Open(ctx context.Context, in OpenScannerInput) (*OpenScannerResult, error)
}

// scannerUseCase implements [ScannerUseCase].
type scannerUseCase struct {
	scanners       entity.ScannerRepository
	events         entity.EventRepository
	eventOrganizer EventOrganizerRepository
	eventState     EventPublishStatePort
	logger         *logging.Logger
}

// Compile-time interface compliance check.
var _ ScannerUseCase = (*scannerUseCase)(nil)

// NewScannerUseCase constructs a ScannerUseCase. All parameters are
// required.
func NewScannerUseCase(
	scanners entity.ScannerRepository,
	events entity.EventRepository,
	eventOrganizer EventOrganizerRepository,
	eventState EventPublishStatePort,
	logger *logging.Logger,
) ScannerUseCase {
	return &scannerUseCase{
		scanners:       scanners,
		events:         events,
		eventOrganizer: eventOrganizer,
		eventState:     eventState,
		logger:         logger,
	}
}

// assertOwnsEvent fails with PermissionDenied, without revealing whether the
// event exists, unless organizerID owns the event.
func (uc *scannerUseCase) assertOwnsEvent(ctx context.Context, organizerID, eventID string) error {
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

// Create implements [ScannerUseCase].
func (uc *scannerUseCase) Create(ctx context.Context, organizerID, eventID string) (*entity.Scanner, error) {
	if err := uc.assertOwnsEvent(ctx, organizerID, eventID); err != nil {
		return nil, err
	}
	published, err := uc.eventState.IsEventPublished(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if !published {
		return nil, apperr.New(codes.FailedPrecondition, "a scanner can be created only for a published event")
	}
	event, err := uc.events.Get(ctx, eventID)
	if err != nil {
		return nil, err
	}
	if event.StartTime == nil {
		return nil, apperr.New(codes.FailedPrecondition, "a scanner can be created only for an event with a start time")
	}
	return uc.scanners.Create(ctx, entity.NewScanner(eventID))
}

// ListByEvent implements [ScannerUseCase].
func (uc *scannerUseCase) ListByEvent(ctx context.Context, organizerID, eventID string) ([]*entity.Scanner, error) {
	if err := uc.assertOwnsEvent(ctx, organizerID, eventID); err != nil {
		return nil, err
	}
	scanners, err := uc.scanners.ListByEvent(ctx, eventID)
	if err != nil {
		return nil, err
	}
	for _, s := range scanners {
		if s.Status != entity.ScannerStatusUnused {
			s.LinkToken = ""
		}
	}
	return scanners, nil
}

// Revoke implements [ScannerUseCase].
func (uc *scannerUseCase) Revoke(ctx context.Context, organizerID string, scannerID entity.ScannerID, now time.Time) (*entity.Scanner, error) {
	scanner, err := uc.scanners.Get(ctx, scannerID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, apperr.New(codes.PermissionDenied, "permission denied")
		}
		return nil, err
	}
	if err := uc.assertOwnsEvent(ctx, organizerID, scanner.EventID); err != nil {
		return nil, err
	}
	revoked, err := uc.scanners.Revoke(ctx, scannerID, now)
	if err != nil {
		return nil, err
	}
	revoked.LinkToken = ""
	return revoked, nil
}

// Open implements [ScannerUseCase].
func (uc *scannerUseCase) Open(ctx context.Context, in OpenScannerInput) (*OpenScannerResult, error) {
	scanner, err := uc.scanners.GetByLinkToken(ctx, in.LinkToken)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, scannerRefused(ErrUnknownScannerLinkToken)
		}
		return nil, err
	}
	if err := in.PublicKey.Validate(); err != nil {
		return nil, err
	}

	call := entity.ScannerCall{
		Procedure: in.Procedure,
		LinkToken: in.LinkToken,
		Content:   in.PublicKey.Base64URL(),
		SignTime:  in.SignTime,
		Signature: in.Signature,
	}
	// The call must be proven by the given key before anything is bound, so a
	// Scanner is only ever bound to a key whose private half the device holds.
	if !call.IsSignedBy(in.PublicKey, in.Now) {
		return nil, scannerRefused(errScannerRefused)
	}

	outcome, bound, err := uc.scanners.BindDevice(ctx, scanner.ID, in.PublicKey, in.Now)
	if err != nil {
		return nil, err
	}
	switch outcome {
	case entity.BindOutcomeRevoked:
		return nil, scannerRefused(errScannerRefused)
	case entity.BindOutcomeOtherDevice:
		return nil, apperr.New(codes.FailedPrecondition, "the scanner is already in use on another device")
	}

	event, err := uc.events.Get(ctx, bound.EventID)
	if err != nil {
		return nil, err
	}
	bound.LinkToken = ""
	result := &OpenScannerResult{Scanner: bound}
	if window, ok := entity.AdmissionWindowOf(event); ok {
		result.Window = &window
		result.InsideWindow = window.Contains(in.Now)
	}
	return result, nil
}
