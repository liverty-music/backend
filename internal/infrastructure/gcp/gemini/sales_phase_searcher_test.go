package gemini

import (
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ----- parseSalesPhaseStep1Envelope -----

func TestParseSalesPhaseStep1Envelope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		raw           string
		wantSourceURL string
		wantPhases    int
	}{
		{
			name: "valid envelope with two phases",
			raw: `<extracted>
  <source_url>https://example.com/ticket</source_url>
  <phase>
    <method>抽選</method>
    <channel>ファンクラブ</channel>
    <provider_name>e+</provider_name>
    <sequence>0</sequence>
    <apply_start>2026年7月1日 10:00</apply_start>
    <apply_end>2026年7月10日 23:59</apply_end>
    <lottery_result></lottery_result>
    <payment_deadline></payment_deadline>
    <url>https://eplus.jp/example</url>
  </phase>
  <phase>
    <method>先着</method>
    <channel>一般</channel>
    <provider_name>ローチケ</provider_name>
    <sequence>0</sequence>
    <apply_start>2026年7月20日 10:00</apply_start>
    <apply_end></apply_end>
    <lottery_result></lottery_result>
    <payment_deadline></payment_deadline>
    <url>https://l-tike.com/example</url>
  </phase>
</extracted>`,
			wantSourceURL: "https://example.com/ticket",
			wantPhases:    2,
		},
		{
			name:          "envelope with markdown code fence is stripped",
			raw:           "```xml\n<extracted>\n  <source_url>https://example.com</source_url>\n  <phase><method>抽選</method><channel>一般</channel><provider_name></provider_name><sequence>0</sequence><apply_start>2026年7月1日</apply_start><apply_end></apply_end><lottery_result></lottery_result><payment_deadline></payment_deadline><url></url></phase>\n</extracted>\n```",
			wantSourceURL: "https://example.com",
			wantPhases:    1,
		},
		{
			name:          "empty string produces no phases",
			raw:           "",
			wantSourceURL: "",
			wantPhases:    0,
		},
		{
			name:          "non-XML prose returns no phases",
			raw:           "チケット販売情報は見つかりませんでした。",
			wantSourceURL: "",
			wantPhases:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			envelope, phases := parseSalesPhaseStep1Envelope(tt.raw)
			assert.Equal(t, tt.wantSourceURL, envelope.SourceURL)
			assert.Len(t, phases, tt.wantPhases)
		})
	}
}

// ----- parseSalesPhaseStep2Response -----

func TestParseSalesPhaseStep2Response(t *testing.T) {
	t.Parallel()

	xmlPhases := []salesPhaseXML{
		{
			SeriesID:     "series-a",
			Method:       "抽選",
			Channel:      "ファンクラブ",
			ProviderName: "e+",
			Sequence:     "0",
			URL:          "https://eplus.jp/example",
		},
		{
			SeriesID:     "series-b",
			Method:       "先着",
			Channel:      "一般",
			ProviderName: "ローチケ",
			Sequence:     "0",
			URL:          "https://l-tike.com/example",
		},
	}
	bothValid := map[string]struct{}{"series-a": {}, "series-b": {}}
	onlyA := map[string]struct{}{"series-a": {}}

	tests := []struct {
		name      string
		rawJSON   string
		xmlPhases []salesPhaseXML
		valid     map[string]struct{}
		sourceURL string
		wantLen   int
		wantErr   bool
	}{
		{
			name: "two series-level phases coerced",
			rawJSON: `{
  "phases": [
    {
      "output_index": 0,
      "apply_start": "2026-07-01T10:00:00+09:00",
      "apply_end": "2026-07-10T23:59:00+09:00",
      "lottery_result": "",
      "payment_deadline": ""
    },
    {
      "output_index": 1,
      "apply_start": "2026-07-20T10:00:00+09:00",
      "apply_end": "",
      "lottery_result": "",
      "payment_deadline": ""
    }
  ]
}`,
			xmlPhases: xmlPhases,
			valid:     bothValid,
			sourceURL: "https://example.com/ticket",
			wantLen:   2,
		},
		{
			name: "single series-level phase coerced",
			rawJSON: `{
  "phases": [
    {
      "output_index": 0,
      "apply_start": "2026-07-01T10:00:00+09:00",
      "apply_end": "",
      "lottery_result": "",
      "payment_deadline": ""
    }
  ]
}`,
			xmlPhases: xmlPhases[:1],
			valid:     onlyA,
			sourceURL: "https://example.com/ticket",
			wantLen:   1,
		},
		{
			name: "persistence guard: phase with empty apply_start is dropped",
			rawJSON: `{
  "phases": [
    {
      "output_index": 0,
      "apply_start": "",
      "apply_end": "",
      "lottery_result": "",
      "payment_deadline": ""
    }
  ]
}`,
			xmlPhases: xmlPhases[:1],
			valid:     onlyA,
			sourceURL: "https://example.com/ticket",
			wantLen:   0,
		},
		{
			name:      "empty grounding: empty JSON produces no phases",
			rawJSON:   `{"phases":[]}`,
			xmlPhases: xmlPhases,
			valid:     bothValid,
			sourceURL: "",
			wantLen:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseSalesPhaseStep2Response(
				tt.rawJSON,
				tt.xmlPhases,
				tt.valid,
				tt.sourceURL,
				nil,
			)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Len(t, got, tt.wantLen)
		})
	}
}

