package mail_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/mail"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeEntries is an in-memory LotteryEntryStore.
type fakeEntries struct {
	mu      sync.Mutex
	entries map[entity.LotteryEntryID]*entity.LotteryEntry
}

func (f *fakeEntries) Get(_ context.Context, id entity.LotteryEntryID) (*entity.LotteryEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.entries[id]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	copied := *e
	return &copied, nil
}

func (f *fakeEntries) MarkLossEmailSent(_ context.Context, id entity.LotteryEntryID, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e := f.entries[id]; e.LossEmailSendTime == nil {
		e.LossEmailSendTime = &at
	}
	return nil
}

var lossMsg = entity.LotteryLossEmail{Subject: "チケット抽選の結果のお知らせ", TextBody: "本文"}

func newLotteryLossSender(t *testing.T, stub *stubPostmark, entries *fakeEntries) *mail.PostmarkLotteryLossSender {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(srv.Close)
	logger, err := logging.New()
	require.NoError(t, err)
	return mail.NewPostmarkLotteryLossSenderForTest(entries, srv.URL, "server-token", func() time.Time { return sentAt }, logger)
}

func lostEntry() *fakeEntries {
	return &fakeEntries{entries: map[entity.LotteryEntryID]*entity.LotteryEntry{
		"entry-1": {ID: "entry-1", State: entity.LotteryEntryStateLost, LostReason: entity.LotteryEntryLostReasonNotDrawn},
	}}
}

func TestPostmarkLotteryLossSender_SendLossEmail(t *testing.T) {
	t.Parallel()

	t.Run("email sent", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/lottery-entry/send-loss-email "Email sent"
		entries := lostEntry()
		stub := &stubPostmark{status: http.StatusOK}
		sender := newLotteryLossSender(t, stub, entries)

		require.NoError(t, sender.SendLossEmail(context.Background(), "entry-1", "fan@example.com", lossMsg))

		require.Len(t, stub.sent, 1)
		assert.Equal(t, "fan@example.com", stub.sent[0]["To"])
		assert.Equal(t, lossMsg.Subject, stub.sent[0]["Subject"])
		assert.Equal(t, "lottery-loss", stub.sent[0]["Tag"])
		got, _ := entries.Get(context.Background(), "entry-1")
		assert.Equal(t, &sentAt, got.LossEmailSendTime)
	})

	t.Run("redelivered event", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/lottery-entry/send-loss-email "Redelivered event"
		entries := lostEntry()
		earlier := sentAt.Add(-time.Hour)
		entries.entries["entry-1"].LossEmailSendTime = &earlier
		stub := &stubPostmark{status: http.StatusOK}
		sender := newLotteryLossSender(t, stub, entries)

		require.NoError(t, sender.SendLossEmail(context.Background(), "entry-1", "fan@example.com", lossMsg))

		assert.Empty(t, stub.sent)
	})

	t.Run("mail unavailable", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/lottery-entry/send-loss-email "Mail unavailable"
		entries := lostEntry()
		stub := &stubPostmark{status: http.StatusInternalServerError}
		sender := newLotteryLossSender(t, stub, entries)

		err := sender.SendLossEmail(context.Background(), "entry-1", "fan@example.com", lossMsg)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		got, _ := entries.Get(context.Background(), "entry-1")
		assert.Nil(t, got.LossEmailSendTime)
	})

	t.Run("empty address", func(t *testing.T) {
		t.Parallel()
		sender := newLotteryLossSender(t, &stubPostmark{status: http.StatusOK}, lostEntry())

		err := sender.SendLossEmail(context.Background(), "entry-1", "", lossMsg)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})

	t.Run("unknown entry", func(t *testing.T) {
		t.Parallel()
		sender := newLotteryLossSender(t, &stubPostmark{status: http.StatusOK}, lostEntry())

		err := sender.SendLossEmail(context.Background(), "entry-2", "fan@example.com", lossMsg)

		assert.ErrorIs(t, err, apperr.ErrNotFound)
	})
}
