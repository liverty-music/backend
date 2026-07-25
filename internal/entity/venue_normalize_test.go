package entity_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

func TestNormalizeVenueName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "unprefixed name is unchanged", in: "フェスティバルホール", want: "フェスティバルホール"},
		{
			name: "leading admin-area dot prefix is stripped",
			in:   "大阪・フェスティバルホール",
			want: "フェスティバルホール",
		},
		{
			name: "full-width performance prefix is stripped",
			in:   "大阪公演 ＠フェスティバルホール",
			want: "フェスティバルホール",
		},
		{
			name: "half-width performance prefix is stripped",
			in:   "東京公演@Zepp Tokyo",
			want: "Zepp Tokyo",
		},
		{
			name: "full-width ASCII folds to half-width",
			in:   "Ｚｅｐｐ　Ｔｏｋｙｏ",
			want: "Zepp Tokyo",
		},
		{
			name: "whitespace is collapsed and trimmed",
			in:   "  フェスティバル　　ホール  ",
			want: "フェスティバル ホール",
		},
		{name: "blank input returns empty", in: "   ", want: ""},
		{
			name: "long token before a middle dot is not treated as a prefix",
			in:   "サントリーホール・大ホール",
			want: "サントリーホール・大ホール",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, entity.NormalizeVenueName(tt.in))
		})
	}
}

// TestNormalizeVenueName_DriftCollapsesButDistinctStays asserts the two
// observed prod-drift spellings collapse to one identity while genuinely
// different venues stay distinct.
func TestNormalizeVenueName_DriftCollapsesButDistinctStays(t *testing.T) {
	t.Parallel()

	assert.Equal(t,
		entity.NormalizeVenueName("フェスティバルホール"),
		entity.NormalizeVenueName("大阪・フェスティバルホール"),
		"prefixed and unprefixed drift must collapse to the same identity",
	)
	assert.NotEqual(t,
		entity.NormalizeVenueName("日本武道館"),
		entity.NormalizeVenueName("東京国際フォーラム"),
		"genuinely different venues must stay distinct",
	)
}