func TestParseSalesPhaseStep2Response_FieldShaping(t *testing.T) {
	t.Parallel()

	xmlPhases := []salesPhaseXML{{
		SeriesID:     "series-001",
		Method:       "抽選",
		Channel:      "ファンクラブ",
		ProviderName: "e+",
		Sequence:     "1",
		URL:          "https://eplus.jp/example",
	}}

	rawJSON := `{
  "phases": [
    {
      "output_index": 0,
      "apply_start": "2026-07-01T10:00:00+09:00",
      "apply_end": "2026-07-10T23:59:00+09:00",
      "lottery_result": "2026-07-20T12:00:00+09:00",
      "payment_deadline": "2026-07-25T23:59:00+09:00"
    }
  ]
}`

	got, err := parseSalesPhaseStep2Response(rawJSON, xmlPhases, map[string]struct{}{"series-001": {}}, "https://example.com", nil)
	require.NoError(t, err)
	require.Len(t, got, 1)

	c := got[0]
	assert.Equal(t, "series-001", c.SeriesID)
	assert.Equal(t, entity.SalesMethodLottery, c.Method)
	assert.Equal(t, entity.SalesChannelFanClub, c.Channel)
	assert.Equal(t, "e+", c.ProviderName)
	assert.Equal(t, 1, c.Sequence)
	assert.Equal(t, "https://eplus.jp/example", c.URL)
	assert.Equal(t, "https://example.com", c.SourceURL)

	// Compare by Unix timestamp to avoid timezone-name differences between
	// time.Parse (which returns a fixed-offset zone with no name) and
	// time.FixedZone (which returns a named zone). Both represent the same
	// instant.
	wantApplyStartUnix := time.Date(2026, 7, 1, 10, 0, 0, 0, time.FixedZone("JST", 9*3600)).Unix()
	assert.Equal(t, wantApplyStartUnix, c.ApplyStartTime.Unix())
}

// ----- parseSalesMethod / parseSalesChannel -----

func TestParseSalesMethod(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want entity.SalesMethod
	}{
		{"lottery", "抽選", entity.SalesMethodLottery},
		{"first_come", "先着", entity.SalesMethodFirstCome},
		{"unknown", "不明", entity.SalesMethodUnspecified},
		{"empty", "", entity.SalesMethodUnspecified},
		{"with whitespace", "  抽選  ", entity.SalesMethodLottery},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, parseSalesMethod(tt.raw))
		})
	}
}

func TestParseSalesChannel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  string
		want entity.SalesChannel
	}{
		// The 6 named channels.
		{"fan_club", "ファンクラブ", entity.SalesChannelFanClub},
		{"official", "公式", entity.SalesChannelOfficial},
		{"playguide", "プレイガイド", entity.SalesChannelPlayguide},
		{"credit_card", "クレジットカード", entity.SalesChannelCreditCard},
		{"mobile_carrier", "携帯キャリア", entity.SalesChannelMobileCarrier},
		{"general", "一般", entity.SalesChannelGeneral},
		// Former per-provider channel strings now return UNSPECIFIED because
		// the model emits "プレイガイド" for all play-guide phases; the
		// provider name is stored verbatim in ProviderName, not the channel.
		{"eplus_raw_unrecognized", "e+", entity.SalesChannelUnspecified},
		{"ltike_raw_unrecognized", "ローチケ", entity.SalesChannelUnspecified},
		{"unknown", "不明な会社", entity.SalesChannelUnspecified},
		{"empty", "", entity.SalesChannelUnspecified},
		{"whitespace", "   ", entity.SalesChannelUnspecified},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, parseSalesChannel(tt.raw))
		})
	}
}

