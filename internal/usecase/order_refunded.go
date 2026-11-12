package usecase

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/entity"
)

// WorkflowNotifyOrderRefunded is the registered name of
// NotificationUseCase.RunNotifyOrderRefunded.
const WorkflowNotifyOrderRefunded = "notify-order-refunded"

// SendOrderRefunded implements [NotificationUseCase].
func (uc *notificationUseCase) SendOrderRefunded(ctx context.Context, orderID entity.OrderID) error {
	if err := uc.workflows.Start(ctx, WorkflowNotifyOrderRefunded,
		orderNoticeWorkflowID(WorkflowNotifyOrderRefunded, orderID), OrderNotice{OrderID: string(orderID)}); err != nil {
		return apperr.Wrap(err, codes.Unavailable, "start the refund notice", slog.String("order_id", string(orderID)))
	}
	return nil
}

// RunNotifyOrderRefunded implements [NotificationUseCase].
func (uc *notificationUseCase) RunNotifyOrderRefunded(ctx context.Context, f Flow, in OrderNotice) (struct{}, error) {
	msg, err := Step(ctx, f, "read", func(ctx context.Context) (orderNoticeMessage, error) {
		return uc.readRefundNotice(ctx, entity.OrderID(in.OrderID))
	})
	if err != nil || !msg.Send {
		return struct{}{}, err
	}
	if _, err := Step(ctx, f, "email", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, uc.orderConfirmation.Mailer.SendRefundEmail(ctx, entity.OrderID(in.OrderID), msg.To, msg.email())
	}); err != nil {
		return struct{}{}, err
	}
	// The push outlives this workflow, which ends right after starting it.
	return struct{}{}, f.Start(context.WithoutCancel(ctx), WorkflowDeliverNotification,
		deliverWorkflowID(entity.NotificationTypeOrderRefunded, in.OrderID), DeliverRequest{
			UserID:  msg.UserID,
			Type:    entity.NotificationTypeOrderRefunded,
			Subject: entity.NotificationSubject{OrderID: in.OrderID},
		})
}

// refundContent is everything the refund copy shows.
type refundContent struct {
	order   *entity.Order
	concert *entity.Concert
	// refunded is the number of tickets the refund paid back; resold the
	// rest of the Order's tickets, refunded at their resale.
	refunded, resold int64
}

// readRefundNotice reads the refund notice of the Order. It sends nothing for
// an Order that no longer exists, that was refunded for a dispute, or whose
// refund amount is 0 because every ticket was resold.
func (uc *notificationUseCase) readRefundNotice(ctx context.Context, orderID entity.OrderID) (orderNoticeMessage, error) {
	d := uc.orderConfirmation
	order, err := d.Orders.Get(ctx, orderID)
	if errors.Is(err, apperr.ErrNotFound) {
		// The Event was deleted with its Orders before the notice read it.
		return orderNoticeMessage{}, nil
	}
	if err != nil {
		return orderNoticeMessage{}, err
	}
	refund := order.Refund
	if refund == nil || refund.Reason != entity.RefundReasonCancellation || refund.Amount <= 0 {
		return orderNoticeMessage{}, nil
	}
	user, err := d.Users.Get(ctx, string(order.UserID))
	if err != nil {
		return orderNoticeMessage{}, err
	}
	eventID, err := d.Orders.GetEventID(ctx, orderID)
	if err != nil {
		return orderNoticeMessage{}, err
	}
	concerts, err := d.Concerts.ListByIDs(ctx, []string{eventID})
	if err != nil {
		return orderNoticeMessage{}, err
	}
	if len(concerts) == 0 {
		return orderNoticeMessage{}, apperr.New(codes.NotFound, "the order's concert was not found",
			slog.String("order_id", string(orderID)))
	}

	// The refund Voided every Ticket, so the number refunded comes from the
	// amount, not from the Tickets.
	content := refundContent{order: order, concert: concerts[0], refunded: order.RefundedTicketCount()}
	content.resold = int64(order.TicketCount) - content.refunded

	email := content.email(emailLanguage(user.PreferredLanguage))
	return orderNoticeMessage{
		Send:     true,
		UserID:   user.ID,
		To:       user.Email,
		Subject:  email.Subject,
		TextBody: email.TextBody,
	}, nil
}

// email renders the refund email in lang ("ja" or "en").
func (c refundContent) email(lang string) entity.OrderEmail {
	date := c.concert.LocalDate
	confirmation := orderConfirmationContent{concert: c.concert}
	card := strings.ToUpper(c.order.Payment.CardBrand)
	amount := c.order.Refund.Amount
	var b strings.Builder
	if lang == "en" {
		fmt.Fprintf(&b, "The price of your tickets for the cancelled concert below has been refunded.\n\n")
		fmt.Fprintf(&b, "[Concert (cancelled)]\n%s\nDate: %s\nVenue: %s\n\n", confirmation.concertTitle(), date.Format("Jan 2, 2006 (Mon)"), confirmation.venueName())
		fmt.Fprintf(&b, "[Refund]\nTickets: %d\nAmount: JPY %s (tax included)\nRefunded to: credit card (%s ending in %s)\nWhen the refund shows on your card statement depends on your card issuer.\n",
			c.refunded, entity.FormatYen(amount), card, c.order.Payment.CardLast4)
		if c.resold > 0 {
			fmt.Fprintf(&b, "\nThe %d ticket(s) you resold through official resale were already refunded when they were sold.\n", c.resold)
		}
		fmt.Fprintf(&b, "\nThe refunded tickets are no longer valid for entry.\n\nOrder number: %s\n", c.order.ID)
		return entity.OrderEmail{Subject: "[Liverty Music] Your refund for the cancelled concert", TextBody: b.String()}
	}
	fmt.Fprintf(&b, "中止となった下記の公演のチケット代金を返金しました。\n\n")
	fmt.Fprintf(&b, "■ 公演（中止）\n%s\n日時: %d/%d/%d(%s)\n会場: %s\n\n",
		confirmation.concertTitle(), date.Year(), int(date.Month()), date.Day(), entity.WeekdaysJA[date.Weekday()], confirmation.venueName())
	fmt.Fprintf(&b, "■ 返金\n枚数: %d枚\n金額: %s円（税込）\n返金先: クレジットカード（%s 末尾%s）\n返金がカードのご利用明細に反映される時期は、カード会社によって異なります。\n",
		c.refunded, entity.FormatYen(amount), card, c.order.Payment.CardLast4)
	if c.resold > 0 {
		fmt.Fprintf(&b, "\n公式リセールで譲渡された%d枚は、リセール成立時に返金済みです。\n", c.resold)
	}
	fmt.Fprintf(&b, "\n返金したチケットは、入場にご利用いただけなくなりました。\n\n注文番号: %s\n", c.order.ID)
	return entity.OrderEmail{Subject: "【Liverty Music】公演中止に伴う返金完了のお知らせ", TextBody: b.String()}
}
