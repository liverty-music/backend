package usecase_test

import (
	"context"
	"testing"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/liverty-music/backend/internal/entity"
	entitymocks "github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
)

// resaleSoldFixture holds the mocks of SendResaleSold under test.
type resaleSoldFixture struct {
	listings *entitymocks.MockResaleListingRepository
	users    *entitymocks.MockUserRepository
	orders   *entitymocks.MockOrderRepository
	mailer   *entitymocks.MockResaleListingSoldEmailSender
	uc       usecase.NotificationUseCase
	// emailSent is the listing's sold email send time, set when the send is
	// recorded; nil while none was sent.
	emailSent *time.Time
	// userGone makes the listing's user read as deleted.
	userGone bool
}

var resaleRefunded = entity.ResaleListingRefundedData{ResaleListingID: "l-1", UserID: "fan-1", OrderID: "order-1", EventID: "event-1"}

func newResaleSoldFixture(t *testing.T, lang string) *resaleSoldFixture {
	t.Helper()
	f := &resaleSoldFixture{
		listings: entitymocks.NewMockResaleListingRepository(t),
		users:    entitymocks.NewMockUserRepository(t),
		orders:   entitymocks.NewMockOrderRepository(t),
		mailer:   entitymocks.NewMockResaleListingSoldEmailSender(t),
	}
	start := time.Date(2026, 11, 20, 19, 0, 0, 0, tsJST)
	concert := &entity.Concert{
		ID: "event-1", LocalDate: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC),
		StartTime: tod(start), Venue: &entity.Venue{Name: "Zepp Shinjuku"},
		Series: &entity.Series{Title: "Liverty Live 2026"},
	}
	f.uc = usecase.NewNotificationUseCase(entitymocks.NewMockNotificationRepository(t), entitymocks.NewMockPushSubscriptionRepository(t),
		entitymocks.NewMockPushNotificationSender(t), ucmocks.NewMockWorkflows(t), anyPushMetrics(t), newTestLogger(t),
		usecase.OrderConfirmationDeps{
			Now:    func() time.Time { return soldEmailNow },
			Orders: f.orders, Users: f.users, Concerts: concertReader(t, concert),
			ResaleListings: f.listings, ResaleMailer: f.mailer, TimeZone: tsJST,
		}, usecase.NewConcertsDeps{}, usecase.SubjectDeps{Now: time.Now})
	soldAt := time.Date(2026, 11, 13, 18, 20, 0, 0, tsJST)
	f.listings.EXPECT().Get(mock.Anything, entity.ResaleListingID("l-1")).
		RunAndReturn(func(context.Context, entity.ResaleListingID) (*entity.ResaleListing, error) {
			return &entity.ResaleListing{
				ID: "l-1", OrderID: "order-1", UserID: "fan-1", EventID: "event-1", Price: 5000,
				Status: entity.ResaleListingStatusSold, SellTime: &soldAt, SoldEmailSendTime: f.emailSent,
			}, nil
		}).Maybe()
	f.listings.EXPECT().MarkSoldEmailSent(mock.Anything, entity.ResaleListingID("l-1"), soldEmailNow).
		RunAndReturn(func(context.Context, entity.ResaleListingID, time.Time) error {
			f.emailSent = new(soldEmailNow)
			return nil
		}).Maybe()
	f.users.EXPECT().Get(mock.Anything, "fan-1").
		RunAndReturn(func(context.Context, string) (*entity.User, error) {
			if f.userGone {
				return nil, apperr.New(codes.NotFound, "user not found")
			}
			return &entity.User{ID: "fan-1", Email: "fan@example.com", PreferredLanguage: lang}, nil
		}).Maybe()
	f.orders.EXPECT().Get(mock.Anything, entity.OrderID("order-1")).Return(&entity.Order{
		ID: "order-1", Payment: entity.Payment{CardBrand: "visa", CardLast4: "4242"},
	}, nil).Maybe()
	return f
}

