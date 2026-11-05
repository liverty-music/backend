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

// resaleEntriesLinkURL is the deep link that opens the fan's resale entries.
const resaleEntriesLinkURL = "/resale-entries"

// resaleEntryEndedContent is everything the lapse and loss copy shows.
type resaleEntryEndedContent struct {
	concert  *entity.Concert
	seatType string
	count    int
	state    entity.ResaleEntryState
	reason   entity.ResaleEntryLostReason
	// resaleOpen is true while the event's resale is still open.
	resaleOpen bool
}

// SendResaleEntryEnded implements [NotificationUseCase].
func (uc *notificationUseCase) SendResaleEntryEnded(ctx context.Context, entryID entity.ResaleEntryID) error {
	d := uc.orderConfirmation
	entry, err := d.ResaleEntries.Get(ctx, entryID)
	if err != nil {
		return err
	}
	if entry.State != entity.ResaleEntryStateLapsed && entry.State != entity.ResaleEntryStateLost {
		return nil
	}
	user, err := d.Users.Get(ctx, string(entry.Order.UserID))
	if err != nil {
		return err
	}
	now := time.Now()
	stock, err := d.Stocks.Get(ctx, entry.Order.TicketStockID, now)
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
	content := resaleEntryEndedContent{
		concert:  concerts[0],
		seatType: stock.SeatType.Name,
		count:    entry.Order.TicketCount,
		state:    entry.State,
		reason:   entry.LostReason,
	}
	if deadline, ok := entity.ResaleDeadlineOf(&concerts[0].Event); ok {
		content.resaleOpen = now.Before(deadline) && !concerts[0].HasPublishState(entity.PublishStateCancelled)
	}
	if err := d.ResaleEntryMailer.SendEndedEmail(ctx, entry.ID, user.Email, content.email(emailLanguage(user.PreferredLanguage))); err != nil {
		return err
	}

	pushLang := copyLanguage(user.PreferredLanguage)
	if user.PreferredLanguage == "" {
		pushLang = "ja"
	}
	notificationType := entity.NotificationTypeResaleEntryLost
	if entry.State == entity.ResaleEntryStateLapsed {
		notificationType = entity.NotificationTypeResaleEntryLapsed
	}
	title, body := content.push(pushLang)
	payload := entity.NewNotificationPayload(title, body, resaleEntriesLinkURL, string(notificationType)+"-"+string(entry.ID))
	// Named after the type and the entry, so a repeated announcement pushes
	// once.
	return uc.startDeliver(ctx, deliverWorkflowID(notificationType, string(entry.ID)), DeliverRequest{
		UserID:  user.ID,
		Type:    notificationType,
		Payload: payload,
	})
}

// concertTitle is the concert's series title, or empty.
func (c resaleEntryEndedContent) concertTitle() string {
	if c.concert.Series == nil {
		return ""
	}
	return c.concert.Series.Title
}

// lead is the first paragraph, which says why the entry ended, and
// whether the fan is invited to apply again.
func (c resaleEntryEndedContent) lead(lang string) (subject, lead string, applyAgain bool) {
	en := lang == "en"
	switch {
	case c.state == entity.ResaleEntryStateLapsed:
		if en {
			return "[Liverty Music] Your official resale application has expired",
				"The card authorization for your official resale application expired before a ticket was found, so your application ended.", c.resaleOpen
		}
		return "【Liverty Music】公式リセールのお申し込みの期限切れのお知らせ",
			"公式リセールのお申し込みは、チケットが見つかる前にカードの与信の有効期限が来たため終了しました。", c.resaleOpen
	case c.reason == entity.ResaleEntryLostReasonConcertCancelled:
		if en {
			return "[Liverty Music] Official resale ended: the concert was cancelled",
				"The concert was cancelled, so your official resale application ended.", false
		}
		return "【Liverty Music】公演中止による公式リセールの終了のお知らせ",
			"公演が中止となったため、公式リセールのお申し込みは終了しました。", false
	case c.reason == entity.ResaleEntryLostReasonCardNotCharged:
		if en {
			return "[Liverty Music] Your official resale win was cancelled",
				"Your official resale application won, but your card could not be charged, so the win was cancelled.", c.resaleOpen
		}
		return "【Liverty Music】公式リセールの当選の取り消しのお知らせ",
			"公式リセールに当選しましたが、カードで決済できなかったため、当選を取り消しました。", c.resaleOpen
	case c.reason == entity.ResaleEntryLostReasonPaymentNotCompletedBeforeShow:
		if en {
			return "[Liverty Music] Your official resale win was cancelled",
				"Your official resale application won, but because of a problem at the payment provider the payment could not be completed before the show, so the win was cancelled.", false
		}
		return "【Liverty Music】公式リセールの当選の取り消しのお知らせ",
			"公式リセールに当選しましたが、決済事業者の障害により開演までに決済を完了できなかったため、当選を取り消しました。", false
	default:
		if en {
			return "[Liverty Music] Your official resale result",
				"No resale ticket was left for your application at the last draw.", false
		}
		return "【Liverty Music】公式リセールの抽選結果のお知らせ",
			"最終抽選までにお申し込みに割り当てられるリセールチケットがありませんでした。", false
	}
}

