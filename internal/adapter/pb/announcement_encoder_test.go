package pb_test

import (
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
)

// TestAnnouncementEncoder_Encode encodes one payload of every type rdb's
// announced constraint lists; a type added there must be added here and to
// the encoder.
func TestAnnouncementEncoder_Encode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		payload any
		check   func(t *testing.T, m proto.Message)
	}{
		{"Artist", &entity.Artist{ID: "a"}, func(t *testing.T, m proto.Message) {
			assert.Equal(t, "a", m.(*entityv1.Artist).GetId().GetValue())
		}},
		{"Checkout", &entity.Checkout{ID: "c"}, func(t *testing.T, m proto.Message) {
			assert.Equal(t, "c", m.(*entityv1.Checkout).GetId().GetValue())
		}},
		{"Concerts of one Series", []*entity.Concert{{ID: "e1"}, {ID: "e2"}}, func(t *testing.T, m proto.Message) {
			list := m.(*entityv1.ConcertList)
			require.Len(t, list.GetConcerts(), 2)
			assert.Equal(t, "e2", list.GetConcerts()[1].GetEvent().GetId().GetValue())
		}},
		{"Event", &entity.Event{ID: "e"}, func(t *testing.T, m proto.Message) {
			assert.Equal(t, "e", m.(*entityv1.Event).GetId().GetValue())
		}},
		{"Follow", &entity.Follow{UserID: "u", ArtistID: "a"}, func(t *testing.T, m proto.Message) {
			assert.Equal(t, "a", m.(*entityv1.Follow).GetArtistId().GetValue())
		}},
		{"LotteryEntry", &entity.LotteryEntry{ID: "l"}, func(t *testing.T, m proto.Message) {
			assert.Equal(t, "l", m.(*entityv1.LotteryEntry).GetId().GetValue())
		}},
		{"Order", &entity.Order{ID: "o"}, func(t *testing.T, m proto.Message) {
			assert.Equal(t, "o", m.(*entityv1.Order).GetId().GetValue())
		}},
		{"Organizer", &entity.Organizer{ID: "org"}, func(t *testing.T, m proto.Message) {
			assert.Equal(t, "org", m.(*entityv1.Organizer).GetId().GetValue())
		}},
		{"Passkey", &entity.Passkey{ProviderPasskeyID: "p"}, func(t *testing.T, m proto.Message) {
			assert.Equal(t, "p", m.(*entityv1.Passkey).GetId().GetValue())
		}},
		{"PushSubscription without its push address and keys", &entity.PushSubscription{ID: "s", Endpoint: "https://push.example/x", P256dh: "k", Auth: "a"}, func(t *testing.T, m proto.Message) {
			sub := m.(*entityv1.PushSubscription)
			assert.Equal(t, "s", sub.GetId().GetValue())
			assert.Empty(t, sub.GetEndpoint().GetValue())
		}},
		{"ResaleEntry", &entity.ResaleEntry{ID: "r"}, func(t *testing.T, m proto.Message) {
			assert.Equal(t, "r", m.(*entityv1.ResaleEntry).GetId().GetValue())
		}},
		{"ResaleListing with its status", &entity.ResaleListing{ID: "rl", Status: entity.ResaleListingStatusSold}, func(t *testing.T, m proto.Message) {
			l := m.(*entityv1.ResaleListing)
			assert.Equal(t, "rl", l.GetId().GetValue())
			assert.Equal(t, entityv1.ResaleListingStatus_RESALE_LISTING_STATUS_SOLD, l.GetStatus())
		}},
		{"SeriesImage", &entity.SeriesImage{ID: "img", OrganizerID: "org"}, func(t *testing.T, m proto.Message) {
			assert.Equal(t, "img", m.(*entityv1.SeriesImage).GetId().GetValue())
		}},
		{"Ticket", &entity.Ticket{ID: "t"}, func(t *testing.T, m proto.Message) {
			assert.Equal(t, "t", m.(*entityv1.Ticket).GetId().GetValue())
		}},
		{"TicketJourney", &entity.TicketJourney{UserID: "u", EventID: "e"}, func(t *testing.T, m proto.Message) {
			assert.Equal(t, "e", m.(*entityv1.TicketJourney).GetEventId().GetValue())
		}},
		{"TicketSale", &entity.TicketSale{ID: "ts"}, func(t *testing.T, m proto.Message) {
			assert.Equal(t, "ts", m.(*entityv1.TicketSale).GetId().GetValue())
		}},
		{"User without its identity-provider display name", &entity.User{ID: "u", Name: "Display Name"}, func(t *testing.T, m proto.Message) {
			u := m.(*entityv1.User)
			assert.Equal(t, "u", u.GetId().GetValue())
			assert.Empty(t, u.GetName())
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m, err := pb.NewAnnouncementEncoder().Encode("SUBJECT.test", tt.payload)
			require.NoError(t, err)
			tt.check(t, m)
		})
	}

	t.Run("fails an unknown type with Internal", func(t *testing.T) {
		t.Parallel()
		_, err := pb.NewAnnouncementEncoder().Encode("SUBJECT.test", &entity.Venue{ID: "v"})
		assert.ErrorIs(t, err, apperr.ErrInternal)
	})
}
