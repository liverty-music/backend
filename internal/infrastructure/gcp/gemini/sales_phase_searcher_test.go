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
    <covered_dates>2026年9月1日,2026年9月2日</covered_dates>
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
    <covered_dates></covered_dates>
  </phase>
</extracted>`,
			wantSourceURL: "https://example.com/ticket",
			wantPhases:    2,
		},
		{
			name:          "envelope with markdown code fence is stripped",
			raw:           "```xml\n<extracted>\n  <source_url>https://example.com</source_url>\n  <phase><method>抽選</method><channel>一般</channel><provider_name></provider_name><sequence>0</sequence><apply_start>2026年7月1日</apply_start><apply_end></apply_end><lottery_result></lottery_result><payment_deadline></payment_deadline><url></url><covered_dates></covered_dates></phase>\n</extracted>\n```",
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

	// Reference time for candidate events.
	date1 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	date2 := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)

	candidateEvents := []*entity.SalesPhaseCandidateEvent{
		{EventID: "event-aaa", LocalDate: date1, ListedVenueName: "VenueA", AdminArea: "東京都"},
		{EventID: "event-bbb", LocalDate: date2, ListedVenueName: "VenueB", AdminArea: "大阪府"},
	}

	xmlPhases := []salesPhaseXML{
		{
			Method:       "抽選",
			Channel:      "ファンクラブ",
			ProviderName: "e+",
			Sequence:     "0",
			URL:          "https://eplus.jp/example",
		},
		{
			Method:       "先着",
			Channel:      "一般",
			ProviderName: "ローチケ",
			Sequence:     "0",
			URL:          "https://l-tike.com/example",
		},
	}

	tests := []struct {
		name    string
		rawJSON string
		args    struct {
			xmlPhases       []salesPhaseXML
			seriesID        string
			sourceURL       string
			candidateEvents []*entity.SalesPhaseCandidateEvent
		}
		wantLen int
		wantErr bool
	}{
		{
			name: "verbatim to coerce to SalesPhase shaping: two phases resolved",
			rawJSON: `{
  "phases": [
    {
      "output_index": 0,
      "apply_start": "2026-07-01T10:00:00+09:00",
      "apply_end": "2026-07-10T23:59:00+09:00",
      "lottery_result": "",
      "payment_deadline": "",
      "covered_event_indices": [0, 1]
    },
    {
      "output_index": 1,
      "apply_start": "2026-07-20T10:00:00+09:00",
      "apply_end": "",
      "lottery_result": "",
      "payment_deadline": "",
      "covered_event_indices": []
    }
  ]
}`,
			args: struct {
				xmlPhases       []salesPhaseXML
				seriesID        string
				sourceURL       string
				candidateEvents []*entity.SalesPhaseCandidateEvent
			}{
				xmlPhases:       xmlPhases,
				seriesID:        "series-111",
				sourceURL:       "https://example.com/ticket",
				candidateEvents: candidateEvents,
			},
			wantLen: 2,
		},
		{
			name: "covered-event resolution: indices mapped to event IDs",
			rawJSON: `{
  "phases": [
    {
      "output_index": 0,
      "apply_start": "2026-07-01T10:00:00+09:00",
      "apply_end": "",
      "lottery_result": "",
      "payment_deadline": "",
      "covered_event_indices": [0]
    }
  ]
}`,
			args: struct {
				xmlPhases       []salesPhaseXML
				seriesID        string
				sourceURL       string
				candidateEvents []*entity.SalesPhaseCandidateEvent
			}{
				xmlPhases:       xmlPhases[:1],
				seriesID:        "series-222",
				sourceURL:       "https://example.com/ticket",
				candidateEvents: candidateEvents,
			},
			wantLen: 1,
		},
		{
			name: "empty covered_event_indices defaults to all candidates",
			rawJSON: `{
  "phases": [
    {
      "output_index": 0,
      "apply_start": "2026-07-01T10:00:00+09:00",
      "apply_end": "",
      "lottery_result": "",
      "payment_deadline": "",
      "covered_event_indices": []
    }
  ]
}`,
			args: struct {
				xmlPhases       []salesPhaseXML
				seriesID        string
				sourceURL       string
				candidateEvents []*entity.SalesPhaseCandidateEvent
			}{
				xmlPhases:       xmlPhases[:1],
				seriesID:        "series-333",
				sourceURL:       "https://example.com/ticket",
				candidateEvents: candidateEvents,
			},
			wantLen: 1,
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
      "payment_deadline": "",
      "covered_event_indices": [0]
    }
  ]
}`,
			args: struct {
				xmlPhases       []salesPhaseXML
				seriesID        string
				sourceURL       string
				candidateEvents []*entity.SalesPhaseCandidateEvent
			}{
				xmlPhases:       xmlPhases[:1],
				seriesID:        "series-444",
				sourceURL:       "https://example.com/ticket",
				candidateEvents: candidateEvents,
			},
			wantLen: 0,
		},
		{
			name:    "empty grounding: empty JSON produces no phases",
			rawJSON: `{"phases":[]}`,
			args: struct {
				xmlPhases       []salesPhaseXML
				seriesID        string
				sourceURL       string
				candidateEvents []*entity.SalesPhaseCandidateEvent
			}{
				xmlPhases:       xmlPhases,
				seriesID:        "series-555",
				sourceURL:       "",
				candidateEvents: candidateEvents,
			},
			wantLen: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseSalesPhaseStep2Response(
				tt.rawJSON,
				tt.args.xmlPhases,
				tt.args.seriesID,
				tt.args.sourceURL,
				tt.args.candidateEvents,
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

	date1 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	date2 := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	candidateEvents := []*entity.SalesPhaseCandidateEvent{
		{EventID: "event-aaa", LocalDate: date1, ListedVenueName: "VenueA"},
		{EventID: "event-bbb", LocalDate: date2, ListedVenueName: "VenueB"},
	}

	xmlPhases := []salesPhaseXML{{
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
      "payment_deadline": "2026-07-25T23:59:00+09:00",
      "covered_event_indices": [0, 1]
    }
  ]
}`

	got, err := parseSalesPhaseStep2Response(rawJSON, xmlPhases, "series-001", "https://example.com", candidateEvents, nil)
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

	// Anchor event ID is the earliest covered event.
	assert.Equal(t, "event-aaa", c.AnchorEventID)

	// Both covered events must be present.
	assert.ElementsMatch(t, []string{"event-aaa", "event-bbb"}, c.CoveredEventIDs)
}

