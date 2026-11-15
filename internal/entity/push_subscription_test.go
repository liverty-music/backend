package entity_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/liverty-music/backend/internal/entity"
)

func TestPushSubscription_DeviceFamily(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		endpoint string
		want     entity.PushDeviceFamily
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
			assert.Equal(t, tt.want, (&entity.PushSubscription{Endpoint: tt.endpoint}).DeviceFamily())
		})
	}
}
