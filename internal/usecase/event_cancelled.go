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

// WorkflowNotifyEventCancelled is the registered name of
// NotificationUseCase.RunNotifyEventCancelled.
const WorkflowNotifyEventCancelled = "notify-event-cancelled"

// OrderNotice is the input of the order notice workflows
// (notify-event-cancelled, notify-order-refunded): the Order the notice is
// for.
type OrderNotice struct {
	// OrderID is the Order the notice is for.
	OrderID string `json:"order_id"`
}

// orderNoticeMessage is what the read step of an order notice workflow
// records: the finished email and push of one Order, or Send false when
// nothing is sent for it. It is recorded as JSON, so a resumed run sends what
// the first run read even after the Order changed or was deleted.
type orderNoticeMessage struct {
	// Send is false when nothing is sent for the Order.
	Send bool `json:"send"`
	// UserID is the Order's user.
	UserID string `json:"user_id,omitempty"`
	// To is the user's email address.
	To string `json:"to,omitempty"`
	// Subject is the email subject line.
	Subject string `json:"subject,omitempty"`
	// TextBody is the plain-text email body.
	TextBody string `json:"text_body,omitempty"`
	// Push is the push notification.
	Push *entity.NotificationPayload `json:"push,omitempty"`
}

// email returns the recorded email.
func (m orderNoticeMessage) email() entity.OrderEmail {
	return entity.OrderEmail{Subject: m.Subject, TextBody: m.TextBody}
}

// orderNoticeWorkflowID names an order notice workflow after the Order, so a
// repeated announcement starts nothing new.
func orderNoticeWorkflowID(workflow string, orderID entity.OrderID) string {
	return workflow + ":" + string(orderID)
}

// SendEventCancelled implements [NotificationUseCase].
func (uc *notificationUseCase) SendEventCancelled(ctx context.Context, eventID string) error {
	orders, err := uc.orderConfirmation.Orders.ListByEvent(ctx, eventID)
	if err != nil {
		return err
	}
	var errs []error
	for _, o := range orders {
		if o.Status != entity.OrderStatusPaid {
			continue
		}
		// A failure to start one Order's notice does not stop the others; the
		// repeated delivery starts only the ones that are missing.
		if err := uc.workflows.Start(ctx, WorkflowNotifyEventCancelled,
			orderNoticeWorkflowID(WorkflowNotifyEventCancelled, o.ID), OrderNotice{OrderID: string(o.ID)}); err != nil {
			errs = append(errs, apperr.Wrap(err, codes.Unavailable, "start the cancellation notice",
				slog.String("event_id", eventID), slog.String("order_id", string(o.ID))))
		}
	}
	return errors.Join(errs...)
}

// RunNotifyEventCancelled implements [NotificationUseCase].
func (uc *notificationUseCase) RunNotifyEventCancelled(ctx context.Context, f Flow, in OrderNotice) (struct{}, error) {
	msg, err := Step(ctx, f, "read", func(ctx context.Context) (orderNoticeMessage, error) {
		return uc.readCancellationNotice(ctx, entity.OrderID(in.OrderID))
	})
	if err != nil || !msg.Send {
		return struct{}{}, err
	}
	if _, err := Step(ctx, f, "email", func(ctx context.Context) (struct{}, error) {
		return struct{}{}, uc.orderConfirmation.Mailer.SendCancellationEmail(ctx, entity.OrderID(in.OrderID), msg.To, msg.email())
	}); err != nil {
		return struct{}{}, err
	}
	// The push outlives this workflow, which ends right after starting it.
	return struct{}{}, f.Start(context.WithoutCancel(ctx), WorkflowDeliverNotification,
		deliverWorkflowID(entity.NotificationTypeEventCancelled, in.OrderID), DeliverRequest{
			UserID:  msg.UserID,
			Type:    entity.NotificationTypeEventCancelled,
			Payload: msg.Push,
		})
}

// cancellationContent is everything the cancellation copy shows.
type cancellationContent struct {
	order   *entity.Order
	concert *entity.Concert
	// issued is the number of the Order's Tickets still Issued; resold the
	// number Voided by an official resale.
	issued, resold int64
}