// ----- resolveCoveredEvents -----

func TestResolveCoveredEvents(t *testing.T) {
	t.Parallel()

	candidates := []*entity.SalesPhaseCandidateEvent{
		{EventID: "event-0"},
		{EventID: "event-1"},
		{EventID: "event-2"},
	}

	tests := []struct {
		name    string
		indices []int
		want    []string
	}{
		{
			name:    "specific indices mapped correctly",
			indices: []int{0, 2},
			want:    []string{"event-0", "event-2"},
		},
		{
			name:    "empty indices returns all candidates",
			indices: []int{},
			want:    []string{"event-0", "event-1", "event-2"},
		},
		{
			name:    "out-of-range indices are skipped",
			indices: []int{1, 99},
			want:    []string{"event-1"},
		},
		{
			name:    "duplicate indices deduplicated",
			indices: []int{0, 0, 1},
			want:    []string{"event-0", "event-1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := resolveCoveredEvents(tt.indices, candidates)
			assert.ElementsMatch(t, tt.want, got)
		})
	}
}

// ----- earliestEventID -----

func TestEarliestEventID(t *testing.T) {
	t.Parallel()

	d1 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	d2 := time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC)
	d3 := time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC)

	candidates := []*entity.SalesPhaseCandidateEvent{
		{EventID: "event-a", LocalDate: d1},
		{EventID: "event-b", LocalDate: d2},
		{EventID: "event-c", LocalDate: d3},
	}

	tests := []struct {
		name    string
		indices []int
		want    string
	}{
		{
			name:    "returns earliest among given indices",
			indices: []int{1, 2},
			want:    "event-b",
		},
		{
			name:    "empty indices returns overall earliest",
			indices: []int{},
			want:    "event-a",
		},
		{
			name:    "single index returns that event",
			indices: []int{2},
			want:    "event-c",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, earliestEventID(tt.indices, candidates))
		})
	}
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
