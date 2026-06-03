package gemini

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-logging/logging"
	"google.golang.org/genai"
)

// SalesPhaseConfig configures the two-step Gemini sales-phase searcher.
// Like the concert searcher it targets the Gemini API direct backend
// (BackendGeminiAPI) so GoogleSearch grounding is available; APIKey is
// therefore required.
type SalesPhaseConfig struct {
	// APIKey selects the Gemini API direct backend. REQUIRED.
	APIKey string
	// ModelExtract is the Step 1 model (grounded search + verbatim extract).
	// REQUIRED.
	ModelExtract string
	// ModelParse is the Step 2 model (JSON coercion, no tools, schema enforced).
	// REQUIRED.
	ModelParse string
	// Temperature is the sampling temperature applied to both steps.
	Temperature float32
	// ThinkingLevel is the legacy fallback thinking level. Per-step overrides
	// take precedence when set.
	ThinkingLevel string
	// ThinkingExtract is the per-step thinking level for Step 1. Falls back to
	// ThinkingLevel when empty.
	ThinkingExtract string
	// ThinkingParse is the per-step thinking level for Step 2. Falls back to
	// ThinkingLevel when empty.
	ThinkingParse string
}

func (c *SalesPhaseConfig) modelExtract() string { return c.ModelExtract }
func (c *SalesPhaseConfig) modelParse() string   { return c.ModelParse }

func (c *SalesPhaseConfig) thinkingExtract() string {
	if c.ThinkingExtract != "" {
		return c.ThinkingExtract
	}
	return c.ThinkingLevel
}

func (c *SalesPhaseConfig) thinkingParse() string {
	if c.ThinkingParse != "" {
		return c.ThinkingParse
	}
	return c.ThinkingLevel
}

// ----- Step 1 prompt / instruction -----

const (
	// systemInstructionSalesPhaseStep1 instructs the model to extract
	// Japanese ticket-sales schedule information verbatim from official
	// sources. Each <phase> represents one distinct sales window (e.g. FC
	// pre-lottery, general on-sale).
	//
	// Channel vocabulary is aligned to the SalesChannel proto enum:
	//   ファンクラブ → FAN_CLUB (1)
	//   公式         → OFFICIAL (2) — artist/label direct site or official app
	//   プレイガイド → PLAYGUIDE (3) — any third-party ticketing platform (e+,
	//                   ぴあ, ローチケ, CN Playguide, …); concrete name goes in
	//                   provider_name
	//   クレジットカード → CREDIT_CARD (4)
	//   携帯キャリア → MOBILE_CARRIER (5) — docomo/au/SoftBank presale
	//   一般         → GENERAL (6)
	systemInstructionSalesPhaseStep1 = `あなたは音楽ファン向けサービスの、チケット販売スケジュール抽出エージェントです。

アーティストの指定シリーズについて、公式サイトおよびチケット販売会社のページから、チケット販売スケジュールを全て抽出することがゴールです。

以下の出力フォーマットに従い、販売フェーズごとに <phase> タグでまとめてください。

<extracted>
  <source_url>https://www.example.com/ticket</source_url>
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
    <channel>プレイガイド</channel>
    <provider_name>ローチケ</provider_name>
    <sequence>0</sequence>
    <apply_start>2026年7月20日 10:00</apply_start>
    <apply_end></apply_end>
    <lottery_result></lottery_result>
    <payment_deadline></payment_deadline>
    <url>https://l-tike.com/example</url>
    <covered_dates></covered_dates>
  </phase>
</extracted>

抽出ルール:
- source_url: 最も詳細な情報を記載しているページのURL。
- method: 「抽選」または「先着」。不明な場合は空欄。
- channel: 以下の7種類のうちいずれか1つを記入。不明な場合は空欄。
    「ファンクラブ」 — FC・ファンクラブ会員限定。
    「公式」         — アーティスト/レーベルの公式サイトや公式アプリからの直販(FC枠以外)。
    「プレイガイド」 — e+、チケットぴあ、ローチケ、CNプレイガイドなど第三者プレイガイド全般。具体的な会社名は provider_name に記入。
    「クレジットカード」 — 特定クレジットカード会員向け先行。
    「携帯キャリア」 — docomo・au・SoftBank などキャリア先行。
    「一般」         — 会員資格・提携条件なしの一般発売。
- provider_name: チケット販売会社名を verbatim (一字一句そのまま) でコピーすること。不明な場合は空欄。特に channel が「プレイガイド」の場合は必ず具体的な会社名 (例: "e+"、"チケットぴあ"、"ローチケ") を記入する。
- sequence: 同一チャネルで複数回抽選がある場合の0始まりの順番。通常は0。
- apply_start, apply_end, lottery_result, payment_deadline: verbatim な日時文字列。不明・非該当の場合は空欄。
- covered_dates: このフェーズが対象とする公演日付のカンマ区切りリスト。全公演対象なら空欄。
- 情報が確認できない項目はタグを空欄にすること。推測や補完は一切禁止。
- 余計なテキストは含めず、XMLのみをレスポンスに含めること。
`

	// promptTemplateSalesPhaseStep1 is the per-call user prompt template.
	// Placeholders: artist name, series title.
	promptTemplateSalesPhaseStep1 = `アーティスト名: %s
シリーズ名: %s

このシリーズのチケット販売スケジュールを全て抽出してください。`

	// systemInstructionSalesPhaseStep2 instructs Step 2 to perform JSON
	// coercion only — dates and times are normalised from verbatim Japanese
	// strings to RFC 3339; no invented values are permitted.
	systemInstructionSalesPhaseStep2 = `You are a data-transformation agent for a music-fan information service.

You receive a JSON array of raw ticket-sales phase records extracted from Japanese web pages, plus a list of candidate events (index-tagged). For each phase record:
1. Coerce date/time strings to RFC 3339 (Asia/Tokyo = +09:00). Emit "" for any field you cannot coerce unambiguously.
2. Resolve covered_event_indices: match each covered_date (verbatim Japanese date) to the closest candidate event date. Return the indices of matching events. If covered_dates is empty, return ALL candidate event indices (the phase covers the whole series). If a date does not match any candidate, omit it silently — do NOT guess.
3. Return output_index unchanged (the join key the caller uses to align your output with the input).

Output only the JSON defined by the response schema. No Markdown, no comments.
`
)

