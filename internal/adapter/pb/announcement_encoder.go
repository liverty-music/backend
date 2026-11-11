package pb

import (
	"fmt"
	"log/slog"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"google.golang.org/protobuf/proto"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
)

// AnnouncementEncoder turns an announced entity into its entity/v1 message,
// the payload every announcement carries. The outbox
// (internal/infrastructure/messaging) calls it for each announcement a
// repository records; internal/di wires the two.
type AnnouncementEncoder struct{}

// NewAnnouncementEncoder returns an AnnouncementEncoder.
func NewAnnouncementEncoder() AnnouncementEncoder {
	return AnnouncementEncoder{}
}

// Encode returns the entity/v1 message of payload, an announced entity or
// the Concerts of one Series one operation announced together. A User and a
// PushSubscription are encoded without the fields no announcement carries
// ([FromUserForAnnouncement], [FromPushSubscriptionForAnnouncement]).
//
// # Possible errors
//
//   - Internal: payload is not a type that can be announced.
func (AnnouncementEncoder) Encode(subject string, payload any) (proto.Message, error) {
	switch p := payload.(type) {
	case *entity.Artist:
		return FromArtist(p), nil
	case *entity.Checkout:
		return FromCheckout(p), nil
	case []*entity.Concert:
		return &entityv1.ConcertList{Concerts: FromConcerts(p)}, nil
	case *entity.Event:
		return FromEvent(p), nil
	case *entity.Follow:
		return FromFollow(p), nil
	case *entity.LotteryEntry:
		return FromLotteryEntry(p), nil
	case *entity.Order:
		return FromOrder(p), nil
	case *entity.Organizer:
		return FromOrganizer(p), nil
	case *entity.Passkey:
		return FromPasskey(p), nil
	case *entity.PushSubscription:
		return FromPushSubscriptionForAnnouncement(p), nil
	case *entity.ResaleEntry:
		return FromResaleEntry(p, nil), nil
	case *entity.ResaleListing:
		return FromResaleListing(&usecase.ResaleListingAt{Listing: p, Status: p.Status}), nil
	case *entity.SeriesImage:
		return FromSeriesImage(p), nil
	case *entity.Ticket:
		return FromTicket(p), nil
	case *entity.TicketJourney:
		return FromTicketJourney(p), nil
	case *entity.TicketSale:
		return FromTicketSale(p), nil
	case *entity.User:
		return FromUserForAnnouncement(p), nil
	default:
		return nil, apperr.New(codes.Internal, "the payload cannot be announced",
			slog.String("subject", subject), slog.String("type", fmt.Sprintf("%T", payload)))
	}
}
