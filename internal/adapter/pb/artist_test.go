package pb_test

import (
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestFromArtist(t *testing.T) {
	t.Parallel()

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
					ArtistBackground: []entity.FanartImage{
						{ID: "3", URL: "https://fanart.tv/bg.jpg", Likes: 5},
					},
					MusicBanner: []entity.FanartImage{},
				},
			},
			want: &entityv1.Artist{
				Id:   &entityv1.ArtistId{Value: "artist-id"},
				Name: &entityv1.ArtistName{Value: "Yorushika"},
				Mbid: &entityv1.Mbid{Value: "mbid-123"},
				Fanart: &entityv1.Fanart{
					ArtistThumb:      &entityv1.Url{Value: "https://fanart.tv/thumb-best.jpg"},
					ArtistBackground: &entityv1.Url{Value: "https://fanart.tv/bg.jpg"},
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
					MusicBanner:      &entityv1.Url{Value: "https://fanart.tv/banner.jpg"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := pb.FromArtist(tt.args)
			assert.Equal(t, tt.want.String(), got.String())
		})
	}
}

func TestFromArtists(t *testing.T) {
	t.Parallel()

	artists := []*entity.Artist{
		{ID: "1", Name: "A", MBID: "m1"},
		{ID: "2", Name: "B", MBID: "m2"},
	}

	got := pb.FromArtists(artists)
	assert.Len(t, got, 2)
	assert.Equal(t, "1", got[0].GetId().GetValue())
	assert.Equal(t, "2", got[1].GetId().GetValue())
}
