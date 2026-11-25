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
