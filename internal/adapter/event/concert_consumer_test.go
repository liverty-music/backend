package event_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- test doubles ---

type fakeConcertCreationUC struct {
	called []messaging.ConcertDiscoveredData
	err    error
}

func (uc *fakeConcertCreationUC) CreateFromDiscovered(_ context.Context, data messaging.ConcertDiscoveredData) error {
	if uc.err != nil {
		return uc.err
	}
	uc.called = append(uc.called, data)
	return nil
}

// --- helpers ---

func makeDiscoveredMsg(t *testing.T, data messaging.ConcertDiscoveredData) *message.Message {
	t.Helper()
	payload, err := json.Marshal(data)
	require.NoError(t, err)
	return message.NewMessage("test-id", payload)
}

// --- tests ---

func TestConcertConsumer_Handle(t *testing.T) {
	localDate := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)

	t.Run("delegates to use case", func(t *testing.T) {
		uc := &fakeConcertCreationUC{}
		handler := event.NewConcertConsumer(uc, newTestLogger(t))

		data := messaging.ConcertDiscoveredData{
			ArtistID:   "artist-1",
			ArtistName: "Test Artist",
			Concerts: []messaging.ScrapedConcertData{
				{
					Title:           "Concert A",
					ListedVenueName: "Venue X",
					LocalDate:       localDate,
					SourceURL:       "https://example.com/a",
				},
			},
		}

		msg := makeDiscoveredMsg(t, data)
		err := handler.Handle(msg)
		require.NoError(t, err)

		require.Len(t, uc.called, 1)
		assert.Equal(t, "artist-1", uc.called[0].ArtistID)
		assert.Len(t, uc.called[0].Concerts, 1)
	})

	t.Run("returns error when use case fails", func(t *testing.T) {
		uc := &fakeConcertCreationUC{err: fmt.Errorf("db connection lost")}
		handler := event.NewConcertConsumer(uc, newTestLogger(t))

		data := messaging.ConcertDiscoveredData{
			ArtistID:   "artist-1",
			ArtistName: "Test Artist",
			Concerts:   []messaging.ScrapedConcertData{},
		}

		msg := makeDiscoveredMsg(t, data)
		err := handler.Handle(msg)
		assert.Error(t, err)
	})

	t.Run("returns error on invalid payload", func(t *testing.T) {
		uc := &fakeConcertCreationUC{}
		handler := event.NewConcertConsumer(uc, newTestLogger(t))

		msg := message.NewMessage("bad-id", []byte("invalid json"))
		err := handler.Handle(msg)
		assert.Error(t, err)
	})
}
