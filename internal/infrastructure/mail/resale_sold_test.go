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

// fakeListings is an in-memory ResaleListingStore.
type fakeListings struct {
	mu       sync.Mutex
	listings map[entity.ResaleListingID]*entity.ResaleListing
}

func (f *fakeListings) Get(_ context.Context, id entity.ResaleListingID) (*entity.ResaleListing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	l, ok := f.listings[id]
	if !ok {
		return nil, apperr.ErrNotFound
	}
	copied := *l
	return &copied, nil
}

func (f *fakeListings) MarkSoldEmailSent(_ context.Context, id entity.ResaleListingID, at time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if l := f.listings[id]; l.SoldEmailSendTime == nil {
		l.SoldEmailSendTime = &at
	}
	return nil
}

var soldMsg = entity.ResaleListingSoldEmail{Subject: "公式リセールでチケットが売れました", TextBody: "本文"}

func newResaleSoldSender(t *testing.T, stub *stubPostmark, listings *fakeListings) *mail.PostmarkResaleSoldSender {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(stub.handler))
	t.Cleanup(srv.Close)
	logger, err := logging.New()
	require.NoError(t, err)
	return mail.NewPostmarkResaleSoldSenderForTest(listings, srv.URL, "server-token", func() time.Time { return sentAt }, logger)
}

func soldListing() *fakeListings {
	return &fakeListings{listings: map[entity.ResaleListingID]*entity.ResaleListing{
		"listing-1": {ID: "listing-1", Status: entity.ResaleListingStatusSold},
	}}
}

func TestPostmarkResaleSoldSender_SendSoldEmail(t *testing.T) {
	t.Parallel()

	t.Run("first send", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-listing/send-sold-email "First send"
		listings := soldListing()
		stub := &stubPostmark{status: http.StatusOK}
		sender := newResaleSoldSender(t, stub, listings)

		require.NoError(t, sender.SendSoldEmail(context.Background(), "listing-1", "fan@example.com", soldMsg))

		require.Len(t, stub.sent, 1)
		assert.Equal(t, "fan@example.com", stub.sent[0]["To"])
		assert.Equal(t, "resale-sold", stub.sent[0]["Tag"])
		got, _ := listings.Get(context.Background(), "listing-1")
		assert.Equal(t, &sentAt, got.SoldEmailSendTime)
	})

	t.Run("sent before", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-listing/send-sold-email "Sent before"
		listings := soldListing()
		earlier := sentAt.Add(-time.Hour)
		listings.listings["listing-1"].SoldEmailSendTime = &earlier
		stub := &stubPostmark{status: http.StatusOK}
		sender := newResaleSoldSender(t, stub, listings)

		require.NoError(t, sender.SendSoldEmail(context.Background(), "listing-1", "fan@example.com", soldMsg))

		assert.Empty(t, stub.sent)
	})

	t.Run("mail unavailable", func(t *testing.T) {
		t.Parallel()
		// @spec components/entity/resale-listing/send-sold-email "Mail unavailable"
		listings := soldListing()
		stub := &stubPostmark{status: http.StatusInternalServerError}
		sender := newResaleSoldSender(t, stub, listings)

		err := sender.SendSoldEmail(context.Background(), "listing-1", "fan@example.com", soldMsg)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		got, _ := listings.Get(context.Background(), "listing-1")
		assert.Nil(t, got.SoldEmailSendTime)
	})

	t.Run("empty address", func(t *testing.T) {
		t.Parallel()
		sender := newResaleSoldSender(t, &stubPostmark{status: http.StatusOK}, soldListing())

		err := sender.SendSoldEmail(context.Background(), "listing-1", "", soldMsg)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}
