package mail

import (
	"context"
	"log/slog"
	"net/url"
	"strings"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// signInLinksTag tags sign-in link emails in Postmark's activity log.
const signInLinksTag = "organizer-sign-in-links"

// signInLinksSubject is the subject of the sign-in link email.
const signInLinksSubject = "Liverty Music オーガナイザーコンソールのサインインリンク"

// PostmarkSignInLinkSender implements [entity.SignInLinkSender] with
// Postmark's transactional message stream.
type PostmarkSignInLinkSender struct {
	postmarkClient
	// consoleBaseURL is the organizer console origin, e.g.
	// https://organizer.liverty-music.app.
	consoleBaseURL string
}

// Compile-time interface compliance check.
var _ entity.SignInLinkSender = (*PostmarkSignInLinkSender)(nil)

// NewPostmarkSignInLinkSender creates a sender for the given server token,
// From address and message stream whose links open the organizer console at
// consoleBaseURL.
func NewPostmarkSignInLinkSender(consoleBaseURL, serverToken, fromAddress, messageStream string, logger *logging.Logger) *PostmarkSignInLinkSender {
	return newPostmarkSignInLinkSender(consoleBaseURL, postmarkAPIURL, serverToken, fromAddress, messageStream, logger)
}

func newPostmarkSignInLinkSender(consoleBaseURL, apiURL, serverToken, fromAddress, messageStream string, logger *logging.Logger) *PostmarkSignInLinkSender {
	return &PostmarkSignInLinkSender{
		postmarkClient: newPostmarkClient(apiURL, serverToken, fromAddress, messageStream, logger),
		consoleBaseURL: strings.TrimRight(consoleBaseURL, "/"),
	}
}

// SendSignInLinks implements [entity.SignInLinkSender]. Any failure to hand
// the email to Postmark fails with Internal.
func (s *PostmarkSignInLinkSender) SendSignInLinks(ctx context.Context, to string, organizers []*entity.Organizer) error {
	attrs := slog.Int("organizer_count", len(organizers))
	if to == "" || len(organizers) == 0 {
		return apperr.New(codes.Internal, "the address and the organizers must not be empty", attrs)
	}
	body := signInLinksBody(s.consoleBaseURL, organizers)
	if err := s.post(ctx, to, signInLinksSubject, body, signInLinksTag, nil, attrs); err != nil {
		return apperr.Wrap(err, codes.Internal, "sign-in links cannot be sent", attrs)
	}
	return nil
}

// signInLinksBody renders the Japanese text of the sign-in link email: each
// Organizer by name with its console link naming its Organization by short
// name, and a note that the email was requested from the organizer console
// and can be ignored.
func signInLinksBody(consoleBaseURL string, organizers []*entity.Organizer) string {
	var b strings.Builder
	b.WriteString("Liverty Music オーガナイザーコンソールのサインインリンクをお送りします。\n")
	b.WriteString("サインインするオーガナイザーのリンクを開き、このメールアドレスを入力してパスキーでサインインしてください。\n\n")
	for _, o := range organizers {
		b.WriteString(o.Name)
		b.WriteString("\n")
		b.WriteString(signInLink(consoleBaseURL, o.ShortName))
		b.WriteString("\n\n")
	}
	b.WriteString("このメールは、オーガナイザーコンソールでサインインリンクの送信がリクエストされたため送信しています。")
	b.WriteString("お心当たりがない場合は、このメールを無視してください。\n\n")
	b.WriteString("Liverty Music\n")
	return b.String()
}

// signInLink returns the organizer console link that names the Organizer's
// Organization by its short name: <consoleBaseURL>/?org=<short name>.
func signInLink(consoleBaseURL, shortName string) string {
	return consoleBaseURL + "/?org=" + url.QueryEscape(shortName)
}

// NoopSignInLinkSender is used when no Postmark token is configured (local
// development): every call fails with Internal.
type NoopSignInLinkSender struct {
	logger *logging.Logger
}

// Compile-time interface compliance check.
var _ entity.SignInLinkSender = (*NoopSignInLinkSender)(nil)

// NewNoopSignInLinkSender creates a NoopSignInLinkSender.
func NewNoopSignInLinkSender(logger *logging.Logger) *NoopSignInLinkSender {
	return &NoopSignInLinkSender{logger: logger}
}

// SendSignInLinks returns Internal because no Postmark token is set.
func (s *NoopSignInLinkSender) SendSignInLinks(ctx context.Context, _ string, organizers []*entity.Organizer) error {
	s.logger.Warn(ctx, "sign-in links skipped: POSTMARK_SERVER_TOKEN is not configured",
		slog.Int("organizer_count", len(organizers)))
	return apperr.New(codes.Internal, "email is not configured")
}
