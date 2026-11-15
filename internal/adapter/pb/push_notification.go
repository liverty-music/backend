package pb

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"

	"github.com/liverty-music/backend/internal/entity"
)

// deviceFamilyToProto maps a PushDeviceFamily to Protobuf.
var deviceFamilyToProto = map[entity.PushDeviceFamily]entityv1.PushDeviceFamily{
	entity.PushDeviceFamilyAndroid: entityv1.PushDeviceFamily_PUSH_DEVICE_FAMILY_ANDROID,
	entity.PushDeviceFamilyApple:   entityv1.PushDeviceFamily_PUSH_DEVICE_FAMILY_APPLE,
	entity.PushDeviceFamilyFirefox: entityv1.PushDeviceFamily_PUSH_DEVICE_FAMILY_FIREFOX,
	entity.PushDeviceFamilyWindows: entityv1.PushDeviceFamily_PUSH_DEVICE_FAMILY_WINDOWS,
	entity.PushDeviceFamilyOther:   entityv1.PushDeviceFamily_PUSH_DEVICE_FAMILY_OTHER,
}

// FromPushSubscription maps a PushSubscription to Protobuf: its id, User,
// push address and keys, and its device family (PushSubscription.DeviceFamily).
func FromPushSubscription(s *entity.PushSubscription) *entityv1.PushSubscription {
	if s == nil {
		return nil
	}
	return &entityv1.PushSubscription{
		Id:           &entityv1.PushSubscriptionId{Value: s.ID},
		UserId:       &entityv1.UserId{Value: s.UserID},
		Endpoint:     &entityv1.PushEndpoint{Value: s.Endpoint},
		Keys:         &entityv1.PushKeys{P256Dh: s.P256dh, Auth: s.Auth},
		DeviceFamily: deviceFamilyToProto[s.DeviceFamily()],
	}
}

// FromPushSubscriptionForAnnouncement maps a PushSubscription for an
// announcement payload: as [FromPushSubscription], without the push address
// and its keys, which would let a reader send to the browser.
func FromPushSubscriptionForAnnouncement(s *entity.PushSubscription) *entityv1.PushSubscription {
	pb := FromPushSubscription(s)
	if pb == nil {
		return nil
	}
	pb.Endpoint = nil
	pb.Keys = nil
	return pb
}
