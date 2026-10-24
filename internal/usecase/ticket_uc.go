package usecase

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/entity"
)

// AdmitInput is one scan sent by a scanner device.
type AdmitInput struct {
	// Procedure is the full Connect procedure path the call was signed for.
	Procedure string
	// LinkToken is the secret part of the Scanner's link.
	LinkToken string
	// SignTime is when the device signed the call, by its clock.
	SignTime time.Time
	// Signature is the device's call signature.
	Signature entity.Signature
	// ScannedText is the text read from the QR code, undecoded.
	ScannedText string
	// Now is the current time.
	Now time.Time
}

// RejectedTicket is why one presented ticket was rejected. It does not
// identify the ticket or its user.
type RejectedTicket struct {
	// Reason is Voided or AlreadyAdmitted.
	Reason entity.RejectedScanReason
	// EarlierAdmitTime is, for AlreadyAdmitted, when the ticket was
	// admitted earlier.
	EarlierAdmitTime *time.Time
	// EarlierScannerNumber is, for AlreadyAdmitted, the number of the
	// Scanner it was admitted through; 0 when unknown.
	EarlierScannerNumber int
}

// AdmitResult is what staff see for one scan: a head count and the reasons
// for anything rejected. It carries no personal data by construction.
type AdmitResult struct {
	// AdmittedTicketCount is the number of tickets admitted by this scan.
	AdmittedTicketCount int
	// RejectedScanReason is set (Forged, Expired or OtherEvent) when the whole
	// scan was rejected before any ticket was considered; Unspecified
	// otherwise.
	RejectedScanReason entity.RejectedScanReason
	// RejectedTickets has one entry per rejected presented ticket, in the
	// code's order.
	RejectedTickets []RejectedTicket
}

// TicketUseCase is the fan-facing read surface over a caller's own Orders and
// issued Tickets, and the admission of Tickets at the venue.
type TicketUseCase interface {
	// List returns every Ticket whose user is the calling fan, Voided
	// ones included; empty when the fan has none.
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	List(ctx context.Context, userID entity.UserID) ([]*entity.Ticket, error)

	// Admit decides one scan: through a usable Scanner, from its bound device,
	// inside its event's admission window, it admits each ticket a genuine and
	// fresh AdmissionCode for its event presents exactly once, records every
	// refusal as a RejectedScan, and returns the head count and reasons. A code
	// presenting any ticket that is not its user's own for the event is
	// rejected as a whole as Forged. A rejected code or ticket is a result,
	// not an error.
	//
	// # Possible errors
	//
	//  - PermissionDenied: no Scanner holds the link token (cause
	//    [ErrUnknownScannerLinkToken]), the Scanner is Revoked (also when revoked
	//    while the scan is decided), or the call is not proven by the bound
	//    device. Nothing is admitted.
	//  - FailedPrecondition: now is outside the admission window.
	//    Nothing is admitted or recorded.
	//  - Unavailable: admitting a ticket failed; scan again.
	//  - Internal: database failure before any ticket was considered.
	Admit(ctx context.Context, in AdmitInput) (*AdmitResult, error)
}

// ticketUseCase implements [TicketUseCase].
type ticketUseCase struct {
	orderRepo        entity.OrderRepository
	ticketRepo       entity.TicketRepository
	scannerRepo      entity.ScannerRepository
	eventRepo        entity.EventRepository
	walletKeyRepo    entity.WalletPublicKeyRepository
	admissionRepo    entity.AdmissionRepository
	rejectedScanRepo entity.RejectedScanRepository
	publisher        EventPublisher
	logger           *logging.Logger
}

// Compile-time interface compliance check.
var _ TicketUseCase = (*ticketUseCase)(nil)

// NewTicketUseCase constructs a TicketUseCase. All parameters are required.
func NewTicketUseCase(
	orderRepo entity.OrderRepository,
	ticketRepo entity.TicketRepository,
	scannerRepo entity.ScannerRepository,
	eventRepo entity.EventRepository,
	walletKeyRepo entity.WalletPublicKeyRepository,
	admissionRepo entity.AdmissionRepository,
	rejectedScanRepo entity.RejectedScanRepository,
	publisher EventPublisher,
	logger *logging.Logger,
) TicketUseCase {
	return &ticketUseCase{
		orderRepo:        orderRepo,
		ticketRepo:       ticketRepo,
		scannerRepo:      scannerRepo,
		eventRepo:        eventRepo,
		walletKeyRepo:    walletKeyRepo,
		admissionRepo:    admissionRepo,
		rejectedScanRepo: rejectedScanRepo,
		publisher:        publisher,
		logger:           logger,
	}
}

