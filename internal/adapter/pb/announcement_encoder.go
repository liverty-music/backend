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
	if msg, ok := encodeCatalogue(payload); ok {
		return msg, nil
	}
	if msg, ok := encodeCommerce(payload); ok {
		return msg, nil
	}
	return nil, apperr.New(codes.Internal, "the payload cannot be announced",
		slog.String("subject", subject), slog.String("type", fmt.Sprintf("%T", payload)))
}

// encodeCatalogue encodes the announced types of the catalogue and of the
// accounts: artists, concerts, events, follows, organizers, passkeys, push
// subscriptions, series images and users. ok is false for any other type.
func encodeCatalogue(payload any) (msg proto.Message, ok bool) {
	switch p := payload.(type) {
	case *entity.Artist:
		return FromArtist(p), true
	case []*entity.Concert:
		return &entityv1.ConcertList{Concerts: FromConcerts(p)}, true
	case *entity.Event:
		return FromEvent(p), true
	case *entity.Follow:
		return FromFollow(p), true
	case *entity.Organizer:
		return FromOrganizer(p), true
	case *entity.Passkey:
		return FromPasskey(p), true
	case *entity.PushSubscription:
		return FromPushSubscriptionForAnnouncement(p), true
	case *entity.SeriesImage:
		return FromSeriesImage(p), true
	case *entity.User:
		return FromUserForAnnouncement(p), true
	default:
		return nil, false
	}
}

// encodeCommerce encodes the announced types of ticketing and payment:
// checkouts, lottery entries, orders, resale entries and listings, tickets,
// ticket journeys and ticket sales. ok is false for any other type.
func encodeCommerce(payload any) (msg proto.Message, ok bool) {
	switch p := payload.(type) {
	case *entity.Checkout:
		return FromCheckout(p), true
	case *entity.LotteryEntry:
		return FromLotteryEntry(p), true
	case *entity.Order:
		return FromOrder(p), true
	case *entity.ResaleEntry:
		return FromResaleEntry(p, nil), true
	case *entity.ResaleListing:
		return FromResaleListing(&usecase.ResaleListingAt{Listing: p, Status: p.Status}), true
	case *entity.Ticket:
		return FromTicket(p), true
	case *entity.TicketJourney:
		return FromTicketJourney(p), true
	case *entity.TicketSale:
		return FromTicketSale(p), true
	default:
		return nil, false
	}
}
