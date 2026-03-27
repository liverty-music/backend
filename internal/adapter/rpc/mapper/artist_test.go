package mapper_test

import (
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestArtistToProto(t *testing.T) {
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
		{
			name: "fanart with chromatic logo color profile",
			args: &entity.Artist{
				ID:   "artist-id",
				Name: "Suchmos",
				MBID: "mbid-789",
				Fanart: &entity.Fanart{
					HDMusicLogo: []entity.FanartImage{{ID: "1", URL: "https://fanart.tv/logo.png", Likes: 5}},
					LogoColorProfile: &entity.LogoColorProfile{
						DominantHue:       new(210.0),
						DominantLightness: 0.45,
						IsChromatic:       true,
					},
				},
			},
			want: func() *entityv1.Artist {
				p := &entityv1.Artist{
					Id:   &entityv1.ArtistId{Value: "artist-id"},
					Name: &entityv1.ArtistName{Value: "Suchmos"},
					Mbid: &entityv1.Mbid{Value: "mbid-789"},
					Fanart: &entityv1.Fanart{
						HdMusicLogo: &entityv1.Url{Value: "https://fanart.tv/logo.png"},
						LogoColorProfile: &entityv1.LogoColorProfile{
							DominantLightness: 0.45,
							IsChromatic:       true,
						},
					},
				}
				p.GetFanart().GetLogoColorProfile().SetDominantHue(210.0)
				return p
			}(),
		},
		{
			name: "fanart with achromatic logo color profile has no hue",
			args: &entity.Artist{
				ID:   "artist-id",
				Name: "SPYAIR",
				MBID: "mbid-000",
				Fanart: &entity.Fanart{
					HDMusicLogo: []entity.FanartImage{{ID: "1", URL: "https://fanart.tv/logo.png", Likes: 3}},
					LogoColorProfile: &entity.LogoColorProfile{
						DominantHue:       nil,
						DominantLightness: 0.15,
						IsChromatic:       false,
					},
				},
			},
			want: &entityv1.Artist{
				Id:   &entityv1.ArtistId{Value: "artist-id"},
				Name: &entityv1.ArtistName{Value: "SPYAIR"},
				Mbid: &entityv1.Mbid{Value: "mbid-000"},
				Fanart: &entityv1.Fanart{
					HdMusicLogo: &entityv1.Url{Value: "https://fanart.tv/logo.png"},
					LogoColorProfile: &entityv1.LogoColorProfile{
						DominantLightness: 0.15,
						IsChromatic:       false,
					},
				},
			},
		},
		{
			name: "fanart without logo color profile omits field",
			args: &entity.Artist{
				ID:   "artist-id",
				Name: "UVERworld",
				MBID: "mbid-111",
				Fanart: &entity.Fanart{
					HDMusicLogo: []entity.FanartImage{{ID: "1", URL: "https://fanart.tv/logo.png", Likes: 1}},
				},
			},
			want: &entityv1.Artist{
				Id:   &entityv1.ArtistId{Value: "artist-id"},
				Name: &entityv1.ArtistName{Value: "UVERworld"},
				Mbid: &entityv1.Mbid{Value: "mbid-111"},
				Fanart: &entityv1.Fanart{
					HdMusicLogo: &entityv1.Url{Value: "https://fanart.tv/logo.png"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := mapper.ArtistToProto(tt.args)
			assert.Equal(t, tt.want.String(), got.String())
		})
	}
}

func TestArtistsToProto(t *testing.T) {
	t.Parallel()

	artists := []*entity.Artist{
		{ID: "1", Name: "A", MBID: "m1"},
		{ID: "2", Name: "B", MBID: "m2"},
	}

	got := mapper.ArtistsToProto(artists)
	assert.Len(t, got, 2)
	assert.Equal(t, "1", got[0].GetId().GetValue())
	assert.Equal(t, "2", got[1].GetId().GetValue())
}
