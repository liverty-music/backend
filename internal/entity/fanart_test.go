package entity_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestBestByLikes(t *testing.T) {
	t.Parallel()

	type args struct {
		images []entity.FanartImage
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			name: "returns empty string for nil slice",
			args: args{images: nil},
			want: "",
		},
		{
			name: "returns empty string for empty slice",
			args: args{images: []entity.FanartImage{}},
			want: "",
		},
		{
			name: "returns URL of the only image",
			args: args{
				images: []entity.FanartImage{
					{ID: "1", URL: "https://assets.fanart.tv/img1.jpg", Likes: 5, Lang: "en"},
				},
			},
			want: "https://assets.fanart.tv/img1.jpg",
		},
		{
			name: "returns URL with highest likes",
			args: args{
				images: []entity.FanartImage{
					{ID: "1", URL: "https://assets.fanart.tv/low.jpg", Likes: 2, Lang: "en"},
					{ID: "2", URL: "https://assets.fanart.tv/high.jpg", Likes: 10, Lang: "ja"},
					{ID: "3", URL: "https://assets.fanart.tv/mid.jpg", Likes: 5, Lang: "en"},
				},
			},
			want: "https://assets.fanart.tv/high.jpg",
		},
		{
			name: "returns first image when all likes are equal",
			args: args{
				images: []entity.FanartImage{
					{ID: "1", URL: "https://assets.fanart.tv/first.jpg", Likes: 3, Lang: "en"},
					{ID: "2", URL: "https://assets.fanart.tv/second.jpg", Likes: 3, Lang: "ja"},
				},
			},
			want: "https://assets.fanart.tv/first.jpg",
		},
		{
			name: "handles zero likes correctly",
			args: args{
				images: []entity.FanartImage{
					{ID: "1", URL: "https://assets.fanart.tv/zero.jpg", Likes: 0, Lang: "en"},
					{ID: "2", URL: "https://assets.fanart.tv/one.jpg", Likes: 1, Lang: "en"},
				},
			},
			want: "https://assets.fanart.tv/one.jpg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := entity.BestByLikes(tt.args.images)
			assert.Equal(t, tt.want, got)
		})
	}
}
