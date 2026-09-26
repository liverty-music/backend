package mapper_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/stretchr/testify/assert"
)

// TestDeviceTypeFromEndpoint moved from internal/entity/push_subscription_test.go
// as part of liverty-music/backend#485: push-vendor host-name matching is an
// adapter concern, not entity business logic.
func TestDeviceTypeFromEndpoint(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		endpoint string
		want     string
	}{
		{
			name:     "FCM endpoint classified as android",
			endpoint: "https://fcm.googleapis.com/fcm/send/abc123",
			want:     "android",
		},
		{
			name:     "Apple Web Push endpoint classified as apple",
			endpoint: "https://web.push.apple.com/sub/xyz789",
			want:     "apple",
		},
		{
			name:     "Mozilla autopush endpoint classified as firefox",
			endpoint: "https://updates.push.services.mozilla.com/wpush/v2/abcdef",
			want:     "firefox",
		},
		{
			name:     "Windows Notification Service endpoint classified as windows",
			endpoint: "https://db5p.notify.windows.com/?token=abc",
			want:     "windows",
		},
		{
			name:     "unknown host classifies as other",
			endpoint: "https://push.example.com/sub/123",
			want:     "other",
		},
		{
			name:     "empty endpoint classifies as other",
			endpoint: "",
			want:     "other",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, mapper.DeviceTypeFromEndpoint(tt.endpoint))
		})
	}
}
