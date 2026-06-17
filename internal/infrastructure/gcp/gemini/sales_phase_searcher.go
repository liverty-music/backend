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

アーティストの指定シリーズについて、公式サイトおよびチケット販売会社のページから、ユーザーが「今後申し込める」チケット販売スケジュールを抽出することがゴールです。ユーザー(ファン)が見逃したくないのは、これから受付が始まる先行・先着・一般発売です。すでに申込受付が終了した販売(申込締切が本日より前のもの)は価値がないため対象外とし、出力しないでください。

販売フェーズはシリーズ(ツアー)全体に対する1つの販売機会として扱います。どの個別公演を対象とするか(対象公演日)は抽出不要です。販売条件(チャネル・受付期間など)が異なる販売は、それぞれ別の <phase> として出力してください。

以下の出力フォーマットに従い、販売フェーズごとに <phase> タグでまとめてください。

<extracted>
  <source_url>https://www.example.com/ticket</source_url>
  <phase>
    <method>抽選</method>
    <channel>ファンクラブ</channel>
    <provider_name></provider_name>
    <sequence>0</sequence>
    <apply_start>2026年7月1日 10:00</apply_start>
    <apply_end>2026年7月10日 23:59</apply_end>
    <lottery_result></lottery_result>
    <payment_deadline></payment_deadline>
    <url>https://fc.example/entry</url>
  </phase>
  <phase>
    <method>先着</method>
    <channel>一般</channel>
    <provider_name>チケットぴあ</provider_name>
    <sequence>0</sequence>
    <apply_start>2026年8月1日 10:00</apply_start>
    <apply_end></apply_end>
    <lottery_result></lottery_result>
    <payment_deadline></payment_deadline>
    <url>https://t.pia.jp/example</url>
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
- 対象期間: ユーザープロンプトで与えられる「本日」を基準とし、申込締切が本日より前の販売(受付終了済み)は出力しないこと。これから受付が始まる、または現在受付中で締切が本日以降の販売のみを対象とする。特に、告知されたばかりの今後の一般発売・追加先行を見落とさないこと。
- 情報が確認できない項目はタグを空欄にすること。推測や補完は一切禁止。
- 余計なテキストは含めず、XMLのみをレスポンスに含めること。
`

	// promptTemplateSalesPhaseStep1 is the per-call user prompt template.
	// Placeholders: today (JST date), artist name, series title.
	promptTemplateSalesPhaseStep1 = `本日: %s
アーティスト名: %s
シリーズ名: %s

このシリーズについて、本日以降に申し込めるチケット販売スケジュールのみを抽出してください。申込締切が本日より前の販売(受付終了済み)は出力しないこと。これから受付が始まる先行・先着・一般発売、および現在受付中で締切が本日以降の販売を対象とし、今後の一般発売・追加先行を見落とさないでください。`

	// systemInstructionSalesPhaseStep2 instructs Step 2 to perform JSON
	// coercion only — dates and times are normalised from verbatim Japanese
	// strings to RFC 3339; no invented values are permitted.
	systemInstructionSalesPhaseStep2 = `You are a data-transformation agent for a music-fan information service.

You receive a JSON array of raw ticket-sales phase records extracted from Japanese web pages. For each phase record:
1. Coerce date/time strings to RFC 3339 (Asia/Tokyo = +09:00). Emit "" for any field you cannot coerce unambiguously.
2. Return output_index unchanged (the join key the caller uses to align your output with the input).

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
}

// unmarshalSalesPhaseXML parses a raw <extracted>…</extracted> XML string
// into a salesPhaseStep1Envelope. Returns an error when the XML is malformed.
func unmarshalSalesPhaseXML(raw string, out *salesPhaseStep1Envelope) error {
	return xml.Unmarshal([]byte(raw), out)
}

// ----- Step 2 JSON types -----

