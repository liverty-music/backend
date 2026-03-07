package event_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeArtistCreatedMsg(t *testing.T, data messaging.ArtistCreatedData) *message.Message {
	t.Helper()
	payload, err := json.Marshal(data)
	require.NoError(t, err)
	return message.NewMessage("test-id", payload)
}

func TestArtistNameConsumer_Handle(t *testing.T) {
	t.Run("updates name when canonical differs", func(t *testing.T) {
		artistRepo := mocks.NewMockArtistRepository(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		handler := event.NewArtistNameConsumer(artistRepo, idManager, newTestLogger(t))

		idManager.EXPECT().GetArtist(anyCtx, "mbid-001").Return(&entity.Artist{
			Name: "Canonical Name",
			MBID: "mbid-001",
		}, nil).Once()
		artistRepo.EXPECT().UpdateName(anyCtx, "artist-1", "Canonical Name").Return(nil).Once()

		msg := makeArtistCreatedMsg(t, messaging.ArtistCreatedData{
			ArtistID:   "artist-1",
			ArtistName: "Original Name",
			MBID:       "mbid-001",
		})

		err := handler.Handle(msg)
		assert.NoError(t, err)
	})

	t.Run("no-op when name already matches", func(t *testing.T) {
		artistRepo := mocks.NewMockArtistRepository(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		handler := event.NewArtistNameConsumer(artistRepo, idManager, newTestLogger(t))

		idManager.EXPECT().GetArtist(anyCtx, "mbid-002").Return(&entity.Artist{
			Name: "Same Name",
			MBID: "mbid-002",
		}, nil).Once()

		msg := makeArtistCreatedMsg(t, messaging.ArtistCreatedData{
			ArtistID:   "artist-2",
			ArtistName: "Same Name",
			MBID:       "mbid-002",
		})

		err := handler.Handle(msg)
		assert.NoError(t, err)
	})

	t.Run("returns error when MusicBrainz lookup fails", func(t *testing.T) {
		artistRepo := mocks.NewMockArtistRepository(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		handler := event.NewArtistNameConsumer(artistRepo, idManager, newTestLogger(t))

		idManager.EXPECT().GetArtist(anyCtx, "mbid-fail").Return(nil, fmt.Errorf("rate limited")).Once()

		msg := makeArtistCreatedMsg(t, messaging.ArtistCreatedData{
			ArtistID:   "artist-3",
			ArtistName: "Some Artist",
			MBID:       "mbid-fail",
		})

		err := handler.Handle(msg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "resolve canonical name")
	})

	t.Run("returns error when UpdateName fails", func(t *testing.T) {
		artistRepo := mocks.NewMockArtistRepository(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		handler := event.NewArtistNameConsumer(artistRepo, idManager, newTestLogger(t))

		idManager.EXPECT().GetArtist(anyCtx, "mbid-003").Return(&entity.Artist{
			Name: "New Name",
			MBID: "mbid-003",
		}, nil).Once()
		artistRepo.EXPECT().UpdateName(anyCtx, "artist-4", "New Name").Return(fmt.Errorf("db error")).Once()

		msg := makeArtistCreatedMsg(t, messaging.ArtistCreatedData{
			ArtistID:   "artist-4",
			ArtistName: "Old Name",
			MBID:       "mbid-003",
		})

		err := handler.Handle(msg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "update artist name")
	})

	t.Run("returns error on invalid payload", func(t *testing.T) {
		artistRepo := mocks.NewMockArtistRepository(t)
		idManager := mocks.NewMockArtistIdentityManager(t)
		handler := event.NewArtistNameConsumer(artistRepo, idManager, newTestLogger(t))

		msg := message.NewMessage("bad-id", []byte("not json"))
		err := handler.Handle(msg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "parse artist.created event")
	})
}