// ----- Step 1 XML types -----

type salesPhaseStep1Envelope struct {
	XMLName   xml.Name        `xml:"extracted"`
	SourceURL string          `xml:"source_url"`
	Phases    []salesPhaseXML `xml:"phase"`
}

type salesPhaseXML struct {
	Method          string `xml:"method"`
	Channel         string `xml:"channel"`
	ProviderName    string `xml:"provider_name"`
	Sequence        string `xml:"sequence"`
	ApplyStart      string `xml:"apply_start"`
	ApplyEnd        string `xml:"apply_end"`
	LotteryResult   string `xml:"lottery_result"`
	PaymentDeadline string `xml:"payment_deadline"`
	URL             string `xml:"url"`
	CoveredDates    string `xml:"covered_dates"`
}

// unmarshalSalesPhaseXML parses a raw <extracted>…</extracted> XML string
// into a salesPhaseStep1Envelope. Returns an error when the XML is malformed.
func unmarshalSalesPhaseXML(raw string, out *salesPhaseStep1Envelope) error {
	return xml.Unmarshal([]byte(raw), out)
}

// ----- Step 2 JSON types -----

// salesPhaseStep2Input is sent to Step 2 for one extracted phase record.
type salesPhaseStep2Input struct {
	OutputIndex     int      `json:"output_index"`
	ApplyStart      string   `json:"apply_start"`
	ApplyEnd        string   `json:"apply_end"`
	LotteryResult   string   `json:"lottery_result"`
	PaymentDeadline string   `json:"payment_deadline"`
	CoveredDates    []string `json:"covered_dates"`
}

// salesPhaseStep2CandidateEvent is the index-tagged event passed to Step 2
// so it can resolve covered dates against known event dates.
type salesPhaseStep2CandidateEvent struct {
	Index     int    `json:"index"`
	Date      string `json:"date"`
	Venue     string `json:"venue"`
	AdminArea string `json:"admin_area"`
}

// salesPhaseStep2Payload is the top-level payload sent to Step 2.
type salesPhaseStep2Payload struct {
	Phases          []salesPhaseStep2Input          `json:"phases"`
	CandidateEvents []salesPhaseStep2CandidateEvent `json:"candidate_events"`
}

