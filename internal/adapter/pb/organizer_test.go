package pb_test

import (
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestOrganizerToProto_Status(t *testing.T) {
	t.Parallel()

	type args struct {
		status entity.OrganizerStatus
	}
	tests := []struct {
		name string
		args args
		want entityv1.OrganizerStatus
	}{
		{
			name: "provisioning",
			args: args{status: entity.OrganizerStatusProvisioning},
			want: entityv1.OrganizerStatus_ORGANIZER_STATUS_PROVISIONING,
		},
		{
			name: "active",
			args: args{status: entity.OrganizerStatusActive},
			want: entityv1.OrganizerStatus_ORGANIZER_STATUS_ACTIVE,
		},
		{
			name: "deactivated",
			args: args{status: entity.OrganizerStatusDeactivated},
			want: entityv1.OrganizerStatus_ORGANIZER_STATUS_DEACTIVATED,
		},
		{
			name: "unspecified",
			args: args{status: entity.OrganizerStatusUnspecified},
			want: entityv1.OrganizerStatus_ORGANIZER_STATUS_UNSPECIFIED,
		},
		{
			name: "a value the proto does not define",
			args: args{status: entity.OrganizerStatus(99)},
			want: entityv1.OrganizerStatus_ORGANIZER_STATUS_UNSPECIFIED,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := pb.FromOrganizer(&entity.Organizer{ID: "org-1", Name: "Acme Music", Status: tt.args.status})

			assert.Equal(t, tt.want, got.GetStatus())
		})
	}
}

func TestPublicOrganizerToProto_LeavesStatusUnset(t *testing.T) {
	t.Parallel()

	got := pb.FromPublicOrganizer(&entity.Organizer{
		ID: "org-1", Name: "Acme Music", ShortName: "acme-music",
		Status: entity.OrganizerStatusActive, PlatformFeeRateBps: 800,
	})

	assert.Equal(t, entityv1.OrganizerStatus_ORGANIZER_STATUS_UNSPECIFIED, got.GetStatus())
	assert.Nil(t, got.GetShortName())
	assert.Zero(t, got.GetPlatformFeeRateBps())
}
