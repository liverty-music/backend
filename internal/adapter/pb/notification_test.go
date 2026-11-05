package pb_test

import (
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/stretchr/testify/assert"

	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
)

// TestFromNotification_ResaleEntryTypes checks the types of the resale entry
// lapse and loss pushes map to their own proto values, never UNSPECIFIED,
// which the required, defined-only type field rejects.
func TestFromNotification_ResaleEntryTypes(t *testing.T) {
	t.Parallel()
	for typ, want := range map[entity.NotificationType]entityv1.NotificationType{
		entity.NotificationTypeResaleEntryLapsed: entityv1.NotificationType_NOTIFICATION_TYPE_RESALE_ENTRY_LAPSED,
		entity.NotificationTypeResaleEntryLost:   entityv1.NotificationType_NOTIFICATION_TYPE_RESALE_ENTRY_LOST,
	} {
		got := pb.FromNotification(&entity.Notification{ID: "n-1", UserID: "u-1", Type: typ})
		assert.Equal(t, want, got.GetType(), string(typ))
	}
}
