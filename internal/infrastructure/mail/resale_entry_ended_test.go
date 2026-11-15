package mail_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/mail"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var endedMsg = entity.ResaleEntryEndedEmail{Subject: "公式リセールの申込が終了しました", TextBody: "本文"}

func newResaleEntryEndedSender(t *testing.T, stub *stubPostmark) *mail.PostmarkResaleEntryEndedSender {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(srv.Close)
	logger, err := logging.New()
	require.NoError(t, err)
	return mail.NewPostmarkResaleEntryEndedSenderForTest(srv.URL, "server-token", logger)
}

func TestPostmarkResaleEntryEndedSender_SendEndedEmail(t *testing.T) {
	t.Parallel()

	t.Run("sends the message to the address, tagged with the id", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-entry/send-ended-email "First send"
		stub := &stubPostmark{status: http.StatusOK}

		require.NoError(t, newResaleEntryEndedSender(t, stub).SendEndedEmail(context.Background(), "entry-1", "fan@example.com", endedMsg))

		require.Len(t, stub.sent, 1)
		assert.Equal(t, "fan@example.com", stub.sent[0]["To"])
		assert.Equal(t, endedMsg.Subject, stub.sent[0]["Subject"])
		assert.Equal(t, "resale-entry-ended", stub.sent[0]["Tag"])
		assert.Equal(t, map[string]any{"resale_entry_id": "entry-1"}, stub.sent[0]["Metadata"])
	})

	t.Run("mail unavailable", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-entry/send-ended-email "Mail unavailable"
		stub := &stubPostmark{status: http.StatusInternalServerError}

		err := newResaleEntryEndedSender(t, stub).SendEndedEmail(context.Background(), "entry-1", "fan@example.com", endedMsg)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})

	t.Run("empty address", func(t *testing.T) {
		t.Parallel()
		stub := &stubPostmark{status: http.StatusOK}

		err := newResaleEntryEndedSender(t, stub).SendEndedEmail(context.Background(), "entry-1", "", endedMsg)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Empty(t, stub.sent)
	})
}
