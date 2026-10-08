package mapper

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	receptionv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/reception/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// WalletPublicKeyToProto converts a domain WalletPublicKey to wire format.
func WalletPublicKeyToProto(key *entity.WalletPublicKey) *entityv1.WalletPublicKey {
	if key == nil {
		return nil
	}
	return &entityv1.WalletPublicKey{
		UserId:       &entityv1.UserId{Value: string(key.UserID)},
		PublicKey:    &entityv1.PublicKey{Value: key.PublicKey},
		RegisterTime: timestamppb.New(key.RegisteredTime),
	}
}

// ReceptionLinkToProto converts a domain ReceptionLink to wire format. The
// token is mapped only when the domain link carries one (the usecases set it
// only for an Unused link returned to its owning Organizer).
func ReceptionLinkToProto(link *entity.ReceptionLink) *entityv1.ReceptionLink {
	if link == nil {
		return nil
	}
	out := &entityv1.ReceptionLink{
		Id:      &entityv1.ReceptionLinkId{Value: string(link.ID)},
		EventId: &entityv1.EventId{Value: link.EventID},
		Number:  &entityv1.ReceptionLinkNumber{Value: int32(link.Number)},
		Status:  receptionLinkStatusToProto(link.Status),
	}
	if link.Token != "" {
		out.Token = &entityv1.ReceptionLinkToken{Value: link.Token}
	}
	if link.BoundPublicKey != nil {
		out.BoundPublicKey = &entityv1.PublicKey{Value: link.BoundPublicKey}
	}
	if link.BoundTime != nil {
		out.BindTime = timestamppb.New(*link.BoundTime)
	}
	if link.RevokedTime != nil {
		out.RevokeTime = timestamppb.New(*link.RevokedTime)
	}
	return out
}

// ReceptionLinksToProto converts a slice of domain ReceptionLinks.
func ReceptionLinksToProto(links []*entity.ReceptionLink) []*entityv1.ReceptionLink {
	out := make([]*entityv1.ReceptionLink, 0, len(links))
	for _, l := range links {
		out = append(out, ReceptionLinkToProto(l))
	}
	return out
}

// ReceptionWindowToProto converts a domain ReceptionWindow; nil stays nil.
func ReceptionWindowToProto(w *entity.ReceptionWindow) *entityv1.ReceptionWindow {
	if w == nil {
		return nil
	}
	return &entityv1.ReceptionWindow{
		OpenTime:  timestamppb.New(w.OpenTime),
		CloseTime: timestamppb.New(w.CloseTime),
	}
}

// AdmitResultToProto converts the result of one scan to the AdmitResponse.
// Only a head count, reasons and the earlier admission time / link number are
// mapped: the response has no field for any personal data.
func AdmitResultToProto(r *usecase.AdmitResult) *receptionv1.AdmitResponse {
	out := &receptionv1.AdmitResponse{
		AdmittedTicketCount: int32(r.AdmittedTicketCount),
		RejectedScanReason:  RejectedScanReasonToProto(r.RejectedScanReason),
	}
	for _, rt := range r.RejectedTickets {
		t := &receptionv1.RejectedTicket{Reason: RejectedScanReasonToProto(rt.Reason)}
		if rt.Reason == entity.RejectedScanReasonAlreadyAdmitted {
			if rt.EarlierAdmittedTime != nil {
				t.EarlierAdmitTime = timestamppb.New(*rt.EarlierAdmittedTime)
			}
			if rt.EarlierReceptionLinkNumber > 0 {
				t.EarlierReceptionLinkNumber = &entityv1.ReceptionLinkNumber{Value: int32(rt.EarlierReceptionLinkNumber)}
			}
		}
		out.RejectedTickets = append(out.RejectedTickets, t)
	}
	return out
}

// RejectedScanReasonToProto maps a domain RejectedScanReason to the proto
// enum.
func RejectedScanReasonToProto(r entity.RejectedScanReason) entityv1.RejectedScanReason {
	switch r {
	case entity.RejectedScanReasonForged:
		return entityv1.RejectedScanReason_REJECTED_SCAN_REASON_FORGED
	case entity.RejectedScanReasonExpired:
		return entityv1.RejectedScanReason_REJECTED_SCAN_REASON_EXPIRED
	case entity.RejectedScanReasonOtherEvent:
		return entityv1.RejectedScanReason_REJECTED_SCAN_REASON_OTHER_EVENT
	case entity.RejectedScanReasonNotHolder:
		return entityv1.RejectedScanReason_REJECTED_SCAN_REASON_NOT_HOLDER
	case entity.RejectedScanReasonVoided:
		return entityv1.RejectedScanReason_REJECTED_SCAN_REASON_VOIDED
	case entity.RejectedScanReasonAlreadyAdmitted:
		return entityv1.RejectedScanReason_REJECTED_SCAN_REASON_ALREADY_ADMITTED
	default:
		return entityv1.RejectedScanReason_REJECTED_SCAN_REASON_UNSPECIFIED
	}
}

// receptionLinkStatusToProto maps a domain ReceptionLinkStatus to the proto
// enum.
func receptionLinkStatusToProto(s entity.ReceptionLinkStatus) entityv1.ReceptionLinkStatus {
	switch s {
	case entity.ReceptionLinkStatusUnused:
		return entityv1.ReceptionLinkStatus_RECEPTION_LINK_STATUS_UNUSED
	case entity.ReceptionLinkStatusInUse:
		return entityv1.ReceptionLinkStatus_RECEPTION_LINK_STATUS_IN_USE
	case entity.ReceptionLinkStatusRevoked:
		return entityv1.ReceptionLinkStatus_RECEPTION_LINK_STATUS_REVOKED
	default:
		return entityv1.ReceptionLinkStatus_RECEPTION_LINK_STATUS_UNSPECIFIED
	}
}
