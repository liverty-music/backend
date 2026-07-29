package google

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCountryCodeToLanguage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cc   string
		want string
	}{
		{"japan", "JP", "ja"},
		{"korea", "KR", "ko"},
		{"china", "CN", "zh-CN"},
		{"taiwan", "TW", "zh-TW"},
		{"empty", "", "en"},
		{"unknown_US", "US", "en"},
		{"unknown_FR", "FR", "en"},
		{"lowercase_jp", "jp", "ja"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, countryCodeToLanguage(tt.cc))
		})
	}
}

func TestAdminAreaToLanguage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		adminArea string
		want      string
	}{
		{"japan_tokyo", "JP-13", "ja"},
		{"japan_osaka", "JP-27", "ja"},
		{"korea_seoul", "KR-11", "ko"},
		{"china_beijing", "CN-BJ", "zh-CN"},
		{"taiwan_taipei", "TW-TPE", "zh-TW"},
		{"usa_california", "US-CA", "en"},
		{"empty", "", "en"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, adminAreaToLanguage(tt.adminArea))
		})
	}
}
