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
// reads and sends with. All are required.
type OrderConfirmationDeps struct {
	Orders         entity.OrderRepository
	Users          entity.UserRepository
	Concerts       ConcertsByIDsReader
	EventOrganizer EventOrganizerRepository
	Organizers     entity.OrganizerRepository
	Mailer         entity.OrderConfirmationSender
	// TimeZone renders the concert and payment times (Asia/Tokyo).
	TimeZone *time.Location
}

// ticketsLinkURL is the deep link that opens the Tickets screen.
const ticketsLinkURL = "/tickets"

// orderConfirmationContent is everything the confirmation copy shows.
type orderConfirmationContent struct {
	order     *entity.Order
	concert   *entity.Concert
	organizer *entity.Organizer
	count     int
	tz        *time.Location
}

// SendOrderConfirmation implements [NotificationUseCase]: the confirmation
// email, sent at most once per Order, then the push notification.
func (uc *notificationUseCase) SendOrderConfirmation(ctx context.Context, paid entity.OrderPaidData) error {
	d := uc.orderConfirmation
	order, err := d.Orders.Get(ctx, entity.OrderID(paid.OrderID))
	if err != nil {
		return err
	}
	buyer, err := d.Users.Get(ctx, paid.BuyerID)
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
		order:     order,
		concert:   concerts[0],
		organizer: organizer,
		count:     paid.TicketCount,
		tz:        d.TimeZone,
	}
	lang := emailLanguage(buyer.PreferredLanguage)
	if err := d.Mailer.SendConfirmationEmail(ctx, order.ID, buyer.Email, content.email(lang)); err != nil {
		return err
	}

	pushLang := copyLanguage(buyer.PreferredLanguage)
	if buyer.PreferredLanguage == "" {
		pushLang = "ja"
	}
	title, body := content.push(pushLang)
	payload := entity.NewNotificationPayload(title, body, ticketsLinkURL, "order-confirmation-"+paid.OrderID)
	if _, err := uc.Deliver(ctx, buyer.ID, entity.NotificationTypeOrderConfirmation, payload); err != nil {
		return err
	}
	return nil
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
	if c.concert.Venue != nil && c.concert.Venue.Name != "" {
		return c.concert.Venue.Name
	}
	if c.concert.ListedVenueName != nil {
		return *c.concert.ListedVenueName
	}
	return ""
}

// clock formats an optional time of day in the concert's time zone.
func (c orderConfirmationContent) clock(t *time.Time, unknown string) string {
	if t == nil {
		return unknown
	}
	return t.In(c.tz).Format("15:04")
}

// yen formats an amount with thousands separators: 6000 -> "6,000".
func yen(amount int64) string {
	s := fmt.Sprintf("%d", amount)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// email renders the confirmation email in lang ("ja" or "en").
func (c orderConfirmationContent) email(lang string) entity.OrderConfirmationEmail {
	date := c.concert.LocalDate
	paid := c.order.PaidTime.In(c.tz)
	card := strings.ToUpper(c.order.Payment.CardBrand)
	seller := c.organizer.SellerDetails
	if seller == nil {
		seller = &entity.SellerDetails{}
	}

	var b strings.Builder
	if lang == "en" {
		fmt.Fprintf(&b, "Thank you for using Liverty Music. Your ticket purchase is complete.\n\n")
		fmt.Fprintf(&b, "[Concert]\n%s\nDate: %s  Doors %s / Start %s\nVenue: %s\n\n",
			c.concertTitle(), date.Format("Jan 2, 2006 (Mon)"),
			c.clock(c.concert.OpenTime, "TBA"), c.clock(c.concert.StartTime, "TBA"), c.venueName())
		fmt.Fprintf(&b, "[Your purchase]\nTickets: %d\nTotal paid: JPY %s (tax included)\nPaid at: %s\nPayment: credit card (%s ending in %s)\nOrder number: %s\n\n",
			c.count, yen(c.order.Amount), paid.Format("Jan 2, 2006 15:04"), card, c.order.Payment.CardLast4, c.order.ID)
		fmt.Fprintf(&b, "[Showing your tickets]\nOpen the Tickets screen of the app and show your tickets to the staff at the entrance.\n\n")
		fmt.Fprintf(&b, "[Please note]\n- Resale of these tickets without the organizer's consent is prohibited.\n- Tickets cannot be cancelled or refunded, except when the concert is cancelled.\n\n")
		fmt.Fprintf(&b, "[Seller (Specified Commercial Transactions Act)]\nSeller: %s\nRepresentative: %s\nAddress: %s\nPhone: %s\nEmail: %s\n",
			seller.LegalName, seller.RepresentativeName, seller.Address, seller.PhoneNumber, seller.ContactEmail)
		return entity.OrderConfirmationEmail{
			Subject:  "[Liverty Music] Your ticket purchase is complete",
			TextBody: b.String(),
		}
	}

	fmt.Fprintf(&b, "Liverty Music をご利用いただきありがとうございます。チケットのご購入が完了しました。\n\n")
	fmt.Fprintf(&b, "■ 公演\n%s\n日時: %d/%d/%d(%s) 開場 %s / 開演 %s\n会場: %s\n\n",
		c.concertTitle(), date.Year(), int(date.Month()), date.Day(), weekdaysJA[date.Weekday()],
		c.clock(c.concert.OpenTime, "未定"), c.clock(c.concert.StartTime, "未定"), c.venueName())
	fmt.Fprintf(&b, "■ ご購入内容\n枚数: %d枚\nお支払い金額: %s円（税込）\n決済日時: %s\nお支払い方法: クレジットカード（%s 末尾%s）\n注文番号: %s\n\n",
		c.count, yen(c.order.Amount), paid.Format("2006/01/02 15:04"), card, c.order.Payment.CardLast4, c.order.ID)
	fmt.Fprintf(&b, "■ チケットの表示方法\nアプリの「チケット」画面からチケットを表示し、入場時にスタッフへご提示ください。\n\n")
	fmt.Fprintf(&b, "■ ご注意\n・本チケットは、主催者の同意のない有償譲渡（転売）が禁止されています。\n・公演中止の場合を除き、ご購入後のキャンセル・返金はできません。\n\n")
	fmt.Fprintf(&b, "■ 販売業者（特定商取引法に基づく表示）\n販売業者: %s\n代表者: %s\n所在地: %s\n電話番号: %s\nメールアドレス: %s\n",
		seller.LegalName, seller.RepresentativeName, seller.Address, seller.PhoneNumber, seller.ContactEmail)
	return entity.OrderConfirmationEmail{
		Subject:  "【Liverty Music】チケットのご購入が完了しました",
		TextBody: b.String(),
	}
}

// push renders the push notification's title and body in lang.
func (c orderConfirmationContent) push(lang string) (title, body string) {
	if lang == "en" {
		return "Ticket purchase complete", fmt.Sprintf("%s: %d ticket(s). Tap to see your tickets.", c.concertTitle(), c.count)
	}
	return "チケットのご購入が完了しました", fmt.Sprintf("%s のチケット%d枚。タップしてチケットを表示", c.concertTitle(), c.count)
}