// salesPhaseStep2Input is sent to Step 2 for one extracted phase record.
type salesPhaseStep2Input struct {
	OutputIndex     int    `json:"output_index"`
	ApplyStart      string `json:"apply_start"`
	ApplyEnd        string `json:"apply_end"`
	LotteryResult   string `json:"lottery_result"`
	PaymentDeadline string `json:"payment_deadline"`
}

// salesPhaseStep2Payload is the top-level payload sent to Step 2.
type salesPhaseStep2Payload struct {
	Phases []salesPhaseStep2Input `json:"phases"`
}

// salesPhaseStep2OutputPhase is one coerced phase from Step 2.
type salesPhaseStep2OutputPhase struct {
	OutputIndex     int    `json:"output_index"`
	ApplyStart      string `json:"apply_start"`
	ApplyEnd        string `json:"apply_end"`
	LotteryResult   string `json:"lottery_result"`
	PaymentDeadline string `json:"payment_deadline"`
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
				},
				"required": []string{"output_index", "apply_start", "apply_end", "lottery_result", "payment_deadline"},
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

	// ===== Step 2: JSON coercion (verbatim Japanese dates → RFC 3339) =====
	candidates, err := s.runStep2(ctx, seriesID, envelope.SourceURL, xmlPhases, attrs)
	if err != nil {
		return nil, err
	}

	// Deterministic "upcoming only" backstop: the prompt asks the model to skip
	// already-closed sales, but the model is not fully reliable (it can still
	// surface a past round). Drop any phase whose application window has
	// definitively ended (apply_end known and before now). Phases with an
	// unknown end are kept (conservative — they may still be open), and the
	// downstream reminder scan's first-sight guard suppresses stale milestones.
	candidates = filterUpcomingPhases(candidates, time.Now().UTC())
	s.logger.Info(ctx, "SalesPhaseSearcher: extraction complete",
		append(attrs, slog.Int("candidates_count", len(candidates)))...)
	return candidates, nil
}

// filterUpcomingPhases drops phases whose application window has definitively
// closed (ApplyEndTime is known and before now), keeping upcoming, currently-
// open, and unknown-end phases.
func filterUpcomingPhases(candidates []*entity.SalesPhaseCandidate, now time.Time) []*entity.SalesPhaseCandidate {
	out := candidates[:0]
	for _, c := range candidates {
		if c == nil {
			continue
		}
		if !c.ApplyEndTime.IsZero() && c.ApplyEndTime.Before(now) {
			continue // already closed — not actionable
		}
		out = append(out, c)
	}
	return out
}

