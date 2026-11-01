package event_test

// Each receiver reads the protobuf announcement the outbox records; the JSON
// of a message published before the outbox is covered by the receiver's own
// test file.

import (
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/liverty-music/backend/internal/adapter/event"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

func TestReceivers_Protobuf(t *testing.T) {
	t.Parallel()

	newArtist := &entityv1.Artist{
		Id:   &entityv1.ArtistId{Value: "artist-1"},
		Name: &entityv1.ArtistName{Value: "tuki."},
		Mbid: &entityv1.Mbid{Value: "11111111-1111-1111-1111-111111111111"},
	}

	t.Run("CONCERT.created notifies the Artist's followers of the Concerts under the announcement id", func(t *testing.T) {
		t.Parallel()
		notifications := ucmocks.NewMockNotificationUseCase(t)
		want := usecase.ConcertCreatedData{ArtistID: "artist-1", ConcertIDs: []string{"event-1", "event-2"}, RequestID: "announcement-1"}
		// Delivered twice, the request id is the same, so the followers'
		// Deliver workflows are started once (NotifyNewConcerts).
		notifications.EXPECT().NotifyNewConcerts(anyCtx, want).Return(nil).Twice()
		consumer := event.NewNotificationConsumer(notifications, newTestLogger(t))
		list := &entityv1.ConcertList{Concerts: []*entityv1.Concert{
			{Event: &entityv1.Event{Id: &entityv1.EventId{Value: "event-1"}}},
			{Event: &entityv1.Event{Id: &entityv1.EventId{Value: "event-2"}}},
		}}

		require.NoError(t, consumer.Handle(makeAnnouncementMsg(t, "announcement-1", "artist-1", list)))
		require.NoError(t, consumer.Handle(makeAnnouncementMsg(t, "announcement-1", "artist-1", list)))
	})

	t.Run("CONCERT.created without the Artist is an error", func(t *testing.T) {
		t.Parallel()
		consumer := event.NewNotificationConsumer(ucmocks.NewMockNotificationUseCase(t), newTestLogger(t))
		list := &entityv1.ConcertList{Concerts: []*entityv1.Concert{{Event: &entityv1.Event{Id: &entityv1.EventId{Value: "event-1"}}}}}

		assert.Error(t, consumer.Handle(makeAnnouncementMsg(t, "announcement-1", "", list)))
	})

	t.Run("ARTIST.created resolves the canonical name of the new Artist", func(t *testing.T) {
		t.Parallel()
		names := ucmocks.NewMockArtistNameResolutionUseCase(t)
		// Resolving the same Artist again gives the same name.
		names.EXPECT().ResolveCanonicalName(anyCtx, "artist-1", "11111111-1111-1111-1111-111111111111", "tuki.").Return(nil).Twice()
		consumer := event.NewArtistNameConsumer(names, newTestLogger(t))

		require.NoError(t, consumer.Handle(makeAnnouncementMsg(t, "announcement-1", "", newArtist)))
		require.NoError(t, consumer.Handle(makeAnnouncementMsg(t, "announcement-1", "", newArtist)))
	})

	t.Run("ARTIST.created syncs the image of the new Artist", func(t *testing.T) {
		t.Parallel()
		images := ucmocks.NewMockArtistImageSyncUseCase(t)
		images.EXPECT().SyncArtistImage(anyCtx, "artist-1", "11111111-1111-1111-1111-111111111111").Return(nil).Twice()
		consumer := event.NewArtistImageConsumer(images, newTestLogger(t))

		require.NoError(t, consumer.Handle(makeAnnouncementMsg(t, "announcement-1", "", newArtist)))
		require.NoError(t, consumer.Handle(makeAnnouncementMsg(t, "announcement-1", "", newArtist)))
	})

	t.Run("ARTIST.followed searches the followed Artist's concerts on a first follow", func(t *testing.T) {
		t.Parallel()
		concerts := ucmocks.NewMockConcertUseCase(t)
		// The Artist's ConcertSearch record makes the second run search
		// nothing (SearchNewConcertsOnFirstFollow).
		concerts.EXPECT().SearchNewConcertsOnFirstFollow(anyCtx, "artist-1").Return(nil).Twice()
		consumer := event.NewFollowSearchConsumer(concerts, newTestLogger(t))
		follow := &entityv1.Follow{UserId: &entityv1.UserId{Value: "fan-1"}, ArtistId: &entityv1.ArtistId{Value: "artist-1"}}

		require.NoError(t, consumer.Handle(makeAnnouncementMsg(t, "announcement-1", "", follow)))
		require.NoError(t, consumer.Handle(makeAnnouncementMsg(t, "announcement-1", "", follow)))
	})

	t.Run("SALES_PHASE.discovered announces the discovered TicketSale", func(t *testing.T) {
		t.Parallel()
		announcements := ucmocks.NewMockTicketSaleAnnouncementUseCase(t)
		start := time.Date(2026, 11, 1, 3, 0, 0, 0, time.UTC)
		// The fans' Deliver workflows are named after the sale, so the
		// second delivery notifies no one again (AnnounceDiscoveredSale).
		announcements.EXPECT().AnnounceDiscoveredSale(anyCtx, entity.TicketSaleDiscoveredData{
			TicketSaleID: "sale-1", SeriesID: "series-1", Method: int16(entity.TicketSaleMethodLottery), SaleStartTime: start,
		}).Return(nil).Twice()
		consumer := event.NewTicketSaleAnnouncementConsumer(announcements, newTestLogger(t))
		sale := &entityv1.TicketSale{
			Id:            &entityv1.TicketSaleId{Value: "sale-1"},
			SeriesId:      &entityv1.SeriesId{Value: "series-1"},
			Method:        entityv1.TicketSaleMethod_TICKET_SALE_METHOD_LOTTERY,
			SaleStartTime: timestamppb.New(start),
		}

		require.NoError(t, consumer.Handle(makeAnnouncementMsg(t, "announcement-1", "", sale)))
		require.NoError(t, consumer.Handle(makeAnnouncementMsg(t, "announcement-1", "", sale)))
	})

	t.Run("ORDER.resale_listing_refunded sends the sold email of the refunded listing", func(t *testing.T) {
		t.Parallel()
		notifications := ucmocks.NewMockNotificationUseCase(t)
		// The sold email's send time makes the second run send nothing
		// (SendResaleSold).
		notifications.EXPECT().SendResaleSold(anyCtx, entity.ResaleListingRefundedData{
			ResaleListingID: "listing-1", UserID: "fan-1", OrderID: "order-1", EventID: "event-1",
		}).Return(nil).Twice()
		consumer := event.NewResaleListingRefundedConsumer(notifications, newTestLogger(t))
		listing := &entityv1.ResaleListing{
			Id:      &entityv1.ResaleListingId{Value: "listing-1"},
			UserId:  &entityv1.UserId{Value: "fan-1"},
			OrderId: &entityv1.OrderId{Value: "order-1"},
			EventId: &entityv1.EventId{Value: "event-1"},
		}

		require.NoError(t, consumer.Handle(makeAnnouncementMsg(t, "announcement-1", "", listing)))
		require.NoError(t, consumer.Handle(makeAnnouncementMsg(t, "announcement-1", "", listing)))
	})
}
