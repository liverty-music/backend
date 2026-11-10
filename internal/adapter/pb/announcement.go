package pb

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"

	"github.com/liverty-music/backend/internal/entity"
)

// DeviceFamilyFromProto returns the device family name ("android", "apple",
// "firefox", "windows" or "other") of a PushDeviceFamily; "other" for an
// unspecified one.
func DeviceFamilyFromProto(f entityv1.PushDeviceFamily) string {
	for name, v := range deviceFamilyToProto {
		if v == f {
			return name
		}
	}
	return "other"
}

// NotificationTypeFromProto returns the NotificationType of its wire form,
// or "" for an unspecified one.
func NotificationTypeFromProto(t entityv1.NotificationType) entity.NotificationType {
	for typ, v := range notificationTypeToProto {
		if v == t {
			return typ
		}
	}
	return ""
}

// LotteryEntryStateFromProto returns the LotteryEntryState of its wire form.
func LotteryEntryStateFromProto(s entityv1.LotteryEntryState) entity.LotteryEntryState {
	for _, state := range []entity.LotteryEntryState{entity.LotteryEntryStateEntered, entity.LotteryEntryStateWon, entity.LotteryEntryStateLost} {
		if lotteryEntryStateToProto(state) == s {
			return state
		}
	}
	return entity.LotteryEntryStateUnspecified
}

// LotteryEntryFromAnnouncement maps the LotteryEntry a LOTTERY_ENTRY.won or
// LOTTERY_ENTRY.lost announcement carries back to the domain entry, without
// its Order.
func LotteryEntryFromAnnouncement(e *entityv1.LotteryEntry) *entity.LotteryEntry {
	entry := &entity.LotteryEntry{
		ID:           entity.LotteryEntryID(e.GetId().GetValue()),
		OrderID:      entity.OrderID(e.GetOrderId().GetValue()),
		TicketSaleID: entity.TicketSaleID(e.GetTicketSaleId().GetValue()),
		State:        LotteryEntryStateFromProto(e.GetState()),
	}
	if e.DrawPosition != nil {
		position := int(e.GetDrawPosition())
		entry.DrawPosition = &position
	}
	for _, reason := range []entity.LotteryEntryLostReason{entity.LotteryEntryLostReasonNotDrawn, entity.LotteryEntryLostReasonCardNotCharged, entity.LotteryEntryLostReasonConcertCancelled} {
		if lotteryEntryLostReasonToProto(reason) == e.GetLostReason() {
			entry.LostReason = reason
		}
	}
	return entry
}

// CheckoutFromAnnouncement maps the Checkout a CHECKOUT.held,
// CHECKOUT.expired or CHECKOUT.released announcement carries back
// to the domain Checkout, without its Order.
func CheckoutFromAnnouncement(c *entityv1.Checkout) *entity.Checkout {
	checkout := &entity.Checkout{
		ID:             entity.CheckoutID(c.GetId().GetValue()),
		OrderID:        entity.OrderID(c.GetOrderId().GetValue()),
		TicketSaleID:   entity.TicketSaleID(c.GetTicketSaleId().GetValue()),
		Status:         checkoutStatusFromProto(c.GetStatus()),
		HoldExpireTime: c.GetHoldExpireTime().AsTime(),
	}
	if c.CommitTime != nil {
		t := c.GetCommitTime().AsTime()
		checkout.CommitTime = &t
	}
	if c.CaptureTime != nil {
		t := c.GetCaptureTime().AsTime()
		checkout.CaptureTime = &t
	}
	return checkout
}

// checkoutStatusFromProto returns the CheckoutStatus of its wire form.
func checkoutStatusFromProto(s entityv1.CheckoutStatus) entity.CheckoutStatus {
	for _, status := range []entity.CheckoutStatus{
		entity.CheckoutStatusHeld, entity.CheckoutStatusCommitted, entity.CheckoutStatusCompleted,
		entity.CheckoutStatusExpired, entity.CheckoutStatusReleased,
	} {
		if checkoutStatusToProto(status) == s {
			return status
		}
	}
	return entity.CheckoutStatusUnspecified
}