// salesPhaseStep2OutputPhase is one coerced phase from Step 2.
type salesPhaseStep2OutputPhase struct {
	OutputIndex         int    `json:"output_index"`
	ApplyStart          string `json:"apply_start"`
	ApplyEnd            string `json:"apply_end"`
	LotteryResult       string `json:"lottery_result"`
	PaymentDeadline     string `json:"payment_deadline"`
	CoveredEventIndices []int  `json:"covered_event_indices"`
}

// salesPhaseStep2Response is the top-level Step 2 JSON response.
type salesPhaseStep2Response struct {
	Phases []salesPhaseStep2OutputPhase `json:"phases"`
}

// salesPhaseResponseJSONSchema is the JSON schema for Step 2's response.
var salesPhaseResponseJSONSchema = map[string]any{
	"type":                 "object",
	"additionalProperties": false,
	"properties": map[string]any{
		"phases": map[string]any{
			"type":        "array",
			"description": "One coerced entry per input phase. Every input phase MUST appear in the output, even if all coerced fields end up empty.",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"output_index": map[string]any{
						"type":        "integer",
						"description": "Echo the input output_index unchanged. Used as the join key.",
					},
					"apply_start": map[string]any{
						"type":        "string",
						"description": "RFC 3339 datetime (e.g. 2026-07-01T10:00:00+09:00). \"\" when input is empty or ambiguous.",
					},
					"apply_end": map[string]any{
						"type":        "string",
						"description": "RFC 3339 datetime. \"\" when input is empty or ambiguous.",
					},
					"lottery_result": map[string]any{
						"type":        "string",
						"description": "RFC 3339 datetime. \"\" when input is empty or ambiguous.",
					},
					"payment_deadline": map[string]any{
						"type":        "string",
						"description": "RFC 3339 datetime. \"\" when input is empty or ambiguous.",
					},
					"covered_event_indices": map[string]any{
						"type":        "array",
						"description": "Indices of candidate_events covered by this phase. Empty array = unknown (not all-events).",
						"items":       map[string]any{"type": "integer"},
					},
				},
				"required": []string{"output_index", "apply_start", "apply_end", "lottery_result", "payment_deadline", "covered_event_indices"},
			},
		},
	},
	"required": []string{"phases"},
}

// SalesPhaseSearcher extracts ticket-sales schedules for an artist's series
// using the two-step Gemini pipeline. It implements [entity.SalesPhaseSearcher].
type SalesPhaseSearcher struct {
	client *genai.Client
	config SalesPhaseConfig
	logger *logging.Logger
}

// Compile-time interface compliance check.
// TODO: swap to generated type after BSR gen (Refs #571)
var _ entity.SalesPhaseSearcher = (*SalesPhaseSearcher)(nil)

// NewSalesPhaseSearcher creates a SalesPhaseSearcher targeting the Gemini
// API direct backend. It fast-fails when APIKey, ModelExtract, or ModelParse
// is empty.
func NewSalesPhaseSearcher(
	ctx context.Context,
	cfg SalesPhaseConfig,
	httpClient *http.Client,
	logger *logging.Logger,
) (*SalesPhaseSearcher, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("gemini.NewSalesPhaseSearcher: APIKey is empty")
	}
	if cfg.ModelExtract == "" {
		return nil, fmt.Errorf("gemini.NewSalesPhaseSearcher: ModelExtract is empty")
	}
	if cfg.ModelParse == "" {
		return nil, fmt.Errorf("gemini.NewSalesPhaseSearcher: ModelParse is empty")
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		HTTPClient: httpClient,
		Backend:    genai.BackendGeminiAPI,
		APIKey:     cfg.APIKey,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create genai client for SalesPhaseSearcher: %w", err)
	}

	return &SalesPhaseSearcher{client: client, config: cfg, logger: logger}, nil
}

