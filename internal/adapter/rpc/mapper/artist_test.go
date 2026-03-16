package mapper_test

import (
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestArtistToProto(t *testing.T) {
	tests := []struct {
		name string
		args *entity.Artist
		want *entityv1.Artist
	}{
		{
			name: "nil artist returns nil",
			args: nil,
			want: nil,
		},
		{
			name: "artist without fanart",
			args: &entity.Artist{
				ID:   "artist-id",
				Name: "Yorushika",
				MBID: "mbid-123",
			},
			want: &entityv1.Artist{
				Id:   &entityv1.ArtistId{Value: "artist-id"},
				Name: &entityv1.ArtistName{Value: "Yorushika"},
				Mbid: &entityv1.Mbid{Value: "mbid-123"},
			},
		},
		{
			name: "artist with fanart selects best by likes",
			args: &entity.Artist{
				ID:   "artist-id",
				Name: "Yorushika",
				MBID: "mbid-123",
				Fanart: &entity.Fanart{
					ArtistThumb: []entity.FanartImage{
						{ID: "1", URL: "https://fanart.tv/thumb-low.jpg", Likes: 2},
						{ID: "2", URL: "https://fanart.tv/thumb-best.jpg", Likes: 10},
					},
					HDMusicLogo: []entity.FanartImage{
						{ID: "3", URL: "https://fanart.tv/logo.png", Likes: 5},
					},
					MusicBanner: []entity.FanartImage{},
				},
			},
			want: &entityv1.Artist{
				Id:   &entityv1.ArtistId{Value: "artist-id"},
				Name: &entityv1.ArtistName{Value: "Yorushika"},
				Mbid: &entityv1.Mbid{Value: "mbid-123"},
				Fanart: &entityv1.Fanart{
					ArtistThumb: &entityv1.Url{Value: "https://fanart.tv/thumb-best.jpg"},
					HdMusicLogo: &entityv1.Url{Value: "https://fanart.tv/logo.png"},
				},
			},
		},
		{
			name: "artist with all fanart types populated",
			args: &entity.Artist{
				ID:   "artist-id",
				Name: "YOASOBI",
				MBID: "mbid-456",
				Fanart: &entity.Fanart{
					ArtistThumb:      []entity.FanartImage{{ID: "1", URL: "https://fanart.tv/thumb.jpg", Likes: 3}},
					ArtistBackground: []entity.FanartImage{{ID: "2", URL: "https://fanart.tv/bg.jpg", Likes: 7}},
					HDMusicLogo:      []entity.FanartImage{{ID: "3", URL: "https://fanart.tv/hdlogo.png", Likes: 12}},
					MusicLogo:        []entity.FanartImage{{ID: "4", URL: "https://fanart.tv/logo.png", Likes: 4}},
					MusicBanner:      []entity.FanartImage{{ID: "5", URL: "https://fanart.tv/banner.jpg", Likes: 1}},
				},
			},
			want: &entityv1.Artist{
				Id:   &entityv1.ArtistId{Value: "artist-id"},
				Name: &entityv1.ArtistName{Value: "YOASOBI"},
				Mbid: &entityv1.Mbid{Value: "mbid-456"},
				Fanart: &entityv1.Fanart{
					ArtistThumb:      &entityv1.Url{Value: "https://fanart.tv/thumb.jpg"},
					ArtistBackground: &entityv1.Url{Value: "https://fanart.tv/bg.jpg"},
					HdMusicLogo:      &entityv1.Url{Value: "https://fanart.tv/hdlogo.png"},
					MusicLogo:        &entityv1.Url{Value: "https://fanart.tv/logo.png"},
					MusicBanner:      &entityv1.Url{Value: "https://fanart.tv/banner.jpg"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapper.ArtistToProto(tt.args)
			assert.Equal(t, tt.want.String(), got.String())
		})
	}
}

func TestArtistsToProto(t *testing.T) {
	artists := []*entity.Artist{
		{ID: "1", Name: "A", MBID: "m1"},
		{ID: "2", Name: "B", MBID: "m2"},
	}

	got := mapper.ArtistsToProto(artists)
	assert.Len(t, got, 2)
	assert.Equal(t, "1", got[0].GetId().GetValue())
	assert.Equal(t, "2", got[1].GetId().GetValue())
}
