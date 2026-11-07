package usecase

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/entity"
)

// ConcertsByIDsReader reads concerts by their event ids. A minimal interface
// over the concert repository. Interfaces are defined where consumed
// (AGENTS.md rule).
type ConcertsByIDsReader interface {
	// ListByIDs retrieves concerts, with their venue and series, by event id.
	ListByIDs(ctx context.Context, ids []string) ([]*entity.Concert, error)
}

// OrderConfirmationDeps are what NotificationUseCase.SendOrderConfirmation
// and SendResaleSold read and send with. All are required in the process that
// sends them (the event consumer).
type OrderConfirmationDeps struct {
	Orders         entity.OrderRepository
	Users          entity.UserRepository
	Concerts       ConcertsByIDsReader
	EventOrganizer EventOrganizerRepository
	Organizers     entity.OrganizerRepository
	Mailer         entity.OrderMailer
	// ResaleListings reads a refunded listing for its sold email.
	ResaleListings ResaleListingReader
	// ResaleMailer sends a listing's sold email at most once.
	ResaleMailer entity.ResaleListingSoldEmailSender
	// Entries reads a Lost lottery entry for its loss email.
	Entries entity.LotteryEntryRepository
	// Stocks reads the TicketStock of a Lost entry's Order: its SeatType and
	// event.
	Stocks entity.TicketStockRepository
	// LossMailer sends a Lost entry's loss email at most once.
	LossMailer entity.LotteryLossEmailSender
	// ResaleEntries reads a Lapsed or Lost resale entry for its ended
	// message.
	ResaleEntries entity.ResaleEntryRepository
	// ResaleEntryMailer sends a resale entry's ended email at most once.
	ResaleEntryMailer entity.ResaleEntryEndedEmailSender
	// Tickets reads the Tickets of a cancelled event's Order for its
	// cancellation notice.
	Tickets entity.TicketRepository
	// TimeZone renders the concert and payment times (Asia/Tokyo).
	TimeZone *time.Location
}

// orderConfirmationContent is everything the confirmation copy shows.
type orderConfirmationContent struct {
	order     *entity.Order
	concert   *entity.Concert
	organizer *entity.Organizer
	count     int
	tz        *time.Location
	// lotteryWin is set for an Order whose source is a LotteryEntry: the
	// copy first says the entry won.
	lotteryWin bool
}

// SendOrderConfirmation implements [NotificationUseCase]: the confirmation
// email, sent at most once per Order, then the push notification.
func (uc *notificationUseCase) SendOrderConfirmation(ctx context.Context, paid entity.OrderPaidData) error {
	d := uc.orderConfirmation
	order, err := d.Orders.Get(ctx, entity.OrderID(paid.OrderID))
	if err != nil {
		return err
	}
	user, err := d.Users.Get(ctx, paid.UserID)
	if err != nil {
		return err
	}
	concerts, err := d.Concerts.ListByIDs(ctx, []string{paid.EventID})
	if err != nil {
		return err
	}
	if len(concerts) == 0 {
		return apperr.New(codes.NotFound, "the order's concert was not found")
	}
	organizerID, err := d.EventOrganizer.GetOrganizerID(ctx, paid.EventID)
	if err != nil {
		return err
	}
	organizer, err := d.Organizers.Get(ctx, organizerID)
	if err != nil {
		return err
	}

	content := orderConfirmationContent{
		order:      order,
		concert:    concerts[0],
		organizer:  organizer,
		count:      paid.TicketCount,
		tz:         d.TimeZone,
		lotteryWin: order.LotteryEntryID != "",
	}
	lang := emailLanguage(user.PreferredLanguage)
	if err := d.Mailer.SendConfirmationEmail(ctx, order.ID, user.Email, content.email(lang)); err != nil {
		return err
	}

	// An Order of a Checkout is not pushed: the fan has just paid on screen.
	typ := entity.NotificationTypeLotteryWin
	switch {
	case order.LotteryEntryID != "":
	case order.ResaleEntryID != "":
		typ = entity.NotificationTypeResaleWin
	default:
		return nil
	}
	// Named after the type and the Order, so a repeated announcement pushes once.
	return uc.startDeliver(ctx, deliverWorkflowID(typ, paid.OrderID), DeliverRequest{
		UserID:  user.ID,
		Type:    typ,
		Subject: entity.NotificationSubject{OrderID: paid.OrderID},
	})
}

// emailLanguage is the email's language: English for an English preference,
// Japanese otherwise (including no preference).
func emailLanguage(preferred string) string {
	if preferred == "en" {
		return "en"
	}
	return "ja"
}

// concertTitle is the concert's series title, or empty.
func (c orderConfirmationContent) concertTitle() string {
	if c.concert.Series == nil {
		return ""
	}
	return c.concert.Series.Title
}