// ----- parseRFC3339OrZero -----

func TestParseRFC3339OrZero(t *testing.T) {
	t.Parallel()

	jst := time.FixedZone("JST", 9*3600)
	want := time.Date(2026, 7, 1, 10, 0, 0, 0, jst)

	tests := []struct {
		name     string
		input    string
		wantZero bool
		wantTime time.Time
	}{
		{"valid RFC3339", "2026-07-01T10:00:00+09:00", false, want},
		{"empty string", "", true, time.Time{}},
		{"null string", "null", true, time.Time{}},
		{"unparseable", "2026年7月1日 10:00", true, time.Time{}},
		{"whitespace only", "   ", true, time.Time{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := parseRFC3339OrZero(tt.input)
			if tt.wantZero {
				assert.True(t, got.IsZero(), "expected zero time but got %v", got)
			} else {
				assert.Equal(t, tt.wantTime.Unix(), got.Unix())
			}
		})
	}
}

func TestFilterUpcomingPhases(t *testing.T) {
	now := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	mk := func(start, end time.Time) *entity.SalesPhaseCandidate {
		return &entity.SalesPhaseCandidate{ApplyStartTime: start, ApplyEndTime: end}
	}
	closed := mk(now.AddDate(0, -3, 0), now.AddDate(0, 0, -10)) // ended 10d ago → drop
	open := mk(now.AddDate(0, 0, -2), now.AddDate(0, 0, 3))     // open now → keep
	upcoming := mk(now.AddDate(0, 0, 5), now.AddDate(0, 0, 12)) // future → keep
	unknownEnd := mk(now.AddDate(0, 0, -1), time.Time{})        // unknown end → keep

	got := filterUpcomingPhases([]*entity.SalesPhaseCandidate{closed, open, upcoming, unknownEnd}, now)
	if len(got) != 3 {
		t.Fatalf("expected 3 phases kept, got %d", len(got))
	}
	for _, c := range got {
		if !c.ApplyEndTime.IsZero() && c.ApplyEndTime.Before(now) {
			t.Errorf("closed phase leaked through: end=%v", c.ApplyEndTime)
		}
	}
}

func TestSanitizeTimeline(t *testing.T) {
	base := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	d := func(days int) time.Time { return base.AddDate(0, 0, days) }
	zero := time.Time{}

	t.Run("monotonic timeline is preserved", func(t *testing.T) {
		end, res, pay := sanitizeTimeline(base, d(10), d(20), d(25))
		assert.True(t, d(10).Equal(end))
		assert.True(t, d(20).Equal(res))
		assert.True(t, d(25).Equal(pay))
	})
	t.Run("apply_end before start is nulled", func(t *testing.T) {
		end, res, pay := sanitizeTimeline(base, d(-5), d(20), d(25))
		assert.True(t, end.IsZero(), "end before start must be nulled")
		// lower bound falls back to start, so later valid fields survive.
		assert.True(t, d(20).Equal(res))
		assert.True(t, d(25).Equal(pay))
	})
	t.Run("lottery_result before apply_end is nulled, payment still validated", func(t *testing.T) {
		end, res, pay := sanitizeTimeline(base, d(10), d(5), d(25))
		assert.True(t, d(10).Equal(end))
		assert.True(t, res.IsZero(), "result before end must be nulled")
		assert.True(t, d(25).Equal(pay))
	})
	t.Run("payment before lower bound is nulled", func(t *testing.T) {
		end, res, pay := sanitizeTimeline(base, d(10), d(20), d(15))
		assert.True(t, d(10).Equal(end))
		assert.True(t, d(20).Equal(res))
		assert.True(t, pay.IsZero(), "payment before result must be nulled")
	})
	t.Run("unknown (zero) fields pass through", func(t *testing.T) {
		end, res, pay := sanitizeTimeline(base, zero, zero, zero)
		assert.True(t, end.IsZero() && res.IsZero() && pay.IsZero())
	})
}