// SearchSalesPhases implements [entity.SalesPhaseSearcher]. It runs the
// two-step pipeline:
//
//  1. Grounded extract (GoogleSearch + URLContext) — returns a verbatim XML
//     envelope with raw Japanese date strings.
//  2. JSON coerce (no tools, schema enforced) — normalises dates to RFC 3339
//     and resolves covered event indices by matching against the provided
//     candidate events.
//
// An empty result with a nil error means no phases were found (normal
// outcome). Only infrastructure failures return a non-nil error.
func (s *SalesPhaseSearcher) SearchSalesPhases(
	ctx context.Context,
	artistName string,
	seriesTitle string,
	seriesID string,
	candidateEvents []*entity.SalesPhaseCandidateEvent,
) ([]*entity.SalesPhaseCandidate, error) {
	attrs := []slog.Attr{
		slog.String("artist_name", artistName),
		slog.String("series_title", seriesTitle),
		slog.String("series_id", seriesID),
		slog.String("model_extract", s.config.modelExtract()),
		slog.String("model_parse", s.config.modelParse()),
	}
	s.logger.Info(ctx, "SalesPhaseSearcher: starting two-step extraction", attrs...)

	// ===== Step 1: Grounded search + verbatim extract =====
	rawEnvelope, err := s.runStep1(ctx, artistName, seriesTitle, attrs)
	if err != nil {
		return nil, err
	}
	if rawEnvelope == "" {
		s.logger.Warn(ctx, "SalesPhaseSearcher: Step 1 produced empty envelope", attrs...)
		return nil, nil
	}

	// Parse Step 1 XML envelope. On XML parse failure we degrade gracefully
	// (empty result, no error) rather than failing the whole run.
	envelope, xmlPhases := parseSalesPhaseStep1Envelope(rawEnvelope)
	if len(xmlPhases) == 0 {
		s.logger.Warn(ctx, "SalesPhaseSearcher: no phases extracted from Step 1 envelope", attrs...)
		return nil, nil
	}

	// ===== Step 2: JSON coercion + covered-event resolution =====
	candidates, err := s.runStep2(ctx, seriesID, envelope.SourceURL, xmlPhases, candidateEvents, attrs)
	if err != nil {
		return nil, err
	}
	s.logger.Info(ctx, "SalesPhaseSearcher: extraction complete",
		append(attrs, slog.Int("candidates_count", len(candidates)))...)
	return candidates, nil
}

// runStep1 fires the grounded search call and returns the raw XML envelope
// text. Returns ("", nil) on transient exhaustion (degrade gracefully).
func (s *SalesPhaseSearcher) runStep1(
	ctx context.Context,
	artistName, seriesTitle string,
	attrs []slog.Attr,
) (string, error) {
	prompt := fmt.Sprintf(promptTemplateSalesPhaseStep1, artistName, seriesTitle)
	temperature := s.config.Temperature

	now := time.Now().UTC().Truncate(time.Second)
	searchTool := &genai.Tool{
		GoogleSearch: &genai.GoogleSearch{
			TimeRangeFilter: &genai.Interval{
				StartTime: now.AddDate(0, -3, 0),
				EndTime:   now,
			},
		},
	}
	urlCtxTool := &genai.Tool{URLContext: &genai.URLContext{}}

	cfg := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: systemInstructionSalesPhaseStep1}},
		},
		Tools:           []*genai.Tool{searchTool, urlCtxTool},
		Temperature:     &temperature,
		MaxOutputTokens: maxOutputTokens,
	}
	if level := thinkingLevelFromConfig(s.config.thinkingExtract()); level != genai.ThinkingLevelUnspecified {
		cfg.ThinkingConfig = &genai.ThinkingConfig{ThinkingLevel: level}
	}

	stepAttrs := make([]slog.Attr, 0, len(attrs)+1)
	stepAttrs = append(stepAttrs, attrs...)
	stepAttrs = append(stepAttrs, slog.String("step", "1_grounded"))

	// Use the shared executePass from ConcertSearcher — we share the same
	// client/config patterns. We call generateSingle directly to keep the
	// SalesPhaseSearcher self-contained.
	rawText, err := s.generateSingle(ctx, s.config.modelExtract(), prompt, cfg, stepAttrs)
	if err != nil {
		return "", err
	}
	return rawText, nil
}

