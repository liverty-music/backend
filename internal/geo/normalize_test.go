package geo

import "testing"

func TestNormalizeAdminArea(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  *string
	}{
		// Japanese with suffix
		{"ja full tokyo", "東京都", ptr("JP-13")},
		{"ja full osaka", "大阪府", ptr("JP-27")},
		{"ja full aichi", "愛知県", ptr("JP-23")},
		{"ja full hokkaido", "北海道", ptr("JP-01")},
		{"ja full kyoto", "京都府", ptr("JP-26")},

		// Japanese without suffix
		{"ja short tokyo", "東京", ptr("JP-13")},
		{"ja short osaka", "大阪", ptr("JP-27")},
		{"ja short aichi", "愛知", ptr("JP-23")},
		{"ja short fukuoka", "福岡", ptr("JP-40")},
		{"ja short okinawa", "沖縄", ptr("JP-47")},

		// English
		{"en tokyo", "tokyo", ptr("JP-13")},
		{"en osaka", "osaka", ptr("JP-27")},
		{"en aichi", "aichi", ptr("JP-23")},
		{"en hokkaido", "hokkaido", ptr("JP-01")},
		{"en fukuoka", "fukuoka", ptr("JP-40")},

		// Case insensitivity
		{"en mixed case Tokyo", "Tokyo", ptr("JP-13")},
		{"en upper OSAKA", "OSAKA", ptr("JP-27")},
		{"en mixed Fukuoka", "Fukuoka", ptr("JP-40")},

		// Whitespace handling
		{"leading space", " 東京", ptr("JP-13")},
		{"trailing space", "東京 ", ptr("JP-13")},
		{"surrounding space", " tokyo ", ptr("JP-13")},

		// Empty/unknown
		{"empty string", "", nil},
		{"whitespace only", "   ", nil},
		{"unknown text", "somewhere", nil},
		{"unknown jp", "月面基地", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := NormalizeAdminArea(tt.input)
			if tt.want == nil {
				if got != nil {
					t.Errorf("NormalizeAdminArea(%q) = %q, want nil", tt.input, *got)
				}
				return
			}
			if got == nil {
				t.Errorf("NormalizeAdminArea(%q) = nil, want %q", tt.input, *tt.want)
				return
			}
			if *got != *tt.want {
				t.Errorf("NormalizeAdminArea(%q) = %q, want %q", tt.input, *got, *tt.want)
			}
		})
	}
}

func ptr(s string) *string {
	return &s
}