// email renders the ended email in lang ("ja" or "en").
func (c resaleEntryEndedContent) email(lang string) entity.ResaleEntryEndedEmail {
	subject, lead, applyAgain := c.lead(lang)
	date := c.concert.LocalDate
	venue := orderConfirmationContent{concert: c.concert}.venueName()
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", lead)
	if lang == "en" {
		fmt.Fprintf(&b, "[Concert]\n%s\nDate: %s\nVenue: %s\n\n", c.concertTitle(), date.Format("Jan 2, 2006 (Mon)"), venue)
		fmt.Fprintf(&b, "[Your application]\nSeat type: %s\nTickets: %d\n\n", c.seatType, c.count)
		fmt.Fprintf(&b, "[Payment]\nYour card was not charged. The amount held on your card is given back; it may still show on your card statement for a few days.\n")
		switch {
		case applyAgain && c.reason == entity.ResaleEntryLostReasonCardNotCharged:
			fmt.Fprintf(&b, "\nResale for this concert is still open. You can apply again with another card:\n%s\n", resaleEntriesLinkURL)
		case applyAgain:
			fmt.Fprintf(&b, "\nResale for this concert is still open. You can apply again:\n%s\n", resaleEntriesLinkURL)
		case c.state == entity.ResaleEntryStateLost && c.reason != entity.ResaleEntryLostReasonConcertCancelled:
			fmt.Fprintf(&b, "\nResale for this concert has closed.\n")
		}
		return entity.ResaleEntryEndedEmail{Subject: subject, TextBody: b.String()}
	}
	fmt.Fprintf(&b, "■ 公演\n%s\n日時: %d/%d/%d(%s)\n会場: %s\n\n",
		c.concertTitle(), date.Year(), int(date.Month()), date.Day(), weekdaysJA[date.Weekday()], venue)
	fmt.Fprintf(&b, "■ お申し込み内容\n席種: %s\n枚数: %d枚\n\n", c.seatType, c.count)
	fmt.Fprintf(&b, "■ お支払い\nカードへの請求はありません。カードで確保していた金額は解放されます。カードのご利用明細には数日間表示される場合があります。\n")
	switch {
	case applyAgain && c.reason == entity.ResaleEntryLostReasonCardNotCharged:
		fmt.Fprintf(&b, "\nこの公演の公式リセールは受付中です。別のカードで再度お申し込みいただけます。\n%s\n", resaleEntriesLinkURL)
	case applyAgain:
		fmt.Fprintf(&b, "\nこの公演の公式リセールは受付中です。再度お申し込みいただけます。\n%s\n", resaleEntriesLinkURL)
	case c.state == entity.ResaleEntryStateLost && c.reason != entity.ResaleEntryLostReasonConcertCancelled:
		fmt.Fprintf(&b, "\nこの公演の公式リセールの受付は終了しました。\n")
	}
	return entity.ResaleEntryEndedEmail{Subject: subject, TextBody: b.String()}
}

// push renders the push notification's title and body in lang.
func (c resaleEntryEndedContent) push(lang string) (title, body string) {
	_, _, applyAgain := c.lead(lang)
	if lang == "en" {
		switch {
		case c.state == entity.ResaleEntryStateLapsed:
			title, body = "Resale application expired", "Your card authorization expired. You were not charged."
		case c.reason == entity.ResaleEntryLostReasonConcertCancelled:
			title, body = "Resale ended", "The concert was cancelled. You were not charged."
		case c.reason == entity.ResaleEntryLostReasonCardNotCharged, c.reason == entity.ResaleEntryLostReasonPaymentNotCompletedBeforeShow:
			title, body = "Resale win cancelled", "The payment could not be completed, so the win was cancelled. You were not charged."
		default:
			title, body = "Resale result", "No ticket was allocated. You were not charged."
		}
		if applyAgain {
			body += " You can apply again."
		}
		return title, c.concertTitle() + ": " + body
	}
	switch {
	case c.state == entity.ResaleEntryStateLapsed:
		title, body = "公式リセールの期限切れ", "カードの与信の有効期限が来たため申し込みが終了しました。請求はありません。"
	case c.reason == entity.ResaleEntryLostReasonConcertCancelled:
		title, body = "公式リセールの終了", "公演中止のため申し込みが終了しました。請求はありません。"
	case c.reason == entity.ResaleEntryLostReasonCardNotCharged, c.reason == entity.ResaleEntryLostReasonPaymentNotCompletedBeforeShow:
		title, body = "公式リセールの当選取り消し", "決済を完了できなかったため当選を取り消しました。請求はありません。"
	default:
		title, body = "公式リセールの抽選結果", "チケットの割り当てはありませんでした。請求はありません。"
	}
	if applyAgain {
		body += "再度お申し込みいただけます。"
	}
	return title, c.concertTitle() + ": " + body
}
