package entity_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestSRGBToOKLCH(t *testing.T) {
	type args struct {
		r, g, b uint8
	}
	tests := []struct {
		name     string
		args     args
		wantL    float64
		wantCMin float64
		wantCMax float64
		wantHMin float64
		wantHMax float64
		tolerL   float64
	}{
		{
			name:     "pure white has lightness ~1.0 and near-zero chroma",
			args:     args{r: 255, g: 255, b: 255},
			wantL:    1.0,
			wantCMin: 0.0,
			wantCMax: 0.001,
			tolerL:   0.01,
		},
		{
			name:     "pure black has lightness ~0.0 and near-zero chroma",
			args:     args{r: 0, g: 0, b: 0},
			wantL:    0.0,
			wantCMin: 0.0,
			wantCMax: 0.001,
			tolerL:   0.01,
		},
		{
			name:     "pure red has lightness ~0.63, chroma >0.2, hue ~29 degrees",
			args:     args{r: 255, g: 0, b: 0},
			wantL:    0.63,
			wantCMin: 0.2,
			wantCMax: 0.4,
			wantHMin: 20.0,
			wantHMax: 35.0,
			tolerL:   0.02,
		},
		{
			name:     "pure green has high chroma and hue ~142 degrees",
			args:     args{r: 0, g: 255, b: 0},
			wantL:    0.87,
			wantCMin: 0.2,
			wantCMax: 0.4,
			wantHMin: 135.0,
			wantHMax: 150.0,
			tolerL:   0.02,
		},
		{
			name:     "pure blue has hue ~264 degrees",
			args:     args{r: 0, g: 0, b: 255},
			wantL:    0.45,
			wantCMin: 0.2,
			wantCMax: 0.4,
			wantHMin: 255.0,
			wantHMax: 275.0,
			tolerL:   0.02,
		},
		{
			name:     "mid gray has near-zero chroma",
			args:     args{r: 128, g: 128, b: 128},
			wantL:    0.6,
			wantCMin: 0.0,
			wantCMax: 0.001,
			tolerL:   0.05,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := entity.SRGBToOKLCH(tt.args.r, tt.args.g, tt.args.b)

			assert.InDelta(t, tt.wantL, got.L, tt.tolerL, "lightness")
			assert.GreaterOrEqual(t, got.C, tt.wantCMin, "chroma lower bound")
			assert.LessOrEqual(t, got.C, tt.wantCMax, "chroma upper bound")

			if tt.wantHMax > 0 {
				assert.GreaterOrEqual(t, got.H, tt.wantHMin, "hue lower bound")
				assert.LessOrEqual(t, got.H, tt.wantHMax, "hue upper bound")
			}
		})
	}
}
