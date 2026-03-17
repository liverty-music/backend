package entity_test

import (
	"image"
	"image/color"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestAnalyzeLogo(t *testing.T) {
	tests := []struct {
		name          string
		img           image.Image
		wantNil       bool
		wantChromatic bool
		wantHueMin    float64
		wantHueMax    float64
		wantLightMin  float64
		wantLightMax  float64
	}{
		{
			name:          "chromatic image with >30% colored pixels returns hue",
			img:           chromaticImage(),
			wantChromatic: true,
			wantHueMin:    20.0,
			wantHueMax:    35.0,
			wantLightMin:  0.3,
			wantLightMax:  0.8,
		},
		{
			name:          "achromatic light image has high lightness and no hue",
			img:           achromaticLightImage(),
			wantChromatic: false,
			wantLightMin:  0.6,
			wantLightMax:  1.0,
		},
		{
			name:          "achromatic dark image has low lightness and no hue",
			img:           achromaticDarkImage(),
			wantChromatic: false,
			wantLightMin:  0.0,
			wantLightMax:  0.5,
		},
		{
			name:    "fully transparent image returns nil",
			img:     fullyTransparentImage(),
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := entity.AnalyzeLogo(tt.img)

			if tt.wantNil {
				assert.Nil(t, got)
				return
			}

			assert.NotNil(t, got)
			assert.Equal(t, tt.wantChromatic, got.IsChromatic)
			assert.GreaterOrEqual(t, got.DominantLightness, tt.wantLightMin, "lightness lower bound")
			assert.LessOrEqual(t, got.DominantLightness, tt.wantLightMax, "lightness upper bound")

			if tt.wantChromatic {
				assert.NotNil(t, got.DominantHue, "chromatic logo must have hue")
				assert.GreaterOrEqual(t, *got.DominantHue, tt.wantHueMin, "hue lower bound")
				assert.LessOrEqual(t, *got.DominantHue, tt.wantHueMax, "hue upper bound")
			} else {
				assert.Nil(t, got.DominantHue, "achromatic logo must not have hue")
			}
		})
	}
}

func TestBestLogoURL(t *testing.T) {
	tests := []struct {
		name string
		f    *entity.Fanart
		want string
	}{
		{
			name: "prefers HDMusicLogo over MusicLogo",
			f: &entity.Fanart{
				HDMusicLogo: []entity.FanartImage{{URL: "hd.png", Likes: 5}},
				MusicLogo:   []entity.FanartImage{{URL: "sd.png", Likes: 10}},
			},
			want: "hd.png",
		},
		{
			name: "falls back to MusicLogo when no HDMusicLogo",
			f: &entity.Fanart{
				MusicLogo: []entity.FanartImage{{URL: "sd.png", Likes: 3}},
			},
			want: "sd.png",
		},
		{
			name: "returns empty when neither exists",
			f:    &entity.Fanart{},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := entity.BestLogoURL(tt.f)
			assert.Equal(t, tt.want, got)
		})
	}
}

// chromaticImage creates a 10x10 image where all pixels are red (chromatic).
func chromaticImage() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}
	return img
}

// achromaticLightImage creates a 10x10 image where all pixels are white.
func achromaticLightImage() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 240, G: 240, B: 240, A: 255})
		}
	}
	return img
}

// achromaticDarkImage creates a 10x10 image where all pixels are dark gray.
func achromaticDarkImage() *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, 10, 10))
	for y := 0; y < 10; y++ {
		for x := 0; x < 10; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: 30, G: 30, B: 30, A: 255})
		}
	}
	return img
}

// fullyTransparentImage creates a 10x10 image where all pixels have alpha 0.
func fullyTransparentImage() *image.NRGBA {
	return image.NewNRGBA(image.Rect(0, 0, 10, 10))
}