// venueName is the venue's canonical name, falling back to the listed one.
func (c orderConfirmationContent) venueName() string {
	return c.concert.VenueName()
}

// clock formats an optional time of day in the concert's time zone.
func (c orderConfirmationContent) clock(t *time.Time, unknown string) string {
	if t == nil {
		return unknown
	}
	return t.In(c.tz).Format("15:04")
}

// email renders the confirmation email in lang ("ja" or "en").
func (c orderConfirmationContent) email(lang string) entity.OrderConfirmationEmail {
	date := c.concert.LocalDate
	paid := c.order.PayTime.In(c.tz)
	card := strings.ToUpper(c.order.Payment.CardBrand)
	business := c.organizer.BusinessDetails
	if business == nil {
		business = &entity.BusinessDetails{}
	}

	var b strings.Builder
	if lang == "en" {
		subject := "[Liverty Music] Your ticket purchase is complete"
		if c.lotteryWin {
			subject = "[Liverty Music] You won the ticket lottery"
			fmt.Fprintf(&b, "Congratulations! Your lottery entry won, and your tickets are ready.\n\n")
		}
		fmt.Fprintf(&b, "Thank you for using Liverty Music. Your ticket purchase is complete.\n\n")
		fmt.Fprintf(&b, "[Concert]\n%s\nDate: %s  Doors %s / Start %s\nVenue: %s\n\n",
			c.concertTitle(), date.Format("Jan 2, 2006 (Mon)"),
			c.clock(c.concert.OpenTime, "TBA"), c.clock(c.concert.StartTime, "TBA"), c.venueName())
		fmt.Fprintf(&b, "[Your purchase]\nTickets: %d\nTotal paid: JPY %s (tax included)\nPaid at: %s\nPayment: credit card (%s ending in %s)\nOrder number: %s\n\n",
			c.count, entity.FormatYen(c.order.Amount), paid.Format("Jan 2, 2006 15:04"), card, c.order.Payment.CardLast4, c.order.ID)
		fmt.Fprintf(&b, "[Showing your tickets]\nOpen the Tickets screen of the app and show your tickets to the staff at the entrance.\n\n")
		fmt.Fprintf(&b, "[Please note]\n- Resale of these tickets without the organizer's consent is prohibited.\n- Tickets cannot be cancelled or refunded, except when the concert is cancelled.\n\n")
		fmt.Fprintf(&b, "[Sold by (Specified Commercial Transactions Act)]\nBusiness name: %s\nRepresentative: %s\nAddress: %s\nPhone: %s\nEmail: %s\n",
			business.LegalName, business.RepresentativeName, business.BusinessAddress, business.BusinessPhoneNumber, business.ContactEmail)
		return entity.OrderConfirmationEmail{
			Subject:  subject,
			TextBody: b.String(),
		}
	}

	subject := "【Liverty Music】チケットのご購入が完了しました"
	if c.lotteryWin {
		subject = "【Liverty Music】チケット抽選に当選しました"
		fmt.Fprintf(&b, "おめでとうございます。抽選に当選し、チケットのご用意ができました。\n\n")
	}
	fmt.Fprintf(&b, "Liverty Music をご利用いただきありがとうございます。チケットのご購入が完了しました。\n\n")
	fmt.Fprintf(&b, "■ 公演\n%s\n日時: %d/%d/%d(%s) 開場 %s / 開演 %s\n会場: %s\n\n",
		c.concertTitle(), date.Year(), int(date.Month()), date.Day(), entity.WeekdaysJA[date.Weekday()],
		c.clock(c.concert.OpenTime, "未定"), c.clock(c.concert.StartTime, "未定"), c.venueName())
	fmt.Fprintf(&b, "■ ご購入内容\n枚数: %d枚\nお支払い金額: %s円（税込）\n決済日時: %s\nお支払い方法: クレジットカード（%s 末尾%s）\n注文番号: %s\n\n",
		c.count, entity.FormatYen(c.order.Amount), paid.Format("2006/01/02 15:04"), card, c.order.Payment.CardLast4, c.order.ID)
	fmt.Fprintf(&b, "■ チケットの表示方法\nアプリの「チケット」画面からチケットを表示し、入場時にスタッフへご提示ください。\n\n")
	fmt.Fprintf(&b, "■ ご注意\n・本チケットは、主催者の同意のない有償譲渡（転売）が禁止されています。\n・公演中止の場合を除き、ご購入後のキャンセル・返金はできません。\n\n")
	fmt.Fprintf(&b, "■ 販売業者（特定商取引法に基づく表示）\n販売業者: %s\n代表者: %s\n所在地: %s\n電話番号: %s\nメールアドレス: %s\n",
		business.LegalName, business.RepresentativeName, business.BusinessAddress, business.BusinessPhoneNumber, business.ContactEmail)
	return entity.OrderConfirmationEmail{
		Subject:  subject,
		TextBody: b.String(),
	}
}
