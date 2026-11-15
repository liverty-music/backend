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

var soldMsg = entity.ResaleListingSoldEmail{Subject: "リセールでチケットが売れました", TextBody: "本文"}

func newResaleSoldSender(t *testing.T, stub *stubPostmark) *mail.PostmarkResaleSoldSender {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(srv.Close)
	logger, err := logging.New()
	require.NoError(t, err)
	return mail.NewPostmarkResaleSoldSenderForTest(srv.URL, "server-token", logger)
}

func TestPostmarkResaleSoldSender_SendSoldEmail(t *testing.T) {
	t.Parallel()

	t.Run("sends the message to the address, tagged with the id", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-listing/send-sold-email "First send"
		stub := &stubPostmark{status: http.StatusOK}

		require.NoError(t, newResaleSoldSender(t, stub).SendSoldEmail(context.Background(), "listing-1", "fan@example.com", soldMsg))

		require.Len(t, stub.sent, 1)
		assert.Equal(t, "fan@example.com", stub.sent[0]["To"])
		assert.Equal(t, soldMsg.Subject, stub.sent[0]["Subject"])
		assert.Equal(t, "resale-sold", stub.sent[0]["Tag"])
		assert.Equal(t, map[string]any{"resale_listing_id": "listing-1"}, stub.sent[0]["Metadata"])
	})

	t.Run("mail unavailable", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-listing/send-sold-email "Mail unavailable"
		stub := &stubPostmark{status: http.StatusInternalServerError}

		err := newResaleSoldSender(t, stub).SendSoldEmail(context.Background(), "listing-1", "fan@example.com", soldMsg)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
	})

	t.Run("empty address", func(t *testing.T) {
		t.Parallel()
		stub := &stubPostmark{status: http.StatusOK}

		err := newResaleSoldSender(t, stub).SendSoldEmail(context.Background(), "listing-1", "", soldMsg)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
		assert.Empty(t, stub.sent)
	})
}