func TestNotificationUseCase_SendResaleSold(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("sold email", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-resale-sold "Sold email"
		f := newResaleSoldFixture(t, "")
		var sent entity.ResaleListingSoldEmail
		f.mailer.EXPECT().SendSoldEmail(mock.Anything, entity.ResaleListingID("l-1"), "fan@example.com", mock.Anything).
			RunAndReturn(func(_ context.Context, _ entity.ResaleListingID, _ string, msg entity.ResaleListingSoldEmail) error {
				sent = msg
				return nil
			}).Once()

		require.NoError(t, f.uc.SendResaleSold(ctx, resaleRefunded))

		assert.Equal(t, "【Liverty Music】公式リセールでチケットが売れました", sent.Subject)
		assert.Contains(t, sent.TextBody, "Liverty Live 2026")
		assert.Contains(t, sent.TextBody, "2026/11/20")
		assert.Contains(t, sent.TextBody, "Zepp Shinjuku")
		assert.Contains(t, sent.TextBody, "公式リセール")
		assert.Contains(t, sent.TextBody, "価格: 5,000円（税込）")
		assert.Contains(t, sent.TextBody, "リセール手数料: 500円（税込）")
		assert.Contains(t, sent.TextBody, "返金額: 4,500円（税込）")
		assert.Contains(t, sent.TextBody, "VISA 末尾4242")
		assert.Contains(t, sent.TextBody, "カード会社によって異なります")
		assert.Contains(t, sent.TextBody, "入場に使えなくなりました")
	})

	t.Run("sold email in English", func(t *testing.T) {
		t.Parallel()
		f := newResaleSoldFixture(t, "en")
		f.mailer.EXPECT().SendSoldEmail(mock.Anything, entity.ResaleListingID("l-1"), "fan@example.com",
			mock.MatchedBy(func(msg entity.ResaleListingSoldEmail) bool {
				return msg.Subject == "[Liverty Music] Your ticket was resold"
			})).Return(nil)

		require.NoError(t, f.uc.SendResaleSold(ctx, resaleRefunded))
	})

	t.Run("listing announced twice", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-resale-sold "Listing announced twice"
		// The first delivery sends and records the email; the second reads the
		// recorded send time and sends nothing.
		f := newResaleSoldFixture(t, "")
		f.mailer.EXPECT().SendSoldEmail(mock.Anything, entity.ResaleListingID("l-1"), "fan@example.com", mock.Anything).Return(nil).Once()

		require.NoError(t, f.uc.SendResaleSold(ctx, resaleRefunded))
		require.NoError(t, f.uc.SendResaleSold(ctx, resaleRefunded))
	})

	t.Run("listing's user deleted", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-resale-sold "Listing's user deleted"
		f := newResaleSoldFixture(t, "")
		f.userGone = true

		// No SendSoldEmail expectation: sending one would fail the test.
		require.NoError(t, f.uc.SendResaleSold(ctx, resaleRefunded))
		assert.Nil(t, f.emailSent, "no email was sent, so none is recorded")
	})

	t.Run("mail unavailable", func(t *testing.T) {
		t.Parallel()
		// @spec components/usecase/notification/send-resale-sold "Mail unavailable"
		f := newResaleSoldFixture(t, "")
		f.mailer.EXPECT().SendSoldEmail(mock.Anything, entity.ResaleListingID("l-1"), "fan@example.com", mock.Anything).
			Return(apperr.New(codes.Unavailable, "postmark down"))

		err := f.uc.SendResaleSold(ctx, resaleRefunded)

		assert.ErrorIs(t, err, apperr.ErrUnavailable)
		assert.Nil(t, f.emailSent, "a failed send is not recorded")
	})
}

// soldEmailNow is the time the sold email tests' clock reads.
var soldEmailNow = time.Date(2026, 11, 13, 9, 30, 0, 0, time.UTC)
