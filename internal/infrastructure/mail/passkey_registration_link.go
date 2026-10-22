package mail

import (
	"context"
	"log/slog"
	"strings"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// passkeyRegistrationLinkTag tags passkey registration link emails in
// Postmark's activity log.
const passkeyRegistrationLinkTag = "passkey-registration-link"

// PasskeyRegistrationLinkIssuer issues an identity's passkey registration
// link at the identity provider. Implemented by the Zitadel
// PasskeyRegistrationLinkIssuer.
type PasskeyRegistrationLinkIssuer interface {
	// IssuePasskeyRegistrationLink returns the identity's verified email
	// address and a link that registers a passkey for it without a sign-in.
	// It fails with NotFound when the identity does not exist,
	// FailedPrecondition when its address is not verified (nothing is
	// issued) and Internal otherwise.
	IssuePasskeyRegistrationLink(ctx context.Context, providerUserID string) (address, link string, err error)
}

// PostmarkPasskeyRegistrationLinkSender implements
// [entity.PasskeyRegistrationLinkSender]: it issues the link at the identity
// provider and emails it through Postmark's transactional message stream. The
// link carries a one-time code, so it is never logged.
type PostmarkPasskeyRegistrationLinkSender struct {
	postmarkClient
	links PasskeyRegistrationLinkIssuer
}

// Compile-time interface compliance check.
var _ entity.PasskeyRegistrationLinkSender = (*PostmarkPasskeyRegistrationLinkSender)(nil)

// NewPostmarkPasskeyRegistrationLinkSender creates a sender for the given
// link issuer, server token, From address and message stream.
func NewPostmarkPasskeyRegistrationLinkSender(links PasskeyRegistrationLinkIssuer, serverToken, fromAddress, messageStream string, logger *logging.Logger) *PostmarkPasskeyRegistrationLinkSender {
	return newPostmarkPasskeyRegistrationLinkSender(links, postmarkAPIURL, serverToken, fromAddress, messageStream, logger)
}

func newPostmarkPasskeyRegistrationLinkSender(links PasskeyRegistrationLinkIssuer, apiURL, serverToken, fromAddress, messageStream string, logger *logging.Logger) *PostmarkPasskeyRegistrationLinkSender {
	return &PostmarkPasskeyRegistrationLinkSender{
		postmarkClient: newPostmarkClient(apiURL, serverToken, fromAddress, messageStream, logger),
		links:          links,
	}
}

// SendPasskeyRegistrationLink implements [entity.PasskeyRegistrationLinkSender].
// The email goes to the address the identity has verified, which the
// identity provider reports, not to the address stored on the User.
func (s *PostmarkPasskeyRegistrationLinkSender) SendPasskeyRegistrationLink(ctx context.Context, user *entity.User) error {
	attrs := slog.String("user_id", user.ID)
	address, link, err := s.links.IssuePasskeyRegistrationLink(ctx, user.ProviderUserID)
	if err != nil {
		return err
	}
	subject, body := passkeyRegistrationLinkEmail(emailLanguage(user.PreferredLanguage), link)
	if err := s.post(ctx, address, subject, body, passkeyRegistrationLinkTag,
		map[string]string{"user_id": user.ID}, attrs); err != nil {
		return apperr.Wrap(err, codes.Internal, "passkey registration link cannot be sent", attrs)
	}
	return nil
}

// passkeyRegistrationLinkEmail renders the subject and text of the passkey
// registration link email in lang ("ja" or "en"): the link, its 1-hour
// validity, the single-attempt rule, that earlier passkeys stay, and that it
// was requested from the sign-in help and can be ignored.
func passkeyRegistrationLinkEmail(lang, link string) (subject, body string) {
	var b strings.Builder
	if lang == "en" {
		b.WriteString("A passkey registration link was requested from the Liverty Music sign-in help.\n")
		b.WriteString("Open the link below on the device you want to sign in with to register a new passkey for your account. You can then sign in with it.\n\n")
		b.WriteString(link)
		b.WriteString("\n\n")
		b.WriteString("- The link works for 1 hour after this email was sent.\n")
		b.WriteString("- The link works for one attempt. If the passkey prompt is cancelled or fails, request a new link from the sign-in help.\n")
		b.WriteString("- Passkeys you already have keep working.\n\n")
		b.WriteString("If you did not request this, you can ignore this email. Your account does not change unless the link is opened.\n\n")
		b.WriteString("Liverty Music\n")
		return "[Liverty Music] Register a new passkey", b.String()
	}
	b.WriteString("Liverty Music のサインインヘルプから、パスキー登録リンクの送信がリクエストされました。\n")
	b.WriteString("サインインに使うデバイスで下のリンクを開くと、あなたのアカウントに新しいパスキーを登録できます。登録後はそのパスキーでサインインできます。\n\n")
	b.WriteString(link)
	b.WriteString("\n\n")
	b.WriteString("・リンクの有効期限は、このメールの送信から1時間です。\n")
	b.WriteString("・リンクは1回の登録操作にのみ使えます。パスキーの確認をキャンセルした場合や失敗した場合は、サインインヘルプからもう一度リクエストしてください。\n")
	b.WriteString("・登録済みのパスキーはそのまま使えます。\n\n")
	b.WriteString("お心当たりがない場合は、このメールを無視してください。リンクを開かない限り、アカウントは変更されません。\n\n")
	b.WriteString("Liverty Music\n")
	return "【Liverty Music】新しいパスキーの登録", b.String()
}

// emailLanguage returns the language an email is written in: "en" for a
// user who prefers English, otherwise Japanese ("ja").
func emailLanguage(preferred string) string {
	if preferred == "en" {
		return "en"
	}
	return "ja"
}

// NoopPasskeyRegistrationLinkSender is used when the consumer has no
// fan-passkey-recovery key or no Postmark token (local development): every
// call fails with Internal.
type NoopPasskeyRegistrationLinkSender struct {
	logger *logging.Logger
}

// Compile-time interface compliance check.
var _ entity.PasskeyRegistrationLinkSender = (*NoopPasskeyRegistrationLinkSender)(nil)

// NewNoopPasskeyRegistrationLinkSender creates a NoopPasskeyRegistrationLinkSender.
func NewNoopPasskeyRegistrationLinkSender(logger *logging.Logger) *NoopPasskeyRegistrationLinkSender {
	return &NoopPasskeyRegistrationLinkSender{logger: logger}
}

// SendPasskeyRegistrationLink returns Internal because sending is not configured.
func (s *NoopPasskeyRegistrationLinkSender) SendPasskeyRegistrationLink(ctx context.Context, user *entity.User) error {
	s.logger.Warn(ctx, "passkey registration link skipped: email is not configured",
		slog.String("user_id", user.ID))
	return apperr.New(codes.Internal, "email is not configured")
}
