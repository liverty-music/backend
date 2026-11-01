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
