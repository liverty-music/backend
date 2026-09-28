package mapper

import "strings"

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
// Called by PushNotificationHandler at subscription registration/removal
// time; the resulting device type is passed into PushNotificationUseCase so
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