// List implements [TicketUseCase].
func (uc *ticketUseCase) List(ctx context.Context, userID entity.UserID) ([]*entity.Ticket, error) {
	return uc.ticketRepo.ListByUser(ctx, userID)
}

// Admit implements [TicketUseCase].
func (uc *ticketUseCase) Admit(ctx context.Context, in AdmitInput) (*AdmitResult, error) {
	// -- the caller: a usable Scanner, the bound device, inside the window --
	scanner, err := uc.scannerRepo.GetByLinkToken(ctx, in.LinkToken)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, scannerRefused(ErrUnknownScannerLinkToken)
		}
		return nil, err
	}
	call := entity.ScannerCall{
		Procedure: in.Procedure,
		LinkToken: in.LinkToken,
		Content:   in.ScannedText,
		SignTime:  in.SignTime,
		Signature: in.Signature,
	}
	if !scanner.IsUsable() || !scanner.Proves(call, in.Now) {
		return nil, scannerRefused(errScannerRefused)
	}
	event, err := uc.eventRepo.Get(ctx, scanner.EventID)
	if err != nil {
		return nil, err
	}
	window, ok := entity.AdmissionWindowOf(event)
	if !ok || !window.Contains(in.Now) {
		return nil, apperr.New(codes.FailedPrecondition, "outside the admission window")
	}

	// -- the code: genuine, fresh, for this event, only the user's own tickets --
	code, reason, err := uc.checkCode(ctx, in.ScannedText, scanner.EventID, in.Now)
	if err != nil {
		return nil, err
	}
	if reason != entity.RejectedScanReasonUnspecified {
		uc.appendRejections(ctx, wholeScanRejections(scanner, code, reason, in.Now))
		return &AdmitResult{RejectedScanReason: reason}, nil
	}

	// -- each presented ticket, independently --
	result := &AdmitResult{}
	var rejections []*entity.RejectedScan
	reject := func(ticketID entity.TicketID, rt RejectedTicket) {
		result.RejectedTickets = append(result.RejectedTickets, rt)
		rejections = append(rejections, entity.NewRejectedScan(scanner.EventID, scanner.ID, ticketID, rt.Reason, in.Now))
	}
	for _, ticketID := range code.TicketIDs {
		admitted, err := uc.ticketRepo.Admit(ctx, ticketID, scanner.ID, in.Now)
		if err != nil {
			// Refusals decided so far are still recorded.
			uc.appendRejections(ctx, rejections)
			if errors.Is(err, entity.ErrScannerNotUsable) {
				return nil, scannerRefused(errScannerRefused)
			}
			return nil, apperr.Wrap(err, codes.Unavailable, "failed to admit a ticket; scan again")
		}
		switch admitted.Outcome {
		case entity.AdmitOutcomeAdmitted:
			result.AdmittedTicketCount++
			uc.reportAdmitted(ctx, ticketID, code.UserID, scanner.EventID, admitted.AdmitTime)
		case entity.AdmitOutcomeAlreadyAdmitted:
			reject(ticketID, uc.alreadyAdmitted(ctx, ticketID, admitted.AdmitTime))
		case entity.AdmitOutcomeVoided:
			reject(ticketID, RejectedTicket{Reason: entity.RejectedScanReasonVoided})
		}
	}
	uc.appendRejections(ctx, rejections)
	return result, nil
}

