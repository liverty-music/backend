package gemini

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseMerchURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"bare url", "https://artist.example.com/goods", "https://artist.example.com/goods"},
		{"NONE sentinel", "NONE", ""},
		{"none lowercase", "none", ""},
		{"empty", "", ""},
		{"whitespace trimmed", "  https://artist.example.com/goods  ", "https://artist.example.com/goods"},
		{"social media post", "https://x.com/test_artist/status/123", "https://x.com/test_artist/status/123"},
		{"url embedded in prose", "公式グッズはこちら: https://artist.example.com/goods です", "https://artist.example.com/goods"},
		{"trailing japanese period stripped", "https://artist.example.com/goods。", "https://artist.example.com/goods"},
		{"trailing paren stripped", "(https://artist.example.com/goods)", "https://artist.example.com/goods"},
		{"no url present", "見つかりませんでした", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, parseMerchURL(tt.raw))
		})
	}
}
