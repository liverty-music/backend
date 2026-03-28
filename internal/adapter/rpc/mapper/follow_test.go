package mapper_test

import (
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFollowedArtistToProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args *entity.FollowedArtist
		want *entityv1.FollowedArtist
	}{
		{
			name: "nil followed artist returns nil",
			args: nil,
			want: nil,
		},
		{
			name: "followed artist with hype watch",
			args: &entity.FollowedArtist{
				UserID: "user-id-1",
				Artist: &entity.Artist{
					ID:   "artist-id-1",
					Name: "Yorushika",
					MBID: "mbid-123",
				},
				Hype: entity.HypeWatch,
			},
			want: &entityv1.FollowedArtist{
				Artist: &entityv1.Artist{
					Id:   &entityv1.ArtistId{Value: "artist-id-1"},
					Name: &entityv1.ArtistName{Value: "Yorushika"},
					Mbid: &entityv1.Mbid{Value: "mbid-123"},
				},
				Hype: entityv1.HypeType_HYPE_TYPE_WATCH,
			},
		},
		{
			name: "followed artist with hype home",
			args: &entity.FollowedArtist{
				UserID: "user-id-2",
				Artist: &entity.Artist{
					ID:   "artist-id-2",
					Name: "YOASOBI",
					MBID: "mbid-456",
				},
				Hype: entity.HypeHome,
			},
			want: &entityv1.FollowedArtist{
				Artist: &entityv1.Artist{
					Id:   &entityv1.ArtistId{Value: "artist-id-2"},
					Name: &entityv1.ArtistName{Value: "YOASOBI"},
					Mbid: &entityv1.Mbid{Value: "mbid-456"},
				},
				Hype: entityv1.HypeType_HYPE_TYPE_HOME,
			},
		},
		{
			name: "followed artist with hype nearby",
			args: &entity.FollowedArtist{
				UserID: "user-id-3",
				Artist: &entity.Artist{
					ID:   "artist-id-3",
					Name: "Aimyon",
					MBID: "mbid-789",
				},
				Hype: entity.HypeNearby,
			},
			want: &entityv1.FollowedArtist{
				Artist: &entityv1.Artist{
					Id:   &entityv1.ArtistId{Value: "artist-id-3"},
					Name: &entityv1.ArtistName{Value: "Aimyon"},
					Mbid: &entityv1.Mbid{Value: "mbid-789"},
				},
				Hype: entityv1.HypeType_HYPE_TYPE_NEARBY,
			},
		},
		{
			name: "followed artist with hype away",
			args: &entity.FollowedArtist{
				UserID: "user-id-4",
				Artist: &entity.Artist{
					ID:   "artist-id-4",
					Name: "King Gnu",
					MBID: "mbid-000",
				},
				Hype: entity.HypeAway,
			},
			want: &entityv1.FollowedArtist{
				Artist: &entityv1.Artist{
					Id:   &entityv1.ArtistId{Value: "artist-id-4"},
					Name: &entityv1.ArtistName{Value: "King Gnu"},
					Mbid: &entityv1.Mbid{Value: "mbid-000"},
				},
				Hype: entityv1.HypeType_HYPE_TYPE_AWAY,
			},
		},
		{
			name: "followed artist with nil inner artist",
			args: &entity.FollowedArtist{
				UserID: "user-id-5",
				Artist: nil,
				Hype:   entity.HypeWatch,
			},
			want: &entityv1.FollowedArtist{
				Artist: nil,
				Hype:   entityv1.HypeType_HYPE_TYPE_WATCH,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := mapper.FollowedArtistToProto(tt.args)

			if tt.want == nil {
				assert.Nil(t, got)
				return
			}

			require.NotNil(t, got)
			assert.Equal(t, tt.want.String(), got.String())
		})
	}
}

func TestFollowedArtistsToProto(t *testing.T) {
	t.Parallel()

	followed := []*entity.FollowedArtist{
		{
			UserID: "user-1",
			Artist: &entity.Artist{ID: "artist-1", Name: "A", MBID: "m1"},
			Hype:   entity.HypeWatch,
		},
		{
			UserID: "user-1",
			Artist: &entity.Artist{ID: "artist-2", Name: "B", MBID: "m2"},
			Hype:   entity.HypeAway,
		},
	}

	got := mapper.FollowedArtistsToProto(followed)

	require.Len(t, got, 2)
	assert.Equal(t, "artist-1", got[0].GetArtist().GetId().GetValue())
	assert.Equal(t, entityv1.HypeType_HYPE_TYPE_WATCH, got[0].GetHype())
	assert.Equal(t, "artist-2", got[1].GetArtist().GetId().GetValue())
	assert.Equal(t, entityv1.HypeType_HYPE_TYPE_AWAY, got[1].GetHype())
}

func TestFollowedArtistsToProto_empty(t *testing.T) {
	t.Parallel()

	got := mapper.FollowedArtistsToProto([]*entity.FollowedArtist{})
	assert.Empty(t, got)
}

func TestHypeFromProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		proto entityv1.HypeType
		want  entity.Hype
	}{
		{
			name:  "watch proto maps to watch domain",
			proto: entityv1.HypeType_HYPE_TYPE_WATCH,
			want:  entity.HypeWatch,
		},
		{
			name:  "home proto maps to home domain",
			proto: entityv1.HypeType_HYPE_TYPE_HOME,
			want:  entity.HypeHome,
		},
		{
			name:  "nearby proto maps to nearby domain",
			proto: entityv1.HypeType_HYPE_TYPE_NEARBY,
			want:  entity.HypeNearby,
		},
		{
			name:  "away proto maps to away domain",
			proto: entityv1.HypeType_HYPE_TYPE_AWAY,
			want:  entity.HypeAway,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := mapper.HypeFromProto[tt.proto]
			assert.Equal(t, tt.want, got)
		})
	}
}
