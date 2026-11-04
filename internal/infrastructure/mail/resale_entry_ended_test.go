package mail_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/mail"
)

// fakeResaleEntries is an in-memory ResaleEntryStore.
type fakeResaleEntries struct {
	mu      sync.Mutex
	entries map[entity.ResaleEntryID]*entity.ResaleEntry
}

func (f *fakeResaleEntries) Get(_ context.Context, id entity.ResaleEntryID) (*entity.ResaleEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.entries[id]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	copied := *e
	return &copied, nil
}

func (f *fakeResaleEntries) MarkEndedEmailSent(_ context.Context, id entity.ResaleEntryID, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e := f.entries[id]; e.EndedEmailSendTime == nil {
		e.EndedEmailSendTime = &at
	}
	return nil
}

var endedMsg = entity.ResaleEntryEndedEmail{Subject: "公式リセールの申し込みについて", TextBody: "本文"}

func newResaleEntryEndedSender(t *testing.T, stub *stubPostmark, entries *fakeResaleEntries) *mail.PostmarkResaleEntryEndedSender {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(srv.Close)
	logger, err := logging.New()
	require.NoError(t, err)
	return mail.NewPostmarkResaleEntryEndedSenderForTest(entries, srv.URL, "server-token", func() time.Time { return sentAt }, logger)
}

func resaleEntryIn(state entity.ResaleEntryState) *fakeResaleEntries {
	return &fakeResaleEntries{entries: map[entity.ResaleEntryID]*entity.ResaleEntry{
		"entry-1": {ID: "entry-1", State: state},
	}}
}

func TestPostmarkResaleEntryEndedSender_SendEndedEmail(t *testing.T) {
	t.Parallel()

	t.Run("first send", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-entry/send-ended-email "First send"
		entries := resaleEntryIn(entity.ResaleEntryStateLapsed)
		stub := &stubPostmark{status: http.StatusOK}
		sender := newResaleEntryEndedSender(t, stub, entries)

		require.NoError(t, sender.SendEndedEmail(context.Background(), "entry-1", "fan@example.com", endedMsg))

		require.Len(t, stub.sent, 1)
		assert.Equal(t, "fan@example.com", stub.sent[0]["To"])
		assert.Equal(t, endedMsg.Subject, stub.sent[0]["Subject"])
		assert.Equal(t, "resale-entry-ended", stub.sent[0]["Tag"])
		got, _ := entries.Get(context.Background(), "entry-1")
		assert.Equal(t, &sentAt, got.EndedEmailSendTime)
	})

	t.Run("sent already", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-entry/send-ended-email "Sent already"
		entries := resaleEntryIn(entity.ResaleEntryStateLost)
		earlier := sentAt.Add(-time.Hour)
		entries.entries["entry-1"].EndedEmailSendTime = &earlier
		stub := &stubPostmark{status: http.StatusOK}
		sender := newResaleEntryEndedSender(t, stub, entries)

		require.NoError(t, sender.SendEndedEmail(context.Background(), "entry-1", "fan@example.com", endedMsg))

		assert.Empty(t, stub.sent, "no email is sent")
	})

	t.Run("entry still in the draw", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-entry/send-ended-email "Entry still in the draw"
		stub := &stubPostmark{status: http.StatusOK}
		sender := newResaleEntryEndedSender(t, stub, resaleEntryIn(entity.ResaleEntryStateEntered))

		err := sender.SendEndedEmail(context.Background(), "entry-1", "fan@example.com", endedMsg)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		assert.Empty(t, stub.sent)
	})

	t.Run("unknown entry", func(t *testing.T) {
		t.Parallel()
		stub := &stubPostmark{status: http.StatusOK}
		sender := newResaleEntryEndedSender(t, stub, resaleEntryIn(entity.ResaleEntryStateLost))

		assert.ErrorIs(t, sender.SendEndedEmail(context.Background(), "entry-2", "fan@example.com", endedMsg), apperr.ErrNotFound)
	})
}