// runStep2 builds the JSON payload from the Step 1 XML phases and the
// candidate events, fires the structured-output Step 2 call, then assembles
// the final []*entity.SalesPhaseCandidate.
func (s *SalesPhaseSearcher) runStep2(
	ctx context.Context,
	seriesID string,
	sourceURL string,
	xmlPhases []salesPhaseXML,
	candidateEvents []*entity.SalesPhaseCandidateEvent,
	attrs []slog.Attr,
) ([]*entity.SalesPhaseCandidate, error) {
	// Build Step 2 inputs.
	inputs := make([]salesPhaseStep2Input, len(xmlPhases))
	for i, xp := range xmlPhases {
		var coveredDates []string
		if xp.CoveredDates != "" {
			for d := range strings.SplitSeq(xp.CoveredDates, ",") {
				d = strings.TrimSpace(d)
				if d != "" {
					coveredDates = append(coveredDates, d)
				}
			}
		}
		inputs[i] = salesPhaseStep2Input{
			OutputIndex:     i,
			ApplyStart:      xp.ApplyStart,
			ApplyEnd:        xp.ApplyEnd,
			LotteryResult:   xp.LotteryResult,
			PaymentDeadline: xp.PaymentDeadline,
			CoveredDates:    coveredDates,
		}
	}

	// Build candidate event index list for Step 2.
	step2Events := make([]salesPhaseStep2CandidateEvent, len(candidateEvents))
	for i, ce := range candidateEvents {
		step2Events[i] = salesPhaseStep2CandidateEvent{
			Index:     i,
			Date:      ce.LocalDate.Format("2006-01-02"),
			Venue:     ce.ListedVenueName,
			AdminArea: ce.AdminArea,
		}
	}

	payload := salesPhaseStep2Payload{
		Phases:          inputs,
		CandidateEvents: step2Events,
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, backoff.Permanent(toAppErr(err, "failed to marshal Step 2 payload", attrs...))
	}

	temperature := s.config.Temperature
	cfg := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: systemInstructionSalesPhaseStep2}},
		},
		Temperature:        &temperature,
		MaxOutputTokens:    maxOutputTokens,
		ResponseMIMEType:   "application/json",
		ResponseJsonSchema: salesPhaseResponseJSONSchema,
	}
	if level := thinkingLevelFromConfig(s.config.thinkingParse()); level != genai.ThinkingLevelUnspecified {
		cfg.ThinkingConfig = &genai.ThinkingConfig{ThinkingLevel: level}
	}

	stepAttrs := make([]slog.Attr, 0, len(attrs)+1)
	stepAttrs = append(stepAttrs, attrs...)
	stepAttrs = append(stepAttrs, slog.String("step", "2_parse"))

	rawText, err := s.generateSingle(ctx, s.config.modelParse(), string(payloadJSON), cfg, stepAttrs)
	if err != nil {
		return nil, err
	}
	if rawText == "" {
		s.logger.Warn(ctx, "SalesPhaseSearcher: Step 2 returned empty text", stepAttrs...)
		return nil, nil
	}

	return parseSalesPhaseStep2Response(rawText, xmlPhases, seriesID, sourceURL, candidateEvents, attrs)
}

// generateSingle fires a single Gemini call with bounded exponential-backoff
// retry. It returns the concatenated text of the first candidate.
// ("", nil) is returned on transient exhaustion (the caller should treat it as
// an empty result).
func (s *SalesPhaseSearcher) generateSingle(
	ctx context.Context,
	modelName string,
	prompt string,
	cfg *genai.GenerateContentConfig,
	attrs []slog.Attr,
) (string, error) {
	bo := &backoff.ExponentialBackOff{
		InitialInterval: 1 * time.Second,
		Multiplier:      2.0,
		MaxInterval:     60 * time.Second,
	}

	var sawPermanent bool
	rawText, err := backoff.Retry(ctx, func() (string, error) {
		reqCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), geminiCallTimeout)
		defer cancel()

		resp, err := s.client.Models.GenerateContent(reqCtx, modelName, genai.Text(prompt), cfg)
		if err != nil {
			if !isRetryable(err) {
				sawPermanent = true
				return "", backoff.Permanent(toAppErr(err, "SalesPhaseSearcher Gemini call failed", attrs...))
			}
			return "", err
		}
		if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
			return "", nil
		}
		candidate := resp.Candidates[0]
		if candidate.FinishReason != genai.FinishReasonStop && candidate.FinishReason != "" {
			return "", fmt.Errorf("non-STOP finish_reason: %s", candidate.FinishReason)
		}
		var b strings.Builder
		for _, p := range candidate.Content.Parts {
			if p == nil || p.Thought || p.Text == "" {
				continue
			}
			b.WriteString(p.Text)
		}
		return b.String(), nil
	}, backoff.WithBackOff(bo), backoff.WithMaxTries(3))

	if err != nil {
		if sawPermanent || ctx.Err() != nil {
			return "", toAppErr(err, "SalesPhaseSearcher: Gemini API call failed", attrs...)
		}
		// Transient exhaustion — degrade gracefully.
		s.logger.Warn(ctx, "SalesPhaseSearcher: exhausted retries, returning empty result",
			append(attrs, slog.String("error", err.Error()))...)
		return "", nil
	}
	return rawText, nil
}

