package mail

import (
	"context"
	"errors"
	"log/slog"
	"net/url"
	"strings"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
)

// emailVerificationTag tags verification emails in Postmark's activity log.
const emailVerificationTag = "email-verification"

// EmailVerificationCodeIssuer issues an identity's email verification code at
// the identity provider, which sends nothing. Implemented by the identity
// service client.
type EmailVerificationCodeIssuer interface {
	// IssueEmailVerificationCode issues the code sent at sign-up and returns
	// the identity's address and the code. It fails with FailedPrecondition
	// when the address is already verified and NotFound when the identity
	// does not exist.
	IssueEmailVerificationCode(ctx context.Context, providerUserID string) (address, code string, err error)
	// ReissueEmailVerificationCode issues a fresh code when the fan asks for
	// one, with the same results.
	ReissueEmailVerificationCode(ctx context.Context, providerUserID string) (address, code string, err error)
}

// PostmarkEmailVerificationSender implements [entity.EmailVerifier]: it issues
// a code at the identity provider and emails a link to the fan web's email
// verification screen through Postmark's transactional message stream. The
// link carries the code, so neither is ever logged.
type PostmarkEmailVerificationSender struct {
	postmarkClient
	codes EmailVerificationCodeIssuer
	// fanWebBaseURL is the fan web origin, e.g. https://liverty-music.app.
	fanWebBaseURL string
}

// Compile-time interface compliance check.
var _ entity.EmailVerifier = (*PostmarkEmailVerificationSender)(nil)

// NewPostmarkEmailVerificationSender creates a sender for the given code
// issuer whose links open the fan web at fanWebBaseURL, with the given server
// token, From address and message stream.
func NewPostmarkEmailVerificationSender(codes EmailVerificationCodeIssuer, fanWebBaseURL, serverToken, fromAddress, messageStream string, logger *logging.Logger) *PostmarkEmailVerificationSender {
	return newPostmarkEmailVerificationSender(codes, fanWebBaseURL, postmarkAPIURL, serverToken, fromAddress, messageStream, logger)
}

func newPostmarkEmailVerificationSender(codes EmailVerificationCodeIssuer, fanWebBaseURL, apiURL, serverToken, fromAddress, messageStream string, logger *logging.Logger) *PostmarkEmailVerificationSender {
	return &PostmarkEmailVerificationSender{
		postmarkClient: newPostmarkClient(apiURL, serverToken, fromAddress, messageStream, logger),
		codes:          codes,
		fanWebBaseURL:  strings.TrimRight(fanWebBaseURL, "/"),
	}
}

// SendVerification implements [entity.EmailVerifier]. Any failure is
// Internal.
func (s *PostmarkEmailVerificationSender) SendVerification(ctx context.Context, user *entity.User) error {
	attrs := slog.String("user_id", user.ID)
	address, code, err := s.codes.IssueEmailVerificationCode(ctx, user.ProviderUserID)
	if err != nil {
		return apperr.Wrap(err, codes.Internal, "email verification code cannot be issued", attrs)
	}
	return s.send(ctx, user, address, code)
}

// ResendVerification implements [entity.EmailVerifier]. An address that is
// already verified is FailedPrecondition; any other failure is Internal.
func (s *PostmarkEmailVerificationSender) ResendVerification(ctx context.Context, user *entity.User) error {
	attrs := slog.String("user_id", user.ID)
	address, code, err := s.codes.ReissueEmailVerificationCode(ctx, user.ProviderUserID)
	if err != nil {
		if errors.Is(err, apperr.ErrFailedPrecondition) {
			return err
		}
		return apperr.Wrap(err, codes.Internal, "email verification code cannot be issued", attrs)
	}
	return s.send(ctx, user, address, code)
}

// send emails the verification link for code to address, in the user's
// preferred language. The address is the one the identity provider holds.
func (s *PostmarkEmailVerificationSender) send(ctx context.Context, user *entity.User, address, code string) error {
	attrs := slog.String("user_id", user.ID)
	query := url.Values{}
	query.Set("user", user.ID)
	query.Set("code", code)
	link := s.fanWebBaseURL + "/verify-email?" + query.Encode()
	subject, body := emailVerificationEmail(emailLanguage(user.PreferredLanguage), link)
	if err := s.post(ctx, address, subject, body, emailVerificationTag,
		map[string]string{"user_id": user.ID}, attrs); err != nil {
		return apperr.Wrap(err, codes.Internal, "verification email cannot be sent", attrs)
	}
	return nil
}

// emailVerificationEmail renders the subject and text of the verification
// email in lang ("ja" or "en"): the link to the email verification screen,
// its 1-hour validity, and that it can be ignored by someone who did not sign
// up.
func emailVerificationEmail(lang, link string) (subject, body string) {
	var b strings.Builder
	if lang == "en" {
		b.WriteString("Thank you for signing up for Liverty Music.\n")
		b.WriteString("Open the link below and tap \"Verify email address\" to confirm this address.\n\n")
		b.WriteString(link)
		b.WriteString("\n\n")
		b.WriteString("- The link works for 1 hour after this email was sent. If it has expired, send a new one from Settings in the app.\n\n")
		b.WriteString("If you did not sign up for Liverty Music, you can ignore this email.\n\n")
		b.WriteString("Liverty Music\n")
		return "[Liverty Music] Verify your email address", b.String()
	}
	b.WriteString("Liverty Music にご登録いただきありがとうございます。\n")
	b.WriteString("下のリンクを開き、「メールアドレスを確認する」を押して、このメールアドレスを確認してください。\n\n")
	b.WriteString(link)
	b.WriteString("\n\n")
	b.WriteString("・リンクの有効期限は、このメールの送信から1時間です。期限が切れた場合は、アプリの設定から再送してください。\n\n")
	b.WriteString("お心当たりがない場合は、このメールを無視してください。\n\n")
	b.WriteString("Liverty Music\n")
	return "【Liverty Music】メールアドレスの確認", b.String()
}

// NoopEmailVerificationSender is used when the consumer has no Postmark token
// (local development): every call fails with Internal.
type NoopEmailVerificationSender struct {
	logger *logging.Logger
}

// Compile-time interface compliance check.
var _ entity.EmailVerifier = (*NoopEmailVerificationSender)(nil)

// NewNoopEmailVerificationSender creates a NoopEmailVerificationSender.
func NewNoopEmailVerificationSender(logger *logging.Logger) *NoopEmailVerificationSender {
	return &NoopEmailVerificationSender{logger: logger}
}

// SendVerification returns Internal because sending is not configured.
func (s *NoopEmailVerificationSender) SendVerification(ctx context.Context, user *entity.User) error {
	s.logger.Warn(ctx, "verification email skipped: email is not configured", slog.String("user_id", user.ID))
	return apperr.New(codes.Internal, "email is not configured")
}

// ResendVerification returns Internal because sending is not configured.
func (s *NoopEmailVerificationSender) ResendVerification(ctx context.Context, user *entity.User) error {
	s.logger.Warn(ctx, "verification email skipped: email is not configured", slog.String("user_id", user.ID))
	return apperr.New(codes.Internal, "email is not configured")
}
