package entity_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestSameTourTitle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{
			// @spec components/entity/series "Quotation marks and case differ"
			name: "Quotation marks and case differ",
			a:    "2026 LE SSERAFIM TOUR 'PUREFLOW'",
			b:    "2026 LE SSERAFIM TOUR “PUREFLOW”",
			want: true,
		},
		{
			// @spec components/entity/series "Full-width letters"
			name: "Full-width letters",
			a:    "ＡＲＥＮＡ ＴＯＵＲ ２０２６",
			b:    "Arena Tour 2026",
			want: true,
		},
		{
			// @spec components/entity/series "Half-width voiced katakana"
			name: "Half-width voiced katakana",
			a:    "ｶﾞｰﾃﾞﾝ TOUR",
			b:    "ガーデン TOUR",
			want: true,
		},
		{
			// @spec components/entity/series "Accented Latin letters in another case"
			name: "Accented Latin letters in another case",
			a:    "CAFÉ TOUR",
			b:    "café tour",
			want: true,
		},
		{
			// @spec components/entity/series "Sequel numbered with a Roman numeral"
			name: "Sequel numbered with a Roman numeral",
			a:    "ARENA TOUR Ⅰ",
			b:    "ARENA TOUR Ⅱ",
			want: false,
		},
		{
			// @spec components/entity/series "Roman numeral is not the same as its letters"
			name: "Roman numeral is not the same as its letters",
			a:    "TOUR Ⅱ",
			b:    "TOUR II",
			want: false,
		},
		{
			// @spec components/entity/series "Circled numerals differ"
			name: "Circled numerals differ",
			a:    "VOL.①",
			b:    "VOL.②",
			want: false,
		},
		{
			// @spec components/entity/series "Different year"
			name: "Different year",
			a:    "ARENA TOUR 2026",
			b:    "ARENA TOUR 2027",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, entity.SameTourTitle(tt.a, tt.b))
			assert.Equal(t, tt.want, entity.SameTourTitle(tt.b, tt.a), "the relation is symmetric")
		})
	}
}
