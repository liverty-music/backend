package pb_test

import (
	"testing"
	"time"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/adapter/pb"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFromLotteryEntry_LostReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		state  entity.LotteryEntryState
		reason entity.LotteryEntryLostReason
		want   entityv1.LotteryEntryLostReason
	}{
		{name: "not drawn", state: entity.LotteryEntryStateLost, reason: entity.LotteryEntryLostReasonNotDrawn, want: entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_NOT_DRAWN},
		{name: "card not charged", state: entity.LotteryEntryStateLost, reason: entity.LotteryEntryLostReasonCardNotCharged, want: entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_CARD_NOT_CHARGED},
		{name: "won has no reason", state: entity.LotteryEntryStateWon, want: entityv1.LotteryEntryLostReason_LOTTERY_ENTRY_LOST_REASON_UNSPECIFIED},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := pb.FromLotteryEntry(&entity.LotteryEntry{ID: "e", OrderID: "o", TicketSaleID: "s", State: tt.state, LostReason: tt.reason})
			assert.Equal(t, tt.want, got.GetLostReason())
		})
	}
}

func TestFromUser_LoginAndTimeZone(t *testing.T) {
	t.Parallel()

	login := time.Date(2026, 10, 28, 1, 0, 0, 0, time.UTC)
	got := pb.FromUser(&entity.User{ID: "u", Email: "fan@example.com", TimeZone: "Asia/Tokyo", LastLoginTime: &login})

	assert.Equal(t, "Asia/Tokyo", got.GetTimeZone().GetValue())
	assert.True(t, got.GetLastLoginTime().AsTime().Equal(login))
}

func TestFromUserForAnnouncement(t *testing.T) {
	t.Parallel()

	user := &entity.User{
		ID:          "u",
		Email:       "fan@example.com",
		Name:        "Taro Yamada",
		FullName:    "山田 太郎",
		PhoneNumber: "+819012345678",
	}

	got := pb.FromUserForAnnouncement(user)

	assert.Equal(t, "u", got.GetId().GetValue())
	assert.Equal(t, "fan@example.com", got.GetEmail().GetValue())
	assert.Nil(t, got.GetFullName())
	assert.Nil(t, got.GetPhoneNumber())
	assert.Empty(t, got.GetName())
	assert.Equal(t, "Taro Yamada", user.Name, "the entity is left unchanged")
}

func TestFromPasskey_NameAndUser(t *testing.T) {
	t.Parallel()

	name := "iPhone"
	got := pb.FromPasskey(&entity.Passkey{ProviderPasskeyID: "pk", UserID: "u", AAGUID: entity.ZeroAAGUID, CreateTime: time.Now(), Name: &name})

	assert.Equal(t, "iPhone", got.GetName().GetValue())
	assert.Equal(t, "u", got.GetUserId().GetValue())

	unnamed := pb.FromPasskey(&entity.Passkey{ProviderPasskeyID: "pk", UserID: "u", AAGUID: entity.ZeroAAGUID, CreateTime: time.Now()})
	assert.Nil(t, unnamed.GetName())
}

func TestFromArtist_Organizer(t *testing.T) {
	t.Parallel()

	org := "org-1"
	assert.Equal(t, "org-1", pb.FromArtist(&entity.Artist{ID: "a", OrganizerID: &org}).GetOrganizerId().GetValue())
	assert.Nil(t, pb.FromArtist(&entity.Artist{ID: "a"}).GetOrganizerId())
}

func TestFromResaleListing_References(t *testing.T) {
	t.Parallel()

	withdrawn := time.Date(2026, 10, 28, 1, 0, 0, 0, time.UTC)
	got := pb.FromResaleListing(&usecase.ResaleListingAt{Listing: &entity.ResaleListing{
		ID:               "l",
		OrderID:          "o",
		UserID:           "u",
		EventID:          "ev",
		WithdrawTime:     &withdrawn,
		ProviderRefundID: "re_1",
	}})

	assert.Equal(t, "o", got.GetOrderId().GetValue())
	assert.Equal(t, "u", got.GetUserId().GetValue())
	assert.Equal(t, "ev", got.GetEventId().GetValue())
	assert.True(t, got.GetWithdrawTime().AsTime().Equal(withdrawn))
	assert.Equal(t, "re_1", got.GetProviderRefundId().GetValue())
}

func TestFromSeries_CoverImageOrganizer(t *testing.T) {
	t.Parallel()

	b := pb.NewSeriesImageURLBuilder("https://cdn.example.com")
	got := b.FromSeries(&entity.Series{ID: "s", CoverImage: &entity.SeriesImage{ID: "img", OrganizerID: "org-1"}})

	assert.Equal(t, "org-1", got.GetCoverImage().GetOrganizerId().GetValue())
}

func TestFromFollow(t *testing.T) {
	t.Parallel()

	got := pb.FromFollow(&entity.Follow{UserID: "u", ArtistID: "a", Hype: entity.HypeAway})

	assert.Equal(t, "u", got.GetUserId().GetValue())
	assert.Equal(t, "a", got.GetArtistId().GetValue())
	assert.Equal(t, entityv1.HypeType_HYPE_TYPE_AWAY, got.GetHype())
}

func TestFromPushSubscription(t *testing.T) {
	t.Parallel()

	sub := &entity.PushSubscription{ID: "p", UserID: "u", Endpoint: "https://web.push.apple.com/sub/xyz", P256dh: "key", Auth: "secret"}

	full := pb.FromPushSubscription(sub)
	assert.Equal(t, entityv1.PushDeviceFamily_PUSH_DEVICE_FAMILY_APPLE, full.GetDeviceFamily())
	assert.Equal(t, "https://web.push.apple.com/sub/xyz", full.GetEndpoint().GetValue())

	announced := pb.FromPushSubscriptionForAnnouncement(sub)
	require.NotNil(t, announced)
	assert.Equal(t, "p", announced.GetId().GetValue())
	assert.Equal(t, "u", announced.GetUserId().GetValue())
	assert.Equal(t, entityv1.PushDeviceFamily_PUSH_DEVICE_FAMILY_APPLE, announced.GetDeviceFamily())
	assert.Nil(t, announced.GetEndpoint())
	assert.Nil(t, announced.GetKeys())
}