// runStep1 fires the grounded search call and returns the raw XML envelope
// text. Returns ("", nil) on transient exhaustion (degrade gracefully).
func (s *SalesPhaseSearcher) runStep1(
	ctx context.Context,
	artistName, seriesTitle string,
	attrs []slog.Attr,
) (string, error) {
	now := time.Now().UTC().Truncate(time.Second)
	// Anchor the model on "today" (JST) so it excludes already-closed sales and
	// surfaces upcoming / future-scheduled phases. JST has no DST, so a fixed
	// +09:00 zone is exact and avoids a tzdata dependency.
	jst := time.FixedZone("JST", 9*60*60)
	today := now.In(jst).Format("2006年1月2日")
	prompt := fmt.Sprintf(promptTemplateSalesPhaseStep1, today, artistName, seriesTitle)
	temperature := s.config.Temperature

	// TODO(grounding): GoogleSearch is NOT firing for this searcher — live runs
	// show usage.tool_use=0 and grounding.fired=false for BOTH gemini-3.1-flash-
	// lite AND gemini-3.5-flash, so extraction is currently from model memory,
	// not live search. Removing TimeRangeFilter did NOT restore grounding, so it
	// is not the cause; root cause is at the GoogleSearch tool / SDK / model
	// level (likely affects the concert searcher too). TimeRangeFilter is kept
	// for source freshness once grounding is fixed.
	searchTool := &genai.Tool{
		GoogleSearch: &genai.GoogleSearch{
			TimeRangeFilter: &genai.Interval{
				StartTime: now.AddDate(0, -6, 0),
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
		// NOTE: ResponseLogprobs is unsupported on gemini-3.1-flash-lite
		// (API 400 "Logprobs is not enabled for this model"), so avg_logprobs
		// stays 0 — no confidence signal available with the lite model.
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

// runStep2 builds the JSON payload from the Step 1 XML phases, fires the
// structured-output Step 2 call (verbatim Japanese dates → RFC 3339), then
// assembles the final []*entity.SalesPhaseCandidate.
func (s *SalesPhaseSearcher) runStep2(
	ctx context.Context,
	seriesID string,
	sourceURL string,
	xmlPhases []salesPhaseXML,
	attrs []slog.Attr,
) ([]*entity.SalesPhaseCandidate, error) {
	// Build Step 2 inputs.
	inputs := make([]salesPhaseStep2Input, len(xmlPhases))
	for i, xp := range xmlPhases {
		inputs[i] = salesPhaseStep2Input{
			OutputIndex:     i,
			ApplyStart:      xp.ApplyStart,
			ApplyEnd:        xp.ApplyEnd,
			LotteryResult:   xp.LotteryResult,
			PaymentDeadline: xp.PaymentDeadline,
		}
	}

	payload := salesPhaseStep2Payload{
		Phases: inputs,
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

	return parseSalesPhaseStep2Response(rawText, xmlPhases, seriesID, sourceURL, attrs)
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
		// Log the FULL Gemini response metadata for tuning/observability:
		// token usage, finish reason/message/logprobs, grounding (search
		// queries, source URLs + titles, whether grounding fired, rendered
		// supports), and URL-context retrieval. Emitted for every response,
		// including empty/non-STOP ones.
		s.logResponseMetadata(ctx, modelName, resp, attrs)

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

// logResponseMetadata logs the complete metadata of a Gemini response for
// tuning and observability: token usage, finish reason / message / avg
// logprobs, grounding (search queries, source URLs + titles, whether grounding
// fired, rendered support count), and URL-context retrieval results. It is
// best-effort — nil sub-objects are logged as their zero/empty state so a line
// is always emitted, one per Gemini call (Step 1 grounded and Step 2 parse).
func (s *SalesPhaseSearcher) logResponseMetadata(
	ctx context.Context,
	modelName string,
	resp *genai.GenerateContentResponse,
	attrs []slog.Attr,
) {
	if resp == nil {
		return
	}
	fields := make([]slog.Attr, 0, len(attrs)+10)
	fields = append(fields, attrs...)
	fields = append(fields,
		slog.String("model", modelName),
		slog.String("response_id", resp.ResponseID),
		slog.String("model_version", resp.ModelVersion),
	)

	if u := resp.UsageMetadata; u != nil {
		fields = append(fields, slog.Group("usage",
			slog.Int("prompt", int(u.PromptTokenCount)),
			slog.Int("candidates", int(u.CandidatesTokenCount)),
			slog.Int("thinking", int(u.ThoughtsTokenCount)),
			slog.Int("tool_use", int(u.ToolUsePromptTokenCount)),
			slog.Int("total", int(u.TotalTokenCount)),
		))
	}

	if len(resp.Candidates) == 0 {
		fields = append(fields, slog.Bool("has_candidate", false))
		s.logger.Info(ctx, "SalesPhaseSearcher: gemini response metadata", fields...)
		return
	}
	c := resp.Candidates[0]
	fields = append(fields,
		slog.String("finish_reason", string(c.FinishReason)),
		slog.String("finish_message", c.FinishMessage),
		slog.Float64("avg_logprobs", c.AvgLogprobs),
	)

	if g := c.GroundingMetadata; g != nil {
		urls := make([]string, 0, len(g.GroundingChunks))
		titles := make([]string, 0, len(g.GroundingChunks))
		for _, ch := range g.GroundingChunks {
			if ch != nil && ch.Web != nil {
				urls = append(urls, ch.Web.URI)
				titles = append(titles, ch.Web.Title)
			}
		}
		rendered := 0
		for _, sup := range g.GroundingSupports {
			if sup != nil {
				rendered += len(sup.RenderedParts)
			}
		}
		fields = append(fields, slog.Group("grounding",
			slog.Bool("fired", true),
			slog.Int("search_query_count", len(g.WebSearchQueries)),
			slog.Any("search_queries", g.WebSearchQueries),
			slog.Int("source_count", len(urls)),
			slog.Any("source_urls", urls),
			slog.Any("source_titles", titles),
			slog.Int("rendered_supports", rendered),
		))
	} else {
		fields = append(fields, slog.Group("grounding", slog.Bool("fired", false)))
	}

	if uc := c.URLContextMetadata; uc != nil {
		retrieved := make([]string, 0, len(uc.URLMetadata))
		for _, um := range uc.URLMetadata {
			if um != nil {
				retrieved = append(retrieved, fmt.Sprintf("%s [%s]", um.RetrievedURL, um.URLRetrievalStatus))
			}
		}
		fields = append(fields, slog.Any("url_context_retrieved", retrieved))
	}

	s.logger.Info(ctx, "SalesPhaseSearcher: gemini response metadata", fields...)
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
// output record back to its input XML phase by output_index, and assembles the
// final series-level []*entity.SalesPhaseCandidate.
//
// A candidate is dropped when apply_start is empty or unparseable (the sole
// persistence guard — a known start is all a series-level phase requires).
func parseSalesPhaseStep2Response(
	rawJSON string,
	xmlPhases []salesPhaseXML,
	seriesID string,
	sourceURL string,
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

		// Deterministic timeline validation: enforce
		// apply_start <= apply_end <= lottery_result <= payment_deadline. Any
		// later field that precedes the running lower bound is nulled out (kept
		// as "unknown") instead of driving a wrong reminder — flash-lite
		// occasionally hallucinates inconsistent dates.
		applyEnd, lotteryResult, paymentDeadline := sanitizeTimeline(
			applyStart,
			parseRFC3339OrZero(coerced.ApplyEnd),
			parseRFC3339OrZero(coerced.LotteryResult),
			parseRFC3339OrZero(coerced.PaymentDeadline),
		)

		c := &entity.SalesPhaseCandidate{
			SeriesID:            seriesID,
			Method:              parseSalesMethod(xp.Method),
			Channel:             parseSalesChannel(xp.Channel),
			ProviderName:        strings.TrimSpace(xp.ProviderName),
			Sequence:            parseSequence(xp.Sequence),
			ApplyStartTime:      applyStart,
			ApplyEndTime:        applyEnd,
			LotteryResultTime:   lotteryResult,
			PaymentDeadlineTime: paymentDeadline,
			URL:                 strings.TrimSpace(xp.URL),
			SourceURL:           sourceURL,
		}
		candidates = append(candidates, c)
	}
	return candidates, nil
}

// sanitizeTimeline enforces the monotonic order
// apply_start <= apply_end <= lottery_result <= payment_deadline. apply_start is
// assumed valid (guarded upstream). Any later field that precedes the running
// lower bound is treated as a hallucinated / inconsistent value and nulled out
// (returned as the zero time = "unknown") rather than persisted. Zero (unknown)
// inputs pass through unchanged.
func sanitizeTimeline(start, end, result, payment time.Time) (time.Time, time.Time, time.Time) {
	lower := start
	if !end.IsZero() {
		if end.Before(lower) {
			end = time.Time{}
		} else {
			lower = end
		}
	}
	if !result.IsZero() {
		if result.Before(lower) {
			result = time.Time{}
		} else {
			lower = result
		}
	}
	if !payment.IsZero() && payment.Before(lower) {
		payment = time.Time{}
	}
	return end, result, payment
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
