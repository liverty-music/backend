package entity_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/liverty-music/backend/internal/entity"
)

func TestPrefectureName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		code   string
		want   string
		wantOK bool
	}{
		{name: "prefecture code", code: "JP-13", want: "東京都", wantOK: true},
		{name: "lower-case code", code: "jp-01", want: "北海道", wantOK: true},
		{name: "overseas code", code: "US-CA", wantOK: false},
		{name: "empty", code: "", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := entity.PrefectureName(tt.code)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPrefectureCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		in     string
		want   string
		wantOK bool
	}{
		{name: "japanese name with suffix", in: "東京都", want: "JP-13", wantOK: true},
		{name: "japanese name without suffix", in: "大阪", want: "JP-27", wantOK: true},
		{name: "english name, any case", in: "Hokkaido", want: "JP-01", wantOK: true},
		{name: "unknown name", in: "台北市", wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := entity.PrefectureCode(tt.in)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}
