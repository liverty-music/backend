package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/entity"
)

// ResaleListingReader reads one ResaleListing. A minimal interface over the
// resale listing repository. Interfaces are defined where consumed (AGENTS.md
// rule).
type ResaleListingReader interface {
	// Get returns the listing with the given id.
	Get(ctx context.Context, id entity.ResaleListingID) (*entity.ResaleListing, error)
}

// SendResaleSold implements [NotificationUseCase]: the sold email, sent at
// most once per listing.
func (uc *notificationUseCase) SendResaleSold(ctx context.Context, refunded entity.ResaleListingRefundedData) error {
	d := uc.orderConfirmation
	listing, err := d.ResaleListings.Get(ctx, entity.ResaleListingID(refunded.ResaleListingID))
	if err != nil {
		return err
	}
	user, err := d.Users.Get(ctx, refunded.UserID)
	if err != nil {
		return err
	}
	order, err := d.Orders.Get(ctx, entity.OrderID(refunded.OrderID))
	if err != nil {
		return err
	}
	concerts, err := d.Concerts.ListByIDs(ctx, []string{refunded.EventID})
	if err != nil {
		return err
	}
	if len(concerts) == 0 {
		return apperr.New(codes.NotFound, "the listing's concert was not found")
	}
	content := resaleSoldContent{
		order: order, concert: concerts[0], tz: d.TimeZone,
		listing: listing,
	}
	return d.ResaleMailer.SendSoldEmail(ctx, listing.ID, user.Email, content.email(emailLanguage(user.PreferredLanguage)))
}

// resaleSoldContent is everything the sold email shows.
type resaleSoldContent struct {
	orderConfirmationContent
	listing *entity.ResaleListing
}

// email renders the sold email in lang ("ja" or "en").
func (c resaleSoldContent) email(lang string) entity.ResaleListingSoldEmail {
	date := c.concert.LocalDate
	card := strings.ToUpper(c.order.Payment.CardBrand)
	var b strings.Builder
	if lang == "en" {
		fmt.Fprintf(&b, "Your ticket was resold through official resale.\n\n")
		fmt.Fprintf(&b, "[Concert]\n%s\nDate: %s  Doors %s / Start %s\nVenue: %s\n\n",
			c.concertTitle(), date.Format("Jan 2, 2006 (Mon)"),
			c.clock(c.concert.OpenTime, "TBA"), c.clock(c.concert.StartTime, "TBA"), c.venueName())
		fmt.Fprintf(&b, "[Resale]\nTickets resold: 1\nPrice: JPY %s (tax included)\nResale fee: JPY %s (tax included)\nRefund: JPY %s (tax included)\n\n",
			yen(c.listing.Price), yen(c.listing.Fee()), yen(c.listing.RefundAmount()))
		fmt.Fprintf(&b, "[Refund]\nThe refund goes to the card you paid with (%s ending in %s). When it appears on your statement depends on your card issuer.\n\n",
			card, c.order.Payment.CardLast4)
		fmt.Fprintf(&b, "[Please note]\nThe resold ticket is no longer valid for entry.\n")
		return entity.ResaleListingSoldEmail{
			Subject:  "[Liverty Music] Your ticket was resold",
			TextBody: b.String(),
		}
	}

	fmt.Fprintf(&b, "公式リセールに出品したチケットが売れました。\n\n")
	fmt.Fprintf(&b, "■ 公演\n%s\n日時: %d/%d/%d(%s) 開場 %s / 開演 %s\n会場: %s\n\n",
		c.concertTitle(), date.Year(), int(date.Month()), date.Day(), weekdaysJA[date.Weekday()],
		c.clock(c.concert.OpenTime, "未定"), c.clock(c.concert.StartTime, "未定"), c.venueName())
	fmt.Fprintf(&b, "■ リセール内容\n枚数: 1枚\n価格: %s円（税込）\nリセール手数料: %s円（税込）\n返金額: %s円（税込）\n\n",
		yen(c.listing.Price), yen(c.listing.Fee()), yen(c.listing.RefundAmount()))
	fmt.Fprintf(&b, "■ 返金について\nご購入時のカード（%s 末尾%s）に返金します。明細に反映される時期はカード会社によって異なります。\n\n",
		card, c.order.Payment.CardLast4)
	fmt.Fprintf(&b, "■ ご注意\nリセールされたチケットは入場に使えなくなりました。\n")
	return entity.ResaleListingSoldEmail{
		Subject:  "【Liverty Music】公式リセールでチケットが売れました",
		TextBody: b.String(),
	}
}
