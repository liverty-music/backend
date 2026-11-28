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

func TestArtistRefreshConsumer_Handle(t *testing.T) {
	t.Parallel()

	t.Run("returns error when use case fails", func(t *testing.T) {
		t.Parallel()

		artistUC := ucmocks.NewMockArtistUseCase(t)
		handler := event.NewArtistRefreshConsumer(artistUC, newTestLogger(t))

		artistUC.EXPECT().VerifyMBID(anyCtx, "artist-2").Return(fmt.Errorf("rate limited")).Once()

		msg := makeAnnouncementMsg(t, "announcement-1", "", newArtistAnnouncement("artist-2", "Some Artist", "mbid-fail"))

		err := handler.Handle(msg)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "handle ARTIST.created event")
	})

	t.Run("an artist Search registered verified is announced", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/artist/verify-mbid "Artist announced already verified"
		// VerifyMBID runs for every announced artist; for one that is already
		// Verified it changes nothing (covered by the usecase test "Already
		// verified") and succeeds, so the delivery is acknowledged.
		artistUC := ucmocks.NewMockArtistUseCase(t)
		handler := event.NewArtistRefreshConsumer(artistUC, newTestLogger(t))
		artistUC.EXPECT().VerifyMBID(anyCtx, "artist-3").Return(nil).Once()

		err := handler.Handle(makeAnnouncementMsg(t, "announcement-2", "", newArtistAnnouncement("artist-3", "sumika", "bc731e96-0c06-4adf-b94b-ca636f48998a")))

		assert.NoError(t, err)
	})

	t.Run("returns error on invalid payload", func(t *testing.T) {
		t.Parallel()

		artistUC := ucmocks.NewMockArtistUseCase(t)
		handler := event.NewArtistRefreshConsumer(artistUC, newTestLogger(t))

		err := handler.Handle(malformedMsg())
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "parse artist.created event")
	})
}
