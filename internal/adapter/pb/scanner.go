package pb

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	scannerv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/scanner/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// FromWalletPublicKey converts a domain WalletPublicKey to wire format.
func FromWalletPublicKey(key *entity.WalletPublicKey) *entityv1.WalletPublicKey {
	if key == nil {
		return nil
	}
	return &entityv1.WalletPublicKey{
		UserId:       &entityv1.UserId{Value: string(key.UserID)},
		PublicKey:    &entityv1.PublicKey{Value: key.PublicKey},
		RegisterTime: timestamppb.New(key.RegisteredTime),
	}
}

// FromScanner converts a domain Scanner to wire format. The link token is
// mapped only when the domain Scanner carries one (the usecases set it only
// for an Unused Scanner returned to its owning Organizer).
func FromScanner(scanner *entity.Scanner) *entityv1.Scanner {
	if scanner == nil {
		return nil
	}
	out := &entityv1.Scanner{
		Id:      &entityv1.ScannerId{Value: string(scanner.ID)},
		EventId: &entityv1.EventId{Value: scanner.EventID},
		Number:  &entityv1.ScannerNumber{Value: int32(scanner.Number)},
		Status:  scannerStatusToProto(scanner.Status),
	}
	if scanner.LinkToken != "" {
		out.LinkToken = &entityv1.ScannerLinkToken{Value: scanner.LinkToken}
	}
	if scanner.BoundPublicKey != nil {
		out.BoundPublicKey = &entityv1.PublicKey{Value: scanner.BoundPublicKey}
	}
	if scanner.BoundTime != nil {
		out.BindTime = timestamppb.New(*scanner.BoundTime)
	}
	if scanner.RevokedTime != nil {
		out.RevokeTime = timestamppb.New(*scanner.RevokedTime)
	}
	return out
}

// FromScanners converts a slice of domain Scanners.
func FromScanners(scanners []*entity.Scanner) []*entityv1.Scanner {
	out := make([]*entityv1.Scanner, 0, len(scanners))
	for _, s := range scanners {
		out = append(out, FromScanner(s))
	}
	return out
}

// FromAdmissionWindow converts a domain AdmissionWindow; nil stays nil.
func FromAdmissionWindow(w *entity.AdmissionWindow) *entityv1.AdmissionWindow {
	if w == nil {
		return nil
	}
	return &entityv1.AdmissionWindow{
		OpenTime:  timestamppb.New(w.OpenTime),
		CloseTime: timestamppb.New(w.CloseTime),
	}
}

// FromAdmitResult converts the result of one scan to the AdmitResponse.
// Only a head count, reasons and the earlier admission time / Scanner number are
// mapped: the response has no field for any personal data.
func FromAdmitResult(r *usecase.AdmitResult) *scannerv1.AdmitResponse {
	out := &scannerv1.AdmitResponse{
		AdmittedTicketCount: int32(r.AdmittedTicketCount),
		RejectedScanReason:  FromRejectedScanReason(r.RejectedScanReason),
	}
	for _, rt := range r.RejectedTickets {
		t := &scannerv1.RejectedTicket{Reason: FromRejectedScanReason(rt.Reason)}
		if rt.Reason == entity.RejectedScanReasonAlreadyAdmitted {
			if rt.EarlierAdmitTime != nil {
				t.EarlierAdmitTime = timestamppb.New(*rt.EarlierAdmitTime)
			}
			if rt.EarlierScannerNumber > 0 {
				t.EarlierScannerNumber = &entityv1.ScannerNumber{Value: int32(rt.EarlierScannerNumber)}
			}
		}
		out.RejectedTickets = append(out.RejectedTickets, t)
	}
	return out
}

// FromRejectedScanReason maps a domain RejectedScanReason to the proto
// enum.
func FromRejectedScanReason(r entity.RejectedScanReason) entityv1.RejectedScanReason {
	switch r {
	case entity.RejectedScanReasonForged:
		return entityv1.RejectedScanReason_REJECTED_SCAN_REASON_FORGED
	case entity.RejectedScanReasonExpired:
		return entityv1.RejectedScanReason_REJECTED_SCAN_REASON_EXPIRED
	case entity.RejectedScanReasonOtherEvent:
		return entityv1.RejectedScanReason_REJECTED_SCAN_REASON_OTHER_EVENT
	case entity.RejectedScanReasonVoided:
		return entityv1.RejectedScanReason_REJECTED_SCAN_REASON_VOIDED
	case entity.RejectedScanReasonAlreadyAdmitted:
		return entityv1.RejectedScanReason_REJECTED_SCAN_REASON_ALREADY_ADMITTED
	default:
		return entityv1.RejectedScanReason_REJECTED_SCAN_REASON_UNSPECIFIED
	}
}

// scannerStatusToProto maps a domain ScannerStatus to the proto
// enum.
func scannerStatusToProto(s entity.ScannerStatus) entityv1.ScannerStatus {
	switch s {
	case entity.ScannerStatusUnused:
		return entityv1.ScannerStatus_SCANNER_STATUS_UNUSED
	case entity.ScannerStatusInUse:
		return entityv1.ScannerStatus_SCANNER_STATUS_IN_USE
	case entity.ScannerStatusRevoked:
		return entityv1.ScannerStatus_SCANNER_STATUS_REVOKED
	default:
		return entityv1.ScannerStatus_SCANNER_STATUS_UNSPECIFIED
	}
}
