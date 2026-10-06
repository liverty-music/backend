package gemini_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/infrastructure/gcp/gemini"
	"github.com/stretchr/testify/assert"
)

func TestNormalizeVenue(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		// Prefecture prefix ("PREFECTURE・<venue>")
		{"prefix_osaka", "大阪府・Billborad Live OSAKA", "billboradliveosaka"},
		{"prefix_tokyo", "東京都・Billborad Live TOKYO", "billboradlivetokyo"},
		{"prefix_kyoto", "京都府・磔磔", "磔磔"},
		{"prefix_saitama", "埼玉県・HEAVEN'S ROCK さいたま新都心 VJ-3", "heaven'srockさいたま新都心vj3"},

		// Parenthesised prefecture suffix
		{"paren_chiba", "幕張メッセ 9・11ホール（千葉県）", "幕張メッセ911ホール"},
		{"paren_ascii", "Zepp Haneda (東京都)", "zepphaneda"},

		// Both — prefix and inner paren (rare but possible)
		{"prefix_and_paren", "大阪府・京セラドーム大阪（大阪府）", "京セラドーム大阪"},

		// Prefecture embedded in venue name MUST NOT be stripped — only
		// matches the regex pattern (prefix `・` or parenthesised), so
		// "新潟県民会館" stays intact.
		{"embedded_safe", "新潟県民会館", "新潟県民会館"},

		// TBD markers collapse to empty
		{"tbd_empty", "", ""},
		{"tbd_dashed", "-STAY TUNED-", ""},
		{"tbd_japanese", "未定", ""},
		{"tbd_tba", "TBA", ""},
		{"tbd_coming_soon", "Coming Soon", ""},

		// Original punctuation / whitespace handling preserved
		{"basic_lowercase", "Zepp Tokyo", "zepptokyo"},
		{"middle_dot_split", "幕張メッセ 9・11ホール", "幕張メッセ911ホール"},

		// Notation variants across official pages of the same venue
		{"nfkc_cjk_radical", "クロコくんホール(旧 ⽇本ガイシホール)", "日本ガイシホール"},
		{"latin1_middle_dot", "朱鷺メッセ·新潟コンベンションセンター", "朱鷺メッセ新潟コンベンションセンター"},
		{"katakana_middle_dot", "朱鷺メッセ・新潟コンベンションセンター", "朱鷺メッセ新潟コンベンションセンター"},
		{"fullwidth_parens", "盛岡タカヤアリーナ（盛岡市総合アリーナ）", "盛岡タカヤアリーナ盛岡市総合アリーナ"},
		{"halfwidth_parens_spaced", "盛岡タカヤアリーナ (盛岡市総合アリーナ)", "盛岡タカヤアリーナ盛岡市総合アリーナ"},
		{"spacing", "渋谷 CLUB QUATTRO", "渋谷clubquattro"},
		{"no_spacing", "渋谷CLUB QUATTRO", "渋谷clubquattro"},
		{"fullwidth_alnum", "マリンメッセ福岡Ａ館（福岡県）", "マリンメッセ福岡a館"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := gemini.NormalizeVenue(tt.in)
			assert.Equal(t, tt.want, got)
		})
	}
}
