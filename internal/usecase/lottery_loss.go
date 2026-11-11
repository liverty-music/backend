package usecase

import (
	"context"
	"fmt"
	"strings"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/entity"
)

// lotteryLossContent is everything the loss copy shows.
type lotteryLossContent struct {
	concert  *entity.Concert
	seatType string
	count    int
	reason   entity.LotteryEntryLostReason
}

// SendLotteryLoss implements [NotificationUseCase]: the loss email, sent at
// most once per entry, then the push notification.
func (uc *notificationUseCase) SendLotteryLoss(ctx context.Context, entryID entity.LotteryEntryID) error {
	d := uc.orderConfirmation
	entry, err := d.Entries.Get(ctx, entryID)
	if err != nil {
		return err
	}
	if entry.State != entity.LotteryEntryStateLost {
		return nil
	}
	order, err := d.Orders.Get(ctx, entry.OrderID)
	if err != nil {
		return err
	}
	user, err := d.Users.Get(ctx, string(order.UserID))
	if err != nil {
		return err
	}
	stock, err := d.Stocks.Get(ctx, order.TicketStockID, d.Now())
	if err != nil {
		return err
	}
	concerts, err := d.Concerts.ListByIDs(ctx, []string{stock.EventID})
	if err != nil {
		return err
	}
	if len(concerts) == 0 {
		return apperr.New(codes.NotFound, "the entry's concert was not found")
	}

	content := lotteryLossContent{
		concert:  concerts[0],
		seatType: stock.SeatType.Name,
		count:    order.TicketCount,
		reason:   entry.LostReason,
	}
	if err := d.LossMailer.SendLossEmail(ctx, entry.ID, user.Email, content.email(emailLanguage(user.PreferredLanguage))); err != nil {
		return err
	}

	// Named after the entry, so a repeated announcement pushes once.
	return uc.startDeliver(ctx, deliverWorkflowID(entity.NotificationTypeLotteryLoss, string(entry.ID)), DeliverRequest{
		UserID:  user.ID,
		Type:    entity.NotificationTypeLotteryLoss,
		Subject: entity.NotificationSubject{LotteryEntryID: string(entry.ID)},
	})
}

// concertTitle is the concert's series title, or empty.
func (c lotteryLossContent) concertTitle() string {
	if c.concert.Series == nil {
		return ""
	}
	return c.concert.Series.Title
}

// venueName is the venue's canonical name, falling back to the listed one.
func (c lotteryLossContent) venueName() string {
	return c.concert.VenueName()
}

// email renders the loss email in lang ("ja" or "en").
func (c lotteryLossContent) email(lang string) entity.LotteryLossEmail {
	date := c.concert.LocalDate
	var b strings.Builder
	if lang == "en" {
		var subject string
		switch c.reason {
		case entity.LotteryEntryLostReasonCardNotCharged:
			subject = "[Liverty Music] Your lottery win was cancelled"
			fmt.Fprintf(&b, "Your lottery entry won, but your card could not be charged, so the win was cancelled.\n\n")
		case entity.LotteryEntryLostReasonConcertCancelled:
			subject = "[Liverty Music] Your ticket lottery ended: the concert was cancelled"
			fmt.Fprintf(&b, "The concert was cancelled, so your ticket lottery entry ended.\n\n")
		default:
			subject = "[Liverty Music] Your ticket lottery result"
			fmt.Fprintf(&b, "Thank you for entering the ticket lottery. We are sorry, but your entry was not drawn.\n\n")
		}
		fmt.Fprintf(&b, "[Concert]\n%s\nDate: %s\nVenue: %s\n\n", c.concertTitle(), date.Format("Jan 2, 2006 (Mon)"), c.venueName())
		fmt.Fprintf(&b, "[Your entry]\nSeat type: %s\nTickets: %d\n\n", c.seatType, c.count)
		fmt.Fprintf(&b, "[Payment]\nYour card was not charged. The amount held on your card is given back; it may still show on your card statement for a few days.\n")
		return entity.LotteryLossEmail{Subject: subject, TextBody: b.String()}
	}

	var subject string
	switch c.reason {
	case entity.LotteryEntryLostReasonCardNotCharged:
		subject = "【Liverty Music】当選の取り消しのお知らせ"
		fmt.Fprintf(&b, "抽選に当選しましたが、カードで決済できなかったため、当選を取り消しました。\n\n")
	case entity.LotteryEntryLostReasonConcertCancelled:
		subject = "【Liverty Music】公演中止のお知らせ"
		fmt.Fprintf(&b, "公演が中止になったため、チケット抽選のお申し込みは終了しました。\n\n")
	default:
		subject = "【Liverty Music】チケット抽選の結果のお知らせ"
		fmt.Fprintf(&b, "チケット抽選にお申し込みいただきありがとうございました。誠に残念ながら、落選となりました。\n\n")
	}
	fmt.Fprintf(&b, "■ 公演\n%s\n日時: %d/%d/%d(%s)\n会場: %s\n\n",
		c.concertTitle(), date.Year(), int(date.Month()), date.Day(), entity.WeekdaysJA[date.Weekday()], c.venueName())
	fmt.Fprintf(&b, "■ お申し込み内容\n席種: %s\n枚数: %d枚\n\n", c.seatType, c.count)
	fmt.Fprintf(&b, "■ お支払い\nカードへの請求はありません。カードで確保していた金額は解放されます。カードのご利用明細には数日間表示される場合があります。\n")
	return entity.LotteryLossEmail{Subject: subject, TextBody: b.String()}
}
