package gemini

import (
	"context"
	"errors"

	"google.golang.org/genai"

	"github.com/liverty-music/backend/internal/infrastructure/analytics/posthog"
)

// GenerationReporter receives one report per request a searcher sends to the
// model, retries and failures included (posthog.GenerationReporter in
// production). Reporting never changes what a search returns.
type GenerationReporter interface {
	ReportGeneration(ctx context.Context, g posthog.LLMGeneration)
}

// Error kinds of a generation (posthog.LLMGeneration.ErrorKind). They are
// bounded so PostHog can group failures by them.
const (
	errorKindTransient        = "transient"
	errorKindPermanent        = "permanent"
	errorKindInvalidRequest   = "invalid_request"
	errorKindCancelled        = "cancelled"
	errorKindDeadlineExceeded = "deadline_exceeded"
	errorKindNoCandidates     = "no_candidates"
	errorKindTooManyToolCalls = "too_many_tool_calls"
	errorKindIncomplete       = "incomplete"
	errorKindNoText           = "no_text"
	errorKindInvalidJSON      = "invalid_json"
)

// callErrorKind classifies a failed GenerateContent call. ctx is the caller's
// context: its cancellation decides over the call's own error.
func callErrorKind(ctx context.Context, err error) string {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded):
		return errorKindDeadlineExceeded
	case ctx.Err() != nil || errors.Is(err, context.Canceled):
		return errorKindCancelled
	case isInvalidRequest(err):
		return errorKindInvalidRequest
	case isRetryable(err):
		return errorKindTransient
	default:
		return errorKindPermanent
	}
}

// fillFromResponse copies the usage and grounding details of resp into g: the
// model release, token counts by kind, the block reason, the finish reason,
// the Google Search queries (from groundingMetadata, or from the server-side
// tool calls when it is absent), the cited sources and each page read with URL
// context. A nil resp leaves g unchanged.
func fillFromResponse(g *posthog.LLMGeneration, resp *genai.GenerateContentResponse) {
	if resp == nil {
		return
	}
	g.ModelVersion = resp.ModelVersion
	if u := resp.UsageMetadata; u != nil {
		g.Usage = posthog.LLMUsage{
			PromptTokens:     int(u.PromptTokenCount),
			CandidatesTokens: int(u.CandidatesTokenCount),
			ThinkingTokens:   int(u.ThoughtsTokenCount),
			ToolUseTokens:    int(u.ToolUsePromptTokenCount),
			CachedTokens:     int(u.CachedContentTokenCount),
		}
	}
	if pf := resp.PromptFeedback; pf != nil {
		g.BlockReason = string(pf.BlockReason)
	}
	if len(resp.Candidates) == 0 || resp.Candidates[0] == nil {
		return
	}
	c := resp.Candidates[0]
	g.FinishReason = string(c.FinishReason)
	g.FinishMessage = c.FinishMessage

	var toolQueries []string
	if c.Content != nil {
		for _, p := range c.Content.Parts {
			if p != nil && p.ToolCall != nil && p.ToolCall.ToolType == genai.ToolTypeGoogleSearchWeb {
				toolQueries = append(toolQueries, searchQueriesOf(p.ToolCall.Args)...)
			}
		}
	}
	var queries []string
	if gm := c.GroundingMetadata; gm != nil {
		queries = gm.WebSearchQueries
		for _, ch := range gm.GroundingChunks {
			if ch != nil && ch.Web != nil {
				g.GroundingURLs = append(g.GroundingURLs, ch.Web.URI)
			}
		}
	}
	if len(queries) == 0 {
		queries = toolQueries
	}
	g.WebSearchQueries = queries
	if um := c.URLContextMetadata; um != nil {
		for _, m := range um.URLMetadata {
			if m != nil {
				g.URLContext = append(g.URLContext, posthog.URLContextResult{URL: m.RetrievedURL, Status: string(m.URLRetrievalStatus)})
			}
		}
	}
}

// reportAll sends each generation to reporter, which may be nil.
func reportAll(ctx context.Context, reporter GenerationReporter, gens []posthog.LLMGeneration) {
	if reporter == nil {
		return
	}
	for _, g := range gens {
		reporter.ReportGeneration(ctx, g)
	}
}
