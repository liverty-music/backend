package webhook

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/pannpers/go-logging/logging"
	"github.com/zitadel/zitadel-go/v3/pkg/actions"
)

// requirePost answers 405 and reports false unless the request is a POST.
func requirePost(w http.ResponseWriter, r *http.Request) bool {
	if r.Method == http.MethodPost {
		return true
	}
	w.Header().Set("Allow", http.MethodPost)
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	return false
}

// requireConfigured answers 503 and reports false when a setting the handler
// needs is empty. An empty signing key would not reject anything:
// actions.ValidateRequestPayload computes the HMAC with whatever key it is
// given, so a body signed with "" would verify.
func requireConfigured(ctx context.Context, w http.ResponseWriter, logger *logging.Logger, handler, setting, value string) bool {
	if value != "" {
		return true
	}
	logger.Error(ctx, handler+": "+setting+" is not configured; recording nothing",
		errors.New(setting+" is empty"))
	http.Error(w, "not configured", http.StatusServiceUnavailable)
	return false
}

// readSignedBody reads the body of an Actions v2 event execution and verifies
// the HMAC ZITADEL-Signature header against the raw JSON body with the
// Target's signing key; this is the security boundary alongside the private,
// internal-only webhook listener. It answers 400 for a missing or unreadable
// body and 401 for a signature that does not verify, and reports false.
func readSignedBody(w http.ResponseWriter, r *http.Request, logger *logging.Logger, handler, signingKey string) ([]byte, bool) {
	ctx := r.Context()
	body, err := io.ReadAll(r.Body)
	if err != nil || len(body) == 0 {
		logger.Warn(ctx, handler+": missing or unreadable body")
		http.Error(w, "bad request", http.StatusBadRequest)
		return nil, false
	}
	if err := actions.ValidateRequestPayload(body, &r.Header, signingKey); err != nil {
		logger.Warn(ctx, handler+": webhook signature validation failed",
			slog.String("error", err.Error()),
		)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return nil, false
	}
	return body, true
}
