package event_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- test doubles ---

type fakeVenueEnrichmentUC struct {
	enriched []string // venue IDs that were enriched
	err      error
}

func (uc *fakeVenueEnrichmentUC) EnrichPendingVenues(_ context.Context) error {
	return nil
}

func (uc *fakeVenueEnrichmentUC) EnrichOne(_ context.Context, venueID string) error {
	if uc.err != nil {
		return uc.err
	}
	uc.enriched = append(uc.enriched, venueID)
	return nil
}

// --- helpers ---

func makeVenueCreatedMsg(t *testing.T, data messaging.VenueCreatedData) *message.Message {
	t.Helper()
	payload, err := json.Marshal(data)
	require.NoError(t, err)
	return message.NewMessage("test-id", payload)
}

// --- tests ---

func TestVenueConsumer_Handle(t *testing.T) {
	t.Run("enriches venue on venue.created event", func(t *testing.T) {
		uc := &fakeVenueEnrichmentUC{}
		handler := event.NewVenueConsumer(uc, newTestLogger(t))

		data := messaging.VenueCreatedData{
			VenueID: "venue-1",
			Name:    "Zepp Nagoya",
		}

		msg := makeVenueCreatedMsg(t, data)
		err := handler.Handle(msg)
		require.NoError(t, err)

		assert.Contains(t, uc.enriched, "venue-1")
	})

	t.Run("returns error when enrichment fails", func(t *testing.T) {
		uc := &fakeVenueEnrichmentUC{err: fmt.Errorf("external service down")}
		handler := event.NewVenueConsumer(uc, newTestLogger(t))

		data := messaging.VenueCreatedData{
			VenueID: "venue-2",
			Name:    "Unknown Venue",
		}

		msg := makeVenueCreatedMsg(t, data)
		err := handler.Handle(msg)
		assert.Error(t, err)
	})

	t.Run("returns error on invalid payload", func(t *testing.T) {
		uc := &fakeVenueEnrichmentUC{}
		handler := event.NewVenueConsumer(uc, newTestLogger(t))

		msg := message.NewMessage("bad-id", []byte("invalid json"))
		err := handler.Handle(msg)
		assert.Error(t, err)
	})
}
