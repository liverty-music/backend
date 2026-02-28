package geo

import "testing"

func TestDisplayName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		code string
		lang string
		want string
	}{
		{"ja tokyo", "JP-13", "ja", "東京都"},
		{"en tokyo", "JP-13", "en", "Tokyo"},
		{"ja osaka", "JP-27", "ja", "大阪府"},
		{"en osaka", "JP-27", "en", "Osaka"},
		{"ja hokkaido", "JP-01", "ja", "北海道"},
		{"en hokkaido", "JP-01", "en", "Hokkaido"},
		{"ja aichi", "JP-23", "ja", "愛知県"},
		{"en aichi", "JP-23", "en", "Aichi"},
		{"unknown code returns code", "XX-99", "ja", "XX-99"},
		{"unsupported lang defaults to en", "JP-13", "fr", "Tokyo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := DisplayName(tt.code, tt.lang)
			if got != tt.want {
				t.Errorf("DisplayName(%q, %q) = %q, want %q", tt.code, tt.lang, got, tt.want)
			}
		})
	}
}
