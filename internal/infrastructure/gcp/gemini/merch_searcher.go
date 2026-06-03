package gemini

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-logging/logging"
	"google.golang.org/genai"
)

// MerchConfig configures the merch-url searcher. Like the concert searcher it
// targets the Gemini API direct backend (BackendGeminiAPI) so GoogleSearch
// grounding is available; APIKey is therefore required.
type MerchConfig struct {
	// APIKey selects the Gemini API direct backend. REQUIRED.
	APIKey string
	// Model is the grounded-search model (Flash-Lite by default). REQUIRED.
	Model string
	// Temperature is the sampling temperature for the search call.
	Temperature float32
	// ThinkingLevel is the optional thinking level ("", minimal, low, medium,
	// high). A single best-URL lookup needs little thinking; "low" is plenty.
	ThinkingLevel string
}

const (
	// merchMaxOutputTokens caps the merch search response. The model is asked
	// for a single URL or the NONE sentinel, so a small budget suffices even
	// with grounding chatter.
	merchMaxOutputTokens = int32(2048)

	// merchNoneSentinel is the literal the model emits when it finds no
	// confident official source. Compared case-insensitively after trimming.
	merchNoneSentinel = "NONE"

	// merchSystemInstruction restricts the search to official sources and
	// forces a single-URL-or-NONE answer so the parser never has to judge
	// officialness — that judgement lives entirely in the prompt + grounding.
	merchSystemInstruction = `あなたは音楽ファン向けサービスのための、公式グッズ情報URLを特定するエージェントです。

ゴール: 指定アーティストの指定シリーズ（ツアー/単独公演/フェス）について、グッズ販売情報が最も充実している「単一の」URLを1つだけ返すこと。

厳守ルール:
1. 対象は「公式サイト」または「アーティスト公式SNSアカウントの投稿」のみ。非公式サイト・転売・まとめ・ファンサイトは絶対に採用しない。
2. 公式SNSの投稿（例: X/Twitter のポスト）がグッズ情報を最も多く含む場合、その投稿URLを返してよい。
3. 公式の確かな情報源が見つからない場合は、推測や当て推量で不確かなURLを返さず、必ず「NONE」とだけ出力する。
4. 出力は URL 1つ、または「NONE」のみ。説明・前置き・引用・装飾は一切付けない。`

	// merchPromptTemplate is filled with artist name and series title.
	merchPromptTemplate = `アーティスト名: %s
シリーズ名: %s

このシリーズの公式グッズ情報URLを1つだけ出力してください。見つからなければ NONE と出力してください。`
)

// merchURLPattern extracts the first http(s) URL from the model's response,
// tolerating any stray prose the grounded model adds despite the prompt.
var merchURLPattern = regexp.MustCompile(`https?://[^\s<>"')\]]+`)

// MerchSearcher resolves an official merch URL via a single grounded Gemini
// call. It implements [entity.MerchSearcher].
type MerchSearcher struct {
	client *genai.Client
	config MerchConfig
	logger *logging.Logger
}

// Compile-time interface compliance check.
var _ entity.MerchSearcher = (*MerchSearcher)(nil)

// NewMerchSearcher builds a merch searcher against the Gemini API direct
// backend. It fast-fails when APIKey or Model is empty.
func NewMerchSearcher(ctx context.Context, cfg MerchConfig, httpClient *http.Client, logger *logging.Logger) (*MerchSearcher, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("gemini.NewMerchSearcher: APIKey is empty; set GCP_GEMINI_SEARCH_API_KEY")
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("gemini.NewMerchSearcher: Model is empty; set GCP_GEMINI_MERCH_MODEL")
	}

	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		HTTPClient: httpClient,
		Backend:    genai.BackendGeminiAPI,
		APIKey:     cfg.APIKey,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create genai client: %w", err)
	}

	return &MerchSearcher{client: client, config: cfg, logger: logger}, nil
}

// SearchMerchURL returns the single richest official merch URL for the given
// artist + series, or "" when no confident official source exists. An empty
// result is a normal outcome (nil error); only a transport/API failure returns
// an error.
func (s *MerchSearcher) SearchMerchURL(ctx context.Context, artistName, seriesTitle string) (string, error) {
	attrs := []slog.Attr{
		slog.String("artist_name", artistName),
		slog.String("series_title", seriesTitle),
	}

	temperature := s.config.Temperature
	cfg := &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: merchSystemInstruction}}},
		Tools:             []*genai.Tool{{GoogleSearch: &genai.GoogleSearch{}}},
		Temperature:       &temperature,
		MaxOutputTokens:   merchMaxOutputTokens,
	}
	if level := thinkingLevelFromConfig(s.config.ThinkingLevel); level != genai.ThinkingLevelUnspecified {
		cfg.ThinkingConfig = &genai.ThinkingConfig{ThinkingLevel: level}
	}

	prompt := fmt.Sprintf(merchPromptTemplate, artistName, seriesTitle)

	raw, err := s.generate(ctx, prompt, cfg, attrs)
	if err != nil {
		return "", err
	}

	url := parseMerchURL(raw)
	if s.logger != nil {
		s.logger.Info(ctx, "merch search complete",
			append(attrs, slog.Bool("found", url != ""))...)
	}
	return url, nil
}

// generate runs a single grounded GenerateContent call with bounded retries on
// transient errors, returning the concatenated text of the first candidate.
func (s *MerchSearcher) generate(ctx context.Context, prompt string, cfg *genai.GenerateContentConfig, attrs []slog.Attr) (string, error) {
	bo := &backoff.ExponentialBackOff{
		InitialInterval:     time.Second,
		RandomizationFactor: 0.5,
		Multiplier:          2.0,
		MaxInterval:         30 * time.Second,
	}

	return backoff.Retry(ctx, func() (string, error) {
		// Detach from the parent cancel only for the timeout dimension: a real
		// SIGTERM/parent cancel still stops the retry loop between attempts,
		// but a single in-flight call gets a bounded deadline of its own.
		reqCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), geminiCallTimeout)
		defer cancel()

		resp, err := s.client.Models.GenerateContent(reqCtx, s.config.Model, genai.Text(prompt), cfg)
		if err != nil {
			if !isRetryable(err) {
				return "", backoff.Permanent(toAppErr(err, "merch search call failed", attrs...))
			}
			return "", err
		}
		if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
			// No candidate / filtered response → treat as "no source found".
			return "", nil
		}

		var b strings.Builder
		for _, p := range resp.Candidates[0].Content.Parts {
			if p == nil || p.Thought || p.Text == "" {
				continue
			}
			b.WriteString(p.Text)
		}
		return b.String(), nil
	}, backoff.WithBackOff(bo), backoff.WithMaxTries(3))
}

// parseMerchURL extracts the resolved URL from the model's raw text. It returns
// "" when the model emitted the NONE sentinel or no URL is present. Trailing
// sentence punctuation is trimmed off the matched URL.
func parseMerchURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || strings.EqualFold(trimmed, merchNoneSentinel) {
		return ""
	}
	match := merchURLPattern.FindString(trimmed)
	if match == "" {
		return ""
	}
	return strings.TrimRight(match, ".,;)]}\"'。、")
}
