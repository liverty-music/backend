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

// AdmitInput is one scan sent by a reception device.
type AdmitInput struct {
	// Procedure is the full Connect procedure path the call was signed for.
	Procedure string
	// LinkToken is the secret part of the reception link's URL.
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
// identify the ticket or its holder.
type RejectedTicket struct {
	// Reason is NotHolder, Voided or AlreadyAdmitted.
	Reason entity.RejectedScanReason
	// EarlierAdmittedTime is, for AlreadyAdmitted, when the ticket was
	// admitted earlier.
	EarlierAdmittedTime *time.Time
	// EarlierReceptionLinkNumber is, for AlreadyAdmitted, the number of the
	// link it was admitted through; 0 when unknown.
	EarlierReceptionLinkNumber int
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

// TicketUseCase is the buyer-facing read surface over a caller's own Orders and
// issued Tickets, and the reception-side admission of Tickets at the venue.
type TicketUseCase interface {
	// GetOrder returns one of the caller's own Orders. An Order belonging to a
	// different account is reported as NotFound (non-revealing), never disclosed.
	//
	// # Possible errors
	//
	//  - NotFound: no such Order for this caller.
	//  - Internal: database query failure.
	GetOrder(ctx context.Context, buyerID entity.UserID, orderID entity.OrderID) (*entity.Order, error)

	// GetMyTickets returns all tickets currently bound to the caller's account.
	//
	// # Possible errors
	//
	//  - Internal: database query failure.
	GetMyTickets(ctx context.Context, holderID entity.UserID) ([]*entity.Ticket, error)

	// Admit decides one scan: through a usable link, from its bound device,
	// inside its event's reception window, it admits each ticket a genuine and
	// fresh AdmissionCode presents exactly once, records every refusal as a
	// RejectedScan, and returns the head count and reasons. A rejected code or
	// ticket is a result, not an error.
	//
	// # Possible errors
	//
	//  - PermissionDenied: no link holds the token (cause
	//    [ErrUnknownReceptionLinkToken]), the link is Revoked (also when revoked
	//    while the scan is decided), or the call is not proven by the bound
	//    device. Nothing is admitted.
	//  - FailedPrecondition: now is outside the link's reception window.
	//    Nothing is admitted or recorded.
	//  - Unavailable: admitting a ticket failed; scan again.
	//  - Internal: database failure before any ticket was considered.
	Admit(ctx context.Context, in AdmitInput) (*AdmitResult, error)
}

// ticketUseCase implements [TicketUseCase].
type ticketUseCase struct {
	orderRepo        entity.OrderRepository
	ticketRepo       entity.TicketRepository
	linkRepo         entity.ReceptionLinkRepository
	eventRepo        entity.EventRepository
	walletKeyRepo    entity.WalletPublicKeyRepository
	admissionRepo    entity.AdmissionRepository
	rejectedScanRepo entity.RejectedScanRepository
	logger           *logging.Logger
}

// Compile-time interface compliance check.
var _ TicketUseCase = (*ticketUseCase)(nil)

// NewTicketUseCase constructs a TicketUseCase. All parameters are required.
func NewTicketUseCase(
	orderRepo entity.OrderRepository,
	ticketRepo entity.TicketRepository,
	linkRepo entity.ReceptionLinkRepository,
	eventRepo entity.EventRepository,
	walletKeyRepo entity.WalletPublicKeyRepository,
	admissionRepo entity.AdmissionRepository,
	rejectedScanRepo entity.RejectedScanRepository,
	logger *logging.Logger,
) TicketUseCase {
	return &ticketUseCase{
		orderRepo:        orderRepo,
		ticketRepo:       ticketRepo,
		linkRepo:         linkRepo,
		eventRepo:        eventRepo,
		walletKeyRepo:    walletKeyRepo,
		admissionRepo:    admissionRepo,
		rejectedScanRepo: rejectedScanRepo,
		logger:           logger,
	}
}

// GetOrder implements [TicketUseCase].
func (uc *ticketUseCase) GetOrder(ctx context.Context, buyerID entity.UserID, orderID entity.OrderID) (*entity.Order, error) {
	order, err := uc.orderRepo.Get(ctx, orderID)
	if err != nil {
		return nil, err // propagates NotFound
	}
	// Ownership check: an Order belonging to another account is reported exactly
	// like a missing one (non-revealing), never disclosed.
	if order.BuyerID != buyerID {
		return nil, apperr.New(codes.NotFound, "order not found")
	}
	return order, nil
}

// GetMyTickets implements [TicketUseCase].
func (uc *ticketUseCase) GetMyTickets(ctx context.Context, holderID entity.UserID) ([]*entity.Ticket, error) {
	return uc.ticketRepo.ListByHolder(ctx, holderID)
}

// Admit implements [TicketUseCase].
func (uc *ticketUseCase) Admit(ctx context.Context, in AdmitInput) (*AdmitResult, error) {
	// -- the caller: a usable link, the bound device, inside the window --
	link, err := uc.linkRepo.GetByToken(ctx, in.LinkToken)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, receptionRefused(ErrUnknownReceptionLinkToken)
		}
		return nil, err
	}
	call := entity.ReceptionCall{
		Procedure: in.Procedure,
		Token:     in.LinkToken,
		Content:   in.ScannedText,
		SignTime:  in.SignTime,
		Signature: in.Signature,
	}
	if !link.IsUsable() || !link.Proves(call, in.Now) {
		return nil, receptionRefused(errReceptionLinkRefused)
	}
	event, err := uc.eventRepo.Get(ctx, link.EventID)
	if err != nil {
		return nil, err
	}
	window, ok := entity.ReceptionWindowOf(event)
	if !ok || !window.Contains(in.Now) {
		return nil, apperr.New(codes.FailedPrecondition, "outside the reception window")
	}

	// -- the code: genuine, fresh, for this event --
	code, reason, err := uc.checkCode(ctx, in.ScannedText, link.EventID, in.Now)
	if err != nil {
		return nil, err
	}
	if reason != entity.RejectedScanReasonUnspecified {
		uc.appendRejections(ctx, wholeScanRejections(link, code, reason, in.Now))
		return &AdmitResult{RejectedScanReason: reason}, nil
	}

	// -- each presented ticket, independently --
	held, err := uc.ticketRepo.ListByHolderAndEvent(ctx, code.UserID, link.EventID)
	if err != nil {
		return nil, err
	}
	heldIDs := make(map[entity.TicketID]struct{}, len(held))
	for _, t := range held {
		heldIDs[t.ID] = struct{}{}
	}

	result := &AdmitResult{}
	var rejections []*entity.RejectedScan
	reject := func(ticketID entity.TicketID, rt RejectedTicket) {
		result.RejectedTickets = append(result.RejectedTickets, rt)
		rejections = append(rejections, entity.NewRejectedScan(link.EventID, link.ID, ticketID, rt.Reason, in.Now))
	}
	for _, ticketID := range code.TicketIDs {
		if _, ok := heldIDs[ticketID]; !ok {
			reject(ticketID, RejectedTicket{Reason: entity.RejectedScanReasonNotHolder})
			continue
		}
		admitted, err := uc.ticketRepo.Admit(ctx, ticketID, link.ID, in.Now)
		if err != nil {
			// Refusals decided so far are still recorded.
			uc.appendRejections(ctx, rejections)
			if errors.Is(err, entity.ErrReceptionLinkNotUsable) {
				return nil, receptionRefused(errReceptionLinkRefused)
			}
			return nil, apperr.Wrap(err, codes.Unavailable, "failed to admit a ticket; scan again")
		}
		switch admitted.Outcome {
		case entity.AdmitOutcomeAdmitted:
			result.AdmittedTicketCount++
		case entity.AdmitOutcomeAlreadyAdmitted:
			reject(ticketID, uc.alreadyAdmitted(ctx, ticketID, admitted.AdmittedTime))
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
// fresh code for eventID.
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
	return code, entity.RejectedScanReasonUnspecified, nil
}

// wholeScanRejections returns the RejectedScans of a scan rejected as a
// whole: one without a ticket for Forged, one per presented ticket for
// Expired and OtherEvent (the signature verified, so the tickets are known).
func wholeScanRejections(link *entity.ReceptionLink, code *entity.AdmissionCode, reason entity.RejectedScanReason, now time.Time) []*entity.RejectedScan {
	if reason == entity.RejectedScanReasonForged {
		return []*entity.RejectedScan{entity.NewRejectedScan(link.EventID, link.ID, "", reason, now)}
	}
	scans := make([]*entity.RejectedScan, 0, len(code.TicketIDs))
	for _, id := range code.TicketIDs {
		scans = append(scans, entity.NewRejectedScan(link.EventID, link.ID, id, reason, now))
	}
	return scans
}

// alreadyAdmitted builds the AlreadyAdmitted rejection with the earlier
// admission's time and link number. When the Admission cannot be read the
// ticket's admitted time is still reported, without a link number.
func (uc *ticketUseCase) alreadyAdmitted(ctx context.Context, ticketID entity.TicketID, admittedTime time.Time) RejectedTicket {
	rt := RejectedTicket{Reason: entity.RejectedScanReasonAlreadyAdmitted}
	admission, err := uc.admissionRepo.GetByTicket(ctx, ticketID)
	if err != nil {
		uc.logger.Warn(ctx, "failed to read the earlier admission of an already admitted ticket",
			slog.String("ticket_id", string(ticketID)), slog.Any("error", err))
		t := admittedTime
		rt.EarlierAdmittedTime = &t
		return rt
	}
	t := admission.AdmittedTime
	rt.EarlierAdmittedTime = &t
	rt.EarlierReceptionLinkNumber = admission.ReceptionLinkNumber
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
