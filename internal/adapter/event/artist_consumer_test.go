package event_test

import (
	"fmt"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/adapter/event"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/stretchr/testify/assert"
)

// newArtistAnnouncement returns the Artist an ARTIST.created announcement
// carries.
func newArtistAnnouncement(id, name, mbid string) *entityv1.Artist {
	return &entityv1.Artist{
		Id:   &entityv1.ArtistId{Value: id},
		Name: &entityv1.ArtistName{Value: name},
		Mbid: &entityv1.Mbid{Value: mbid},
	}
}

func TestArtistNameConsumer_Handle(t *testing.T) {
	t.Parallel()

	t.Run("returns error when use case fails", func(t *testing.T) {
		t.Parallel()

		nameResolutionUC := ucmocks.NewMockArtistNameResolutionUseCase(t)
		handler := event.NewArtistNameConsumer(nameResolutionUC, newTestLogger(t))

		nameResolutionUC.EXPECT().ResolveCanonicalName(anyCtx, "artist-2", "mbid-fail", "Some Artist").Return(fmt.Errorf("rate limited")).Once()

		msg := makeAnnouncementMsg(t, "announcement-1", "", newArtistAnnouncement("artist-2", "Some Artist", "mbid-fail"))

		err := handler.Handle(msg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "handle ARTIST.created event")
	})

	t.Run("returns error on invalid payload", func(t *testing.T) {
		t.Parallel()

		nameResolutionUC := ucmocks.NewMockArtistNameResolutionUseCase(t)
		handler := event.NewArtistNameConsumer(nameResolutionUC, newTestLogger(t))

		err := handler.Handle(malformedMsg())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "parse artist.created event")
	})
}