// ----- parsing helpers -----

// parseSalesPhaseStep1Envelope parses the verbatim XML envelope returned by
// Step 1 into a flat list of salesPhaseXML structs. On parse failure it
// returns nil, nil (degrade gracefully).
func parseSalesPhaseStep1Envelope(raw string) (salesPhaseStep1Envelope, []salesPhaseXML) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return salesPhaseStep1Envelope{}, nil
	}

	// Strip any Markdown code-fence wrapping the model may add.
	if strings.Contains(raw, "```") {
		for p := range strings.SplitSeq(raw, "```") {
			p = strings.TrimSpace(p)
			if after, ok := strings.CutPrefix(p, "xml"); ok {
				raw = strings.TrimSpace(after)
				break
			}
			if strings.HasPrefix(p, "<") {
				raw = p
				break
			}
		}
	}

	// Locate the <extracted> block (the model sometimes prepends prose).
	if start := strings.Index(raw, "<extracted>"); start >= 0 {
		raw = raw[start:]
	}
	if !strings.HasPrefix(raw, "<extracted>") {
		return salesPhaseStep1Envelope{}, nil
	}

	// Parse <source_url> separately (it sits at the top level, not inside
	// <phase> children, so xml.Unmarshal into a flat struct handles it).
	var envelope salesPhaseStep1Envelope
	if err := unmarshalSalesPhaseXML(raw, &envelope); err != nil {
		return salesPhaseStep1Envelope{}, nil
	}
	return envelope, envelope.Phases
}

// parseSalesPhaseStep2Response unmarshals the Step 2 JSON, matches each
// output record back to its input XML phase by output_index, resolves
// covered-event IDs from the index list, and assembles the final
// []*entity.SalesPhaseCandidate.
//
// Candidates are dropped when:
//   - apply_start is empty or unparseable (persistence guard).
//   - covered_event_indices is empty after resolution (no covered events guard).
func parseSalesPhaseStep2Response(
	rawJSON string,
	xmlPhases []salesPhaseXML,
	seriesID string,
	sourceURL string,
	candidateEvents []*entity.SalesPhaseCandidateEvent,
	attrs []slog.Attr,
) ([]*entity.SalesPhaseCandidate, error) {
	// Strip Markdown fences if present.
	text := strings.TrimSpace(rawJSON)
	if strings.Contains(text, "```") {
		for p := range strings.SplitSeq(text, "```") {
			p = strings.TrimSpace(p)
			if after, ok := strings.CutPrefix(p, "json"); ok {
				text = strings.TrimSpace(after)
				break
			}
			if strings.HasPrefix(p, "{") {
				text = p
				break
			}
		}
	}

	if text == "" || text == "{}" {
		return nil, nil
	}
	if !json.Valid([]byte(text)) {
		return nil, backoff.Permanent(errors.New("SalesPhaseSearcher: Step 2 returned invalid JSON"))
	}

	var resp salesPhaseStep2Response
	if err := json.Unmarshal([]byte(text), &resp); err != nil {
		return nil, backoff.Permanent(toAppErr(err, "SalesPhaseSearcher: failed to unmarshal Step 2 response", attrs...))
	}

	// Build a lookup from output_index → coerced phase.
	byIndex := make(map[int]salesPhaseStep2OutputPhase, len(resp.Phases))
	for _, p := range resp.Phases {
		if p.OutputIndex < 0 || p.OutputIndex >= len(xmlPhases) {
			continue
		}
		byIndex[p.OutputIndex] = p
	}

	var candidates []*entity.SalesPhaseCandidate
	for i, xp := range xmlPhases {
		coerced, ok := byIndex[i]
		if !ok {
			// Step 2 omitted this phase — skip.
			continue
		}

		// Parse the coerced apply_start (required for persistence).
		applyStart := parseRFC3339OrZero(coerced.ApplyStart)
		if applyStart.IsZero() {
			// Persistence guard: drop when apply_start is not resolvable.
			continue
		}

		// Resolve covered event IDs from indices.
		coveredEventIDs := resolveCoveredEvents(coerced.CoveredEventIndices, candidateEvents)
		if len(coveredEventIDs) == 0 {
			// Persistence guard: drop when no covered events resolved.
			continue
		}

		// Determine anchor event ID = earliest covered event.
		anchorEventID := earliestEventID(coerced.CoveredEventIndices, candidateEvents)

		c := &entity.SalesPhaseCandidate{
			SeriesID:            seriesID,
			CoveredEventIDs:     coveredEventIDs,
			AnchorEventID:       anchorEventID,
			Method:              parseSalesMethod(xp.Method),
			Channel:             parseSalesChannel(xp.Channel),
			ProviderName:        strings.TrimSpace(xp.ProviderName),
			Sequence:            parseSequence(xp.Sequence),
			ApplyStartTime:      applyStart,
			ApplyEndTime:        parseRFC3339OrZero(coerced.ApplyEnd),
			LotteryResultTime:   parseRFC3339OrZero(coerced.LotteryResult),
			PaymentDeadlineTime: parseRFC3339OrZero(coerced.PaymentDeadline),
			URL:                 strings.TrimSpace(xp.URL),
			SourceURL:           sourceURL,
		}
		candidates = append(candidates, c)
	}
	return candidates, nil
}

