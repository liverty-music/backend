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

var lossMsg = entity.LotteryLossEmail{Subject: "チケット抽選の結果のお知らせ", TextBody: "本文"}

func newLotteryLossSender(t *testing.T, stub *stubPostmark) *mail.PostmarkLotteryLossSender {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(srv.Close)
	logger, err := logging.New()
	require.NoError(t, err)
	return mail.NewPostmarkLotteryLossSenderForTest(srv.URL, "server-token", logger)
}

func TestPostmarkLotteryLossSender_SendLossEmail(t *testing.T) {
	t.Parallel()

	t.Run("sends the message to the address, tagged with the id", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/lottery-entry/send-loss-email "Email sent"
		stub := &stubPostmark{status: http.StatusOK}

		require.NoError(t, newLotteryLossSender(t, stub).SendLossEmail(context.Background(), "entry-1", "fan@example.com", lossMsg))

		require.Len(t, stub.sent, 1)
		assert.Equal(t, "fan@example.com", stub.sent[0]["To"])
		assert.Equal(t, lossMsg.Subject, stub.sent[0]["Subject"])
		assert.Equal(t, "lottery-loss", stub.sent[0]["Tag"])
		assert.Equal(t, map[string]any{"lottery_entry_id": "entry-1"}, stub.sent[0]["Metadata"])
	})

	t.Run("mail unavailable", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/lottery-entry/send-loss-email "Mail unavailable"
		stub := &stubPostmark{status: http.StatusInternalServerError}

		err := newLotteryLossSender(t, stub).SendLossEmail(context.Background(), "entry-1", "fan@example.com", lossMsg)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})

	t.Run("empty address", func(t *testing.T) {
		t.Parallel()
		stub := &stubPostmark{status: http.StatusOK}

		err := newLotteryLossSender(t, stub).SendLossEmail(context.Background(), "entry-1", "", lossMsg)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Empty(t, stub.sent)
	})
}
