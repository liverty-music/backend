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

ゴール: 指定のアーティストのツアーで販売されるグッズ情報が掲載された公式ページのURLを返すこと。返すのは1つだけ。

厳守ルール:
1. 対象は「公式サイト」または「アーティスト公式SNSアカウントの投稿」のみ。非公式・転売・まとめ・ファンサイトは絶対に採用しない。
2. 指定ツアーで販売されるグッズ情報が公式から発表されている場合、その販売グッズが「画像付きで」掲載された公式ページのURLを返す。販売グッズの画像が掲載されていることを絶対条件とする。
3. 可能なら、ツアーのグッズ内容を告知する詳細ページ（例: 公式サイトの news/detail のグッズ告知ページ）を優先する。ただし正確なURLに確証が持てない場合は、確実に実在する公式グッズストアのトップ（例: store.plusmember.jp/<artist>/ のような公式EC）を返してよい。
4. 架空のカテゴリID・記事IDなどを含む deep link を推測で組み立てて返してはならない。正確なIDに自信が無ければ、IDを含まない上位の安定したURL（公式ストアのトップ等）を返すこと。
5. 公式SNSの投稿（例: X/Twitter）が販売グッズの画像を含み最も情報量が多い場合は、その投稿URLを返してよい。
6. 公式の確かなページが見つからない場合は、推測せず必ず「NONE」とだけ出力する。
7. 出力は URL 1つ、または「NONE」のみ。説明・前置き・引用・装飾は一切付けない。`

	// merchPromptTemplate is filled with artist name and tour title.
	merchPromptTemplate = `アーティスト名: %s
ツアータイトル: %s

上記アーティスト/ツアーについて、販売グッズが画像付きで掲載された公式ページのURLを1つだけ出力してください。グッズ告知の詳細ページを優先しますが、正確なURLに確証が無ければ公式グッズストアのトップURLで構いません。架空のID付き deep link は出力しないでください。見つからなければ NONE と出力してください。`
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
		// GoogleSearch grounds discovery; URLContext lets the model fetch the
		// candidate pages it finds so it can verify the page actually exists and
		// shows the for-sale goods with images (instead of guessing a deep link).
		Tools: []*genai.Tool{
			{GoogleSearch: &genai.GoogleSearch{}},
			{URLContext: &genai.URLContext{}},
		},
		Temperature:     &temperature,
		MaxOutputTokens: merchMaxOutputTokens,
	}
	if level := thinkingLevelFromConfig(s.config.ThinkingLevel); level != genai.ThinkingLevelUnspecified {
		cfg.ThinkingConfig = &genai.ThinkingConfig{ThinkingLevel: level}
	}

	prompt := fmt.Sprintf(merchPromptTemplate, artistName, seriesTitle)

	raw, meta, err := s.generate(ctx, prompt, cfg, attrs)
	if err != nil {
		return "", err
	}

	url := parseMerchURL(raw)
	// Note: existence of the resolved URL is verified downstream by the use
	// case's HTTP liveness probe (SSRF-hardened). We intentionally do NOT gate
	// on URLContext metadata here: when GoogleSearch grounding is involved the
	// fetched-URL metadata reports opaque vertexaisearch redirect wrappers, not
	// the real destination, so it cannot be matched against the resolved URL.
	if s.logger != nil {
		// Emit the grounding / URL-context / usage metadata so the resolution is
		// auditable: which web queries ran, which URLs were grounded, which were
		// fetched and with what retrieval status, and the token cost.
		s.logger.Info(ctx, "merch search complete", append(attrs,
			slog.Bool("found", url != ""),
			slog.String("resolved_url", url),
			slog.String("finish_reason", meta.FinishReason),
			slog.Any("web_search_queries", meta.WebSearchQueries),
			slog.Any("grounding_urls", meta.GroundingURLs),
			slog.Any("url_context_fetched", meta.URLContextFetched),
			slog.Group("usage_tokens",
				slog.Int("prompt", int(meta.PromptTokens)),
				slog.Int("candidates", int(meta.CandidatesTokens)),
				slog.Int("thoughts", int(meta.ThoughtsTokens)),
				slog.Int("tool_use", int(meta.ToolUseTokens)),
				slog.Int("total", int(meta.TotalTokens)),
			),
		)...)
	}
	return url, nil
}

// merchCallMeta captures the grounding / URL-context / usage metadata of a
// single GenerateContent response for observability and debugging.
type merchCallMeta struct {
	ResponseID        string
	FinishReason      string
	PromptTokens      int32
	CandidatesTokens  int32
	ThoughtsTokens    int32
	ToolUseTokens     int32
	TotalTokens       int32
	WebSearchQueries  []string
	GroundingURLs     []string
	URLContextFetched []string // "url (status)" pairs, for logging/audit
}

// extractMerchMeta pulls the auditable metadata off a GenerateContent response.
func extractMerchMeta(resp *genai.GenerateContentResponse) merchCallMeta {
	var m merchCallMeta
	if resp == nil {
		return m
	}
	m.ResponseID = resp.ResponseID
	if u := resp.UsageMetadata; u != nil {
		m.PromptTokens = u.PromptTokenCount
		m.CandidatesTokens = u.CandidatesTokenCount
		m.ThoughtsTokens = u.ThoughtsTokenCount
		m.ToolUseTokens = u.ToolUsePromptTokenCount
		m.TotalTokens = u.TotalTokenCount
	}
	if len(resp.Candidates) == 0 {
		return m
	}
	c := resp.Candidates[0]
	m.FinishReason = string(c.FinishReason)
	if g := c.GroundingMetadata; g != nil {
		m.WebSearchQueries = g.WebSearchQueries
		for _, ch := range g.GroundingChunks {
			if ch != nil && ch.Web != nil && ch.Web.URI != "" {
				m.GroundingURLs = append(m.GroundingURLs, ch.Web.URI)
			}
		}
	}
	if c.URLContextMetadata != nil {
		for _, um := range c.URLContextMetadata.URLMetadata {
			if um == nil {
				continue
			}
			m.URLContextFetched = append(m.URLContextFetched, um.RetrievedURL+" ("+string(um.URLRetrievalStatus)+")")
		}
	}
	return m
}

// generate runs a single grounded GenerateContent call with bounded retries on
// transient errors, returning the concatenated text of the first candidate plus
// the response metadata of the final attempt.
func (s *MerchSearcher) generate(ctx context.Context, prompt string, cfg *genai.GenerateContentConfig, attrs []slog.Attr) (string, merchCallMeta, error) {
	var meta merchCallMeta
	bo := &backoff.ExponentialBackOff{
		InitialInterval:     time.Second,
		RandomizationFactor: 0.5,
		Multiplier:          2.0,
		MaxInterval:         30 * time.Second,
	}

	raw, err := backoff.Retry(ctx, func() (string, error) {
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
		// Capture metadata from every attempt; the final (successful) attempt wins.
		meta = extractMerchMeta(resp)
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
	return raw, meta, err
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