// resolveCoveredEvents maps Step 2 event indices back to event IDs.
// When the indices slice is empty, it returns all candidate event IDs (the
// phase is series-wide). Indices out of range are silently skipped.
func resolveCoveredEvents(indices []int, candidates []*entity.SalesPhaseCandidateEvent) []string {
	if len(indices) == 0 {
		// Empty indices = the phase covers all candidate events.
		ids := make([]string, 0, len(candidates))
		for _, ce := range candidates {
			ids = append(ids, ce.EventID)
		}
		return ids
	}
	seen := make(map[string]struct{}, len(indices))
	var ids []string
	for _, idx := range indices {
		if idx < 0 || idx >= len(candidates) {
			continue
		}
		id := candidates[idx].EventID
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

// earliestEventID returns the event ID of the candidate with the earliest
// LocalDate among the given indices. When indices is empty, it returns the
// first candidate's ID. Returns "" when candidates is empty.
func earliestEventID(indices []int, candidates []*entity.SalesPhaseCandidateEvent) string {
	if len(candidates) == 0 {
		return ""
	}
	checkIndices := indices
	if len(checkIndices) == 0 {
		// All candidates.
		checkIndices = make([]int, len(candidates))
		for i := range candidates {
			checkIndices[i] = i
		}
	}
	var earliest *entity.SalesPhaseCandidateEvent
	for _, idx := range checkIndices {
		if idx < 0 || idx >= len(candidates) {
			continue
		}
		ce := candidates[idx]
		if earliest == nil || ce.LocalDate.Before(earliest.LocalDate) {
			earliest = ce
		}
	}
	if earliest == nil {
		return ""
	}
	return earliest.EventID
}

// parseRFC3339OrZero parses an RFC 3339 string, returning time.Time{} on
// failure. Empty or "null" inputs return time.Time{} without logging.
func parseRFC3339OrZero(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "null") {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// parseSalesMethod maps a verbatim Japanese method string to the typed
// SalesMethod constant. Values align to the proto enum.
func parseSalesMethod(raw string) entity.SalesMethod {
	switch strings.TrimSpace(raw) {
	case "抽選":
		return entity.SalesMethodLottery
	case "先着":
		return entity.SalesMethodFirstCome
	default:
		return entity.SalesMethodUnspecified
	}
}

// parseSalesChannel maps a verbatim Japanese channel string to the typed
// SalesChannel constant. Values align to the proto enum.
//
// Note: concrete play-guide provider names (e+, ローチケ, チケットぴあ, etc.)
// are NOT channel values — they all map to SalesChannelPlayguide. The verbatim
// provider name is stored separately in SalesPhaseCandidate.ProviderName via
// the salesPhaseXML.ProviderName field, which is extracted verbatim by Step 1.
func parseSalesChannel(raw string) entity.SalesChannel {
	switch strings.TrimSpace(raw) {
	case "ファンクラブ":
		return entity.SalesChannelFanClub
	case "公式":
		return entity.SalesChannelOfficial
	case "プレイガイド":
		return entity.SalesChannelPlayguide
	case "クレジットカード":
		return entity.SalesChannelCreditCard
	case "携帯キャリア":
		return entity.SalesChannelMobileCarrier
	case "一般":
		return entity.SalesChannelGeneral
	default:
		return entity.SalesChannelUnspecified
	}
}

// parseSequence converts the verbatim sequence string to an integer.
// Returns 0 on failure or when the value is negative.
func parseSequence(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