// checkCode decodes and verifies the scanned text. It returns the decoded
// code (nil when Malformed) and, when the whole scan is rejected, the reason
// (Forged, Expired or OtherEvent); the reason is Unspecified for a genuine,
// fresh code for eventID that presents only the user's own tickets of it.
//
// A code presenting any ticket the user does not hold for eventID (another
// account's, or the user's own for another event) is Forged as a whole: the
// tickets screen only ever presents the user's own tickets of one event, and
// a ticket's user never changes, so such a code was built outside the app.
func (uc *ticketUseCase) checkCode(ctx context.Context, text, eventID string, now time.Time) (*entity.AdmissionCode, entity.RejectedScanReason, error) {
	code, ok := entity.DecodeAdmissionCode(text)
	if !ok {
		return nil, entity.RejectedScanReasonForged, nil
	}
	key, err := uc.walletKeyRepo.GetByUser(ctx, code.UserID)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return code, entity.RejectedScanReasonForged, nil
		}
		return nil, entity.RejectedScanReasonUnspecified, err
	}
	switch code.Verify(key, now) {
	case entity.AdmissionCodeForged:
		return code, entity.RejectedScanReasonForged, nil
	case entity.AdmissionCodeExpired:
		return code, entity.RejectedScanReasonExpired, nil
	}
	if code.EventID != eventID {
		return code, entity.RejectedScanReasonOtherEvent, nil
	}
	held, err := uc.ticketRepo.ListByUserAndEvent(ctx, code.UserID, eventID)
	if err != nil {
		return nil, entity.RejectedScanReasonUnspecified, err
	}
	heldIDs := make(map[entity.TicketID]struct{}, len(held))
	for _, t := range held {
		heldIDs[t.ID] = struct{}{}
	}
	for _, id := range code.TicketIDs {
		if _, ok := heldIDs[id]; !ok {
			return code, entity.RejectedScanReasonForged, nil
		}
	}
	return code, entity.RejectedScanReasonUnspecified, nil
}

// wholeScanRejections returns the RejectedScans of a scan rejected as a
// whole: one without a ticket for Forged, one per presented ticket for
// Expired and OtherEvent (the signature verified, so the tickets are known).
func wholeScanRejections(scanner *entity.Scanner, code *entity.AdmissionCode, reason entity.RejectedScanReason, now time.Time) []*entity.RejectedScan {
	if reason == entity.RejectedScanReasonForged {
		return []*entity.RejectedScan{entity.NewRejectedScan(scanner.EventID, scanner.ID, "", reason, now)}
	}
	scans := make([]*entity.RejectedScan, 0, len(code.TicketIDs))
	for _, id := range code.TicketIDs {
		scans = append(scans, entity.NewRejectedScan(scanner.EventID, scanner.ID, id, reason, now))
	}
	return scans
}

// alreadyAdmitted builds the AlreadyAdmitted rejection with the earlier
// admission's time and Scanner number. When the Admission cannot be read the
// ticket's admitted time is still reported, without a Scanner number.
func (uc *ticketUseCase) alreadyAdmitted(ctx context.Context, ticketID entity.TicketID, admittedTime time.Time) RejectedTicket {
	rt := RejectedTicket{Reason: entity.RejectedScanReasonAlreadyAdmitted}
	admission, err := uc.admissionRepo.GetByTicket(ctx, ticketID)
	if err != nil {
		uc.logger.Warn(ctx, "failed to read the earlier admission of an already admitted ticket",
			slog.String("ticket_id", string(ticketID)), slog.Any("error", err))
		t := admittedTime
		rt.EarlierAdmitTime = &t
		return rt
	}
	t := admission.AdmitTime
	rt.EarlierAdmitTime = &t
	rt.EarlierScannerNumber = admission.ScannerNumber
	return rt
}

// appendRejections records refusals. Losing one is acceptable, so a failure
// is logged and never fails the scan.
func (uc *ticketUseCase) appendRejections(ctx context.Context, scans []*entity.RejectedScan) {
	if len(scans) == 0 {
		return
	}
	if err := uc.rejectedScanRepo.Append(ctx, scans); err != nil {
		uc.logger.Warn(ctx, "failed to record rejected scans", slog.Int("count", len(scans)), slog.Any("error", err))
	}
}

// reportAdmitted announces, for product analytics, that the Ticket was
// admitted. A failed publish is logged and never changes the admission.
func (uc *ticketUseCase) reportAdmitted(ctx context.Context, ticketID entity.TicketID, userID entity.UserID, eventID string, admitTime time.Time) {
	if err := uc.publisher.PublishEvent(ctx, entity.SubjectTicketAdmitted, entity.TicketAdmittedData{
		TicketID:  string(ticketID),
		UserID:    string(userID),
		EventID:   eventID,
		AdmitTime: admitTime,
	}); err != nil {
		uc.logger.Error(ctx, "failed to publish ORDER.ticket_admitted event", err,
			slog.String("ticket_id", string(ticketID)))
	}
}
