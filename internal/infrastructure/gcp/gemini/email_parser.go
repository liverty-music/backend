package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"google.golang.org/genai"
)

const (
	emailParserSystemInstruction = `You are a specialized agent for extracting structured data from Japanese ticket-related emails.
You receive email text that has been shared by the user from their email app.
Extract the requested fields precisely from the email content.
DO NOT hallucinate or infer values not explicitly stated in the email.
If a field is not present in the email, return null for that field.
All dates and times must be in ISO 8601 format with timezone (e.g., 2026-04-10T23:59:00+09:00).`

	lotteryInfoPrompt = `Extract the following from this ticket lottery announcement email:
- lottery_start: Start date/time of the lottery application period (ISO 8601 with timezone)
- lottery_end: End date/time of the lottery application period (ISO 8601 with timezone)
- application_url: URL where the user can apply for the lottery

Return JSON matching this schema:
{"lottery_start": "string or null", "lottery_end": "string or null", "application_url": "string or null"}

Email text:
%s`

	lotteryResultPrompt = `Extract the following from this ticket lottery result email:
- lottery_result: "won" if the user won the lottery (当選), "lost" if the user lost (落選)
- payment_status: "paid" if payment was already completed (e.g., credit card auto-charge), "unpaid" if payment is pending
- payment_deadline: Payment due date/time if payment is pending (ISO 8601 with timezone)

Return JSON matching this schema:
{"lottery_result": "string or null", "payment_status": "string or null", "payment_deadline": "string or null"}

Email text:
%s`
)

// EmailParserConfig holds the configuration for the email parser.
type EmailParserConfig struct {
	ProjectID string
	Location  string
	ModelName string
}

// EmailParser implements entity.TicketEmailParser using Vertex AI Gemini.
type EmailParser struct {
	client *genai.Client
	model  string
	logger *logging.Logger
}

// NewEmailParser creates a new Gemini-based ticket email parser.
// The httpClient parameter allows for custom transport configuration, which is
// particularly useful for unit testing with httptest. If nil, the default transport is used.
func NewEmailParser(ctx context.Context, cfg EmailParserConfig, httpClient *http.Client, logger *logging.Logger) (*EmailParser, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:    cfg.ProjectID,
		Location:   cfg.Location,
		Backend:    genai.BackendVertexAI,
		HTTPClient: httpClient,
	})
	if err != nil {
		return nil, fmt.Errorf("create genai client: %w", err)
	}

	return &EmailParser{
		client: client,
		model:  cfg.ModelName,
		logger: logger,
	}, nil
}

// Parse extracts structured data from a ticket email body using Gemini Flash.
func (p *EmailParser) Parse(ctx context.Context, emailBody string, emailType entity.TicketEmailType) (*entity.ParsedEmailData, error) {
	var prompt string
	switch emailType {
	case entity.TicketEmailTypeLotteryInfo:
		prompt = fmt.Sprintf(lotteryInfoPrompt, emailBody)
	case entity.TicketEmailTypeLotteryResult:
		prompt = fmt.Sprintf(lotteryResultPrompt, emailBody)
	default:
		return nil, apperr.New(codes.InvalidArgument, "unsupported email type for parsing")
	}

	resp, err := p.client.Models.GenerateContent(ctx, p.model, genai.Text(prompt), &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{
			Parts: []*genai.Part{{Text: emailParserSystemInstruction}},
		},
		ResponseMIMEType: "application/json",
		Temperature:      new(float32(0.0)),
	})
	if err != nil {
		p.logger.Error(ctx, "gemini email parse failed", err,
			slog.Int("emailType", int(emailType)),
		)
		return nil, apperr.New(codes.Internal, "failed to parse ticket email with Gemini")
	}

	text := resp.Text()
	if text == "" {
		return nil, apperr.New(codes.Internal, "empty response from Gemini")
	}

	var parsed entity.ParsedEmailData
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		p.logger.Error(ctx, "failed to unmarshal gemini response", err,
			slog.String("response", text),
		)
		return nil, apperr.New(codes.Internal, "failed to parse Gemini response as JSON")
	}

	p.logger.Info(ctx, "ticket email parsed",
		slog.Int("emailType", int(emailType)),
	)
	return &parsed, nil
}
