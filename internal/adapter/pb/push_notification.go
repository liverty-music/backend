package pb

import (
	"strings"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"

	"github.com/liverty-music/backend/internal/entity"
)

// DeviceTypeFromEndpoint classifies a Web Push endpoint URL into a coarse
// device-family label suitable for the notification.subscribed /
// notification.unsubscribed analytics events. The endpoint host identifies
// the push service vendor, which in turn identifies the browser/OS family.
// The endpoint itself is sensitive (it uniquely identifies the user's browser
// session) and MUST NOT leak into analytics — only the classifier output is
// safe to forward.
//
// Unknown hosts return "other" so the analytics breakdown stays well-defined
// without exposing arbitrary endpoint text downstream.
//
// Called by PushSubscriptionHandler at subscription registration/removal
// time; the resulting device type is passed into PushSubscriptionUseCase so
// the use case layer never needs to know about push-vendor host names (see
// liverty-music/backend#485).
func DeviceTypeFromEndpoint(endpoint string) string {
	switch {
	case strings.Contains(endpoint, "fcm.googleapis.com"):
		return "android"
	case strings.Contains(endpoint, "web.push.apple.com"):
		return "apple"
	case strings.Contains(endpoint, "updates.push.services.mozilla.com"):
		return "firefox"
	case strings.Contains(endpoint, ".notify.windows.com"):
		return "windows"
	default:
		return "other"
	}
}

// deviceFamilyToProto maps a [DeviceTypeFromEndpoint] label to Protobuf.
var deviceFamilyToProto = map[string]entityv1.PushDeviceFamily{
	"android": entityv1.PushDeviceFamily_PUSH_DEVICE_FAMILY_ANDROID,
	"apple":   entityv1.PushDeviceFamily_PUSH_DEVICE_FAMILY_APPLE,
	"firefox": entityv1.PushDeviceFamily_PUSH_DEVICE_FAMILY_FIREFOX,
	"windows": entityv1.PushDeviceFamily_PUSH_DEVICE_FAMILY_WINDOWS,
	"other":   entityv1.PushDeviceFamily_PUSH_DEVICE_FAMILY_OTHER,
}

// FromPushSubscription maps a PushSubscription to Protobuf: its id, User,
// push address and keys, and the device family derived from the address.
func FromPushSubscription(s *entity.PushSubscription) *entityv1.PushSubscription {
	if s == nil {
		return nil
	}
	return &entityv1.PushSubscription{
		Id:           &entityv1.PushSubscriptionId{Value: s.ID},
		UserId:       &entityv1.UserId{Value: s.UserID},
		Endpoint:     &entityv1.PushEndpoint{Value: s.Endpoint},
		Keys:         &entityv1.PushKeys{P256Dh: s.P256dh, Auth: s.Auth},
		DeviceFamily: deviceFamilyToProto[DeviceTypeFromEndpoint(s.Endpoint)],
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