// readCancellationNotice reads the cancellation notice of the Order. It sends
// nothing for an Order that no longer exists, is no longer Paid or holds no
// Issued Ticket.
func (uc *notificationUseCase) readCancellationNotice(ctx context.Context, orderID entity.OrderID) (orderNoticeMessage, error) {
	d := uc.orderConfirmation
	order, err := d.Orders.Get(ctx, orderID)
	if errors.Is(err, apperr.ErrNotFound) {
		// The Event was deleted with its Orders before the notice read it.
		return orderNoticeMessage{}, nil
	}
	if err != nil {
		return orderNoticeMessage{}, err
	}
	if order.Status != entity.OrderStatusPaid {
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
	tickets, err := d.Tickets.ListByUserAndEvent(ctx, order.UserID, eventID)
	if err != nil {
		return orderNoticeMessage{}, err
	}
	content := cancellationContent{order: order, concert: concerts[0]}
	for _, t := range tickets {
		if t.OrderID != order.ID {
			continue
		}
		if t.Status == entity.TicketStatusIssued {
			content.issued++
		} else {
			content.resold++
		}
	}
	// Every Ticket resold: nothing of the Order is refunded.
	if content.issued == 0 {
		return orderNoticeMessage{}, nil
	}

	email := content.email(emailLanguage(user.PreferredLanguage))
	title, body := content.push(pushLanguage(user.PreferredLanguage))
	return orderNoticeMessage{
		Send:     true,
		UserID:   user.ID,
		To:       user.Email,
		Subject:  email.Subject,
		TextBody: email.TextBody,
		Push: entity.NewNotificationPayload(title, body, concertDeepLink(content.concert),
			string(entity.NotificationTypeEventCancelled)+"-"+string(order.ID)),
	}, nil
}

// pushLanguage is the push notification's language: the user's preference,
// Japanese when none.
func pushLanguage(preferred string) string {
	if preferred == "" {
		return "ja"
	}
	return copyLanguage(preferred)
}

// pricePerTicket is the Order's amount divided by its ticket count.
func pricePerTicket(o *entity.Order) int64 {
	if o.TicketCount <= 0 {
		return o.Amount
	}
	return o.Amount / int64(o.TicketCount)
}

// concertTitle is the concert's series title, or empty.
func (c cancellationContent) concertTitle() string {
	return orderConfirmationContent{concert: c.concert}.concertTitle()
}

// email renders the cancellation email in lang ("ja" or "en").
func (c cancellationContent) email(lang string) entity.OrderEmail {
	date := c.concert.LocalDate
	venue := orderConfirmationContent{concert: c.concert}.venueName()
	card := strings.ToUpper(c.order.Payment.CardBrand)
	refund := pricePerTicket(c.order) * c.issued
	var b strings.Builder
	if lang == "en" {
		fmt.Fprintf(&b, "We are sorry to tell you that the following concert has been cancelled.\n\n")
		fmt.Fprintf(&b, "[Concert]\n%s\nDate: %s\nVenue: %s\n\n", c.concertTitle(), date.Format("Jan 2, 2006 (Mon)"), venue)
		fmt.Fprintf(&b, "[Refund]\nTickets: %d\nAmount: JPY %s (tax included)\nThis amount is refunded to the card you paid with (%s ending in %s). We will email you again once the refund is made.\n",
			c.issued, yen(refund), card, c.order.Payment.CardLast4)
		if c.resold > 0 {
			fmt.Fprintf(&b, "\nThe %d ticket(s) you resold through official resale were already refunded when they were sold.\n", c.resold)
		}
		fmt.Fprintf(&b, "\nOrder number: %s\n", c.order.ID)
		return entity.OrderEmail{Subject: "[Liverty Music] Concert cancelled: your tickets will be refunded", TextBody: b.String()}
	}
	fmt.Fprintf(&b, "誠に残念ながら、下記の公演は中止となりました。\n\n")
	fmt.Fprintf(&b, "■ 公演\n%s\n日時: %d/%d/%d(%s)\n会場: %s\n\n",
		c.concertTitle(), date.Year(), int(date.Month()), date.Day(), weekdaysJA[date.Weekday()], venue)
	fmt.Fprintf(&b, "■ 返金\n枚数: %d枚\n金額: %s円（税込）\nこの金額を、お支払いいただいたカード（%s 末尾%s）に返金します。返金が完了しましたら、あらためてメールでお知らせします。\n",
		c.issued, yen(refund), card, c.order.Payment.CardLast4)
	if c.resold > 0 {
		fmt.Fprintf(&b, "\n公式リセールで譲渡された%d枚は、リセール成立時に返金済みです。\n", c.resold)
	}
	fmt.Fprintf(&b, "\n注文番号: %s\n", c.order.ID)
	return entity.OrderEmail{Subject: "【Liverty Music】公演中止と返金のお知らせ", TextBody: b.String()}
}

// push renders the push notification's title and body in lang.
func (c cancellationContent) push(lang string) (title, body string) {
	if lang == "en" {
		return "Concert cancelled", fmt.Sprintf("%s has been cancelled. Your tickets will be refunded to your card.", c.concertTitle())
	}
	return "公演中止のお知らせ", fmt.Sprintf("%s は中止となりました。チケット代金はカードに返金します。", c.concertTitle())
}
