package entity

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// LoadedSubject holds what rendering a Notification reads: the records its
// subject names, loaded by the usecase, the fan's follows and time zone, and
// the current time.
type LoadedSubject struct {
	// Artist is the subject's artist; set for new_concerts only.
	Artist *Artist
	// Concerts are the subject's concerts with their Series, Venue and Artists:
	// the matched concerts for new_concerts, and otherwise the one concert the
	// Notification is about.
	Concerts []*Concert
	// TicketSale is the subject's sale; set for ticket sale types only.
	TicketSale *TicketSale
	// Order is the subject's Order; set for win, cancellation and refund types.
	Order *Order
	// LotteryEntry is the subject's entry; set for lottery_loss only.
	LotteryEntry *LotteryEntry
	// ResaleEntry is the subject's entry; set for resale entry types only.
	ResaleEntry *ResaleEntry
	// Follows are the fan's followed artists with their hype levels.
	Follows []*FollowedArtist
	// TimeZone is the fan's time zone (see [TimeZoneOf]).
	TimeZone *time.Location
	// Now is the current time, read by the usecase.
	Now time.Time
}

// Copy keys: one per row of the message table. Each names a type, narrowed by
// the sale's method and stage or by why the entry ended.
const (
	copyNewConcerts           = "new_concerts"
	copyNewConcert            = "new_concert"
	copyAnnounceFirstCome     = "announce_first_come"
	copyAnnounceLottery       = "announce_lottery"
	copyApplyOpenFirstCome    = "apply_open_first_come"
	copyApplyOpenLottery      = "apply_open_lottery"
	copyApplyClose24H         = "apply_close_24h"
	copyLotteryWin            = "lottery_win"
	copyResaleWin             = "resale_win"
	copyLotteryNotDrawn       = "lottery_not_drawn"
	copyLotteryNotCharged     = "lottery_not_charged"
	copyResaleLapsed          = "resale_lapsed"
	copyResaleNoTicketLeft    = "resale_no_ticket_left"
	copyResaleConcertCanceled = "resale_concert_cancelled"
	copyResalePaymentFailed   = "resale_payment_failed"
	copyResaleListingCanceled = "resale_listing_cancelled"
	copyConcertCancelled      = "concert_cancelled"
	copyOrderRefunded         = "order_refunded"
)

// notificationCopy is one message's headline and detail. The detail's
// placeholders are {n}, {start}, {end}, {date}, {venue} and {amount}.
type notificationCopy struct {
	headline string
	detail   string
}

// notificationCopies holds the fixed copy by language and key.
var notificationCopies = map[string]map[string]notificationCopy{
	"ja": {
		copyNewConcerts:           {"新着ライブ発表!!", "新しいライブが{n}件見つかりました"},
		copyNewConcert:            {"新着ライブ発表!!", "新しいライブが{n}件見つかりました"},
		copyAnnounceFirstCome:     {"先着販売のお知らせ", "{start}から先着販売スタート!!"},
		copyAnnounceLottery:       {"抽選受付のお知らせ", "{start}から抽選申し込みスタート!!"},
		copyApplyOpenFirstCome:    {"まもなく先着販売開始", "{start}から先着販売スタート!!"},
		copyApplyOpenLottery:      {"抽選受付スタート", "抽選申し込みがスタートしました!! 締切は{end}"},
		copyApplyClose24H:         {"抽選締切が近づいています", "抽選申し込みの締切は{end}!!"},
		copyLotteryWin:            {"抽選に当選しました", "{date}の{venue}公演のチケット抽選に当選しました!!タップしてチケットを表示"},
		copyResaleWin:             {"公式リセールに当選しました", "{date}の{venue}公演の公式リセールに当選しました!!タップしてチケットを表示"},
		copyLotteryNotDrawn:       {"抽選結果のお知らせ", "{date}の{venue}公演のチケット抽選が落選となりました。"},
		copyLotteryNotCharged:     {"当選を取り消しました", "カードで決済できなかったため、{date}の{venue}公演の当選を取り消しました。"},
		copyResaleLapsed:          {"公式リセールの期限切れ", "カードの与信の有効期限が来たため申し込みが終了しました。"},
		copyResaleNoTicketLeft:    {"公式リセールの抽選結果", "チケットの割り当てはありませんでした。"},
		copyResaleConcertCanceled: {"公式リセールの終了", "公演中止のため申し込みが終了しました。"},
		copyResalePaymentFailed:   {"リセール当選の取り消し", "決済を完了できなかったため、{date}の{venue}公演の当選を取り消しました。請求はありません。"},
		copyResaleListingCanceled: {"リセール当選の取り消し", "お申し込みに割り当てたチケットを販売できなくなったため、{date}の{venue}公演の当選を取り消しました。お支払いいただいた金額はカードに返金します。"},
		copyConcertCancelled:      {"公演中止のお知らせ", "{date}の{venue}公演は中止となりました。チケット代金はカードに返金されます。"},
		copyOrderRefunded:         {"返金が完了しました", "{date}の{venue}公演のチケット代金{amount}円をカードに返金しました。"},
	},
	"en": {
		copyNewConcerts:           {"New Concerts", "{n} new concerts found"},
		copyNewConcert:            {"New Concerts", "{n} new concert found"},
		copyAnnounceFirstCome:     {"First-Come Ticket Sale", "Ticket sale (first come) starts {start}!"},
		copyAnnounceLottery:       {"Ticket Lottery", "Ticket lottery entry starts {start}!"},
		copyApplyOpenFirstCome:    {"First-Come Sale Starting Soon", "Ticket sale (first come) starts {start}!"},
		copyApplyOpenLottery:      {"Ticket Lottery Open", "Ticket lottery entry is open! Closes {end}."},
		copyApplyClose24H:         {"Ticket Lottery Closing Soon", "Ticket lottery entry closes {end}!"},
		copyLotteryWin:            {"You Won the Ticket Lottery", "You won the ticket lottery for {venue} on {date}! Tap to see your tickets."},
		copyResaleWin:             {"You Won the Official Resale", "You won the official resale for {venue} on {date}! Tap to see your tickets."},
		copyLotteryNotDrawn:       {"Ticket Lottery Result", "Your lottery entry for {venue} on {date} was not drawn."},
		copyLotteryNotCharged:     {"Lottery Win Cancelled", "Your win for {venue} on {date} was cancelled because your card could not be charged."},
		copyResaleLapsed:          {"Official Resale Entry Expired", "Your entry ended because the card authorization expired."},
		copyResaleNoTicketLeft:    {"Official Resale Result", "No ticket was allocated to your entry."},
		copyResaleConcertCanceled: {"Official Resale Ended", "Your entry ended because the concert was cancelled."},
		copyResalePaymentFailed:   {"Resale Win Cancelled", "Your win for {venue} on {date} was cancelled because the payment could not be completed. You were not charged."},
		copyResaleListingCanceled: {"Resale Win Cancelled", "Your win for {venue} on {date} was cancelled because the ticket could no longer be sold. The amount charged is refunded to your card."},
		copyConcertCancelled:      {"Concert Cancelled", "The concert at {venue} on {date} has been cancelled. Your tickets will be refunded to your card."},
		copyOrderRefunded:         {"Refund Complete", "JPY {amount} for the concert at {venue} on {date} has been refunded to your card."},
	},
}

// Sentences added to a detail while resale of the event is open, by language.
var (
	resaleApplyAgain = map[string]string{
		"ja": "再度、リセール申し込みをお願いします。",
		"en": " Please apply again.",
	}
	resaleApplyAgainWithCard = map[string]string{
		"ja": "別のカードで再度お申し込みいただけます。",
		"en": " You can apply again with another card.",
	}
)

const (
	// ticketsLink opens the Tickets screen.
	ticketsLink = "/tickets"
	// ResaleEntriesLink opens the fan's resale entries.
	ResaleEntriesLink = "/resale-entries"
)

// Message renders n from the records of s in lang, which falls back to English
// for anything but Japanese: the title reads "[<artist>] <headline>", the body
// "<detail>", a line break and the tour title. It reads nothing and holds no
// clock. s holds what the type's subject names.
func (n *Notification) Message(lang string, s *LoadedSubject) *NotificationPayload {
	if lang != "ja" {
		lang = "en"
	}
	concert := s.concert()
	c := notificationCopies[lang][n.copyKey(s)]
	detail := c.detail + n.detailTail(lang, s, concert)

	artist := s.headlineArtist(n.Type, concert)
	title := c.headline
	if artist != "" {
		title = "[" + artist + "] " + c.headline
	}
	tour := ""
	if concert.Series != nil {
		tour = concert.Series.Title
	}
	return NewNotificationPayload(title, s.fill(lang, detail, concert)+"\n"+tour, n.link(s, concert), n.tag())
}

// concert returns the concert the message is about: the earliest of the
// matched concerts, and otherwise the only one.
func (s *LoadedSubject) concert() *Concert {
	return EarliestConcert(s.Concerts)
}

// copyKey selects the row of the message table.
func (n *Notification) copyKey(s *LoadedSubject) string {
	switch n.Type {
	case NotificationTypeNewConcerts:
		if len(s.Concerts) == 1 {
			return copyNewConcert
		}
		return copyNewConcerts
	case NotificationTypeTicketSaleAnnouncement:
		if s.TicketSale.Method == TicketSaleMethodFirstCome {
			return copyAnnounceFirstCome
		}
		return copyAnnounceLottery
	case NotificationTypeSalesReminder:
		switch {
		case n.Subject.Stage == ReminderStageApplyClose24H:
			return copyApplyClose24H
		case s.TicketSale.Method == TicketSaleMethodFirstCome:
			return copyApplyOpenFirstCome
		default:
			return copyApplyOpenLottery
		}
	case NotificationTypeLotteryWin:
		return copyLotteryWin
	case NotificationTypeResaleWin:
		return copyResaleWin
	case NotificationTypeLotteryLoss:
		if s.LotteryEntry.LostReason == LotteryEntryLostReasonCardNotCharged {
			return copyLotteryNotCharged
		}
		return copyLotteryNotDrawn
	case NotificationTypeResaleEntryLapsed:
		return copyResaleLapsed
	case NotificationTypeResaleEntryLost:
		switch s.ResaleEntry.LostReason {
		case ResaleEntryLostReasonConcertCancelled:
			return copyResaleConcertCanceled
		case ResaleEntryLostReasonCardNotCharged, ResaleEntryLostReasonPaymentNotCompletedBeforeShow:
			return copyResalePaymentFailed
		case ResaleEntryLostReasonListingCancelled:
			return copyResaleListingCanceled
		default:
			return copyResaleNoTicketLeft
		}
	case NotificationTypeConcertCancelled:
		return copyConcertCancelled
	default:
		return copyOrderRefunded
	}
}

// detailTail is the invitation to apply again while resale of the event is
// open: for a lapsed entry, and for a lost entry whose card could not be
// charged.
func (n *Notification) detailTail(lang string, s *LoadedSubject, c *Concert) string {
	var tail map[string]string
	switch {
	case n.Type == NotificationTypeResaleEntryLapsed:
		tail = resaleApplyAgain
	case n.Type == NotificationTypeResaleEntryLost && s.ResaleEntry.LostReason == ResaleEntryLostReasonCardNotCharged:
		tail = resaleApplyAgainWithCard
	default:
		return ""
	}
	deadline, ok := ResaleDeadlineOf(&c.Event)
	if !ok || !s.Now.Before(deadline) || c.HasPublishState(PublishStateCancelled) {
		return ""
	}
	return tail[lang]
}

// headlineArtist is the artist in the title: the subject's artist for
// new_concerts, and otherwise the performer the fan follows at the highest
// hype level, the earliest in display order among equals, or the first
// performer when the fan follows none.
func (s *LoadedSubject) headlineArtist(typ NotificationType, c *Concert) string {
	if typ == NotificationTypeNewConcerts {
		return s.Artist.Name
	}
	hypes := make(map[string]Hype, len(s.Follows))
	for _, f := range s.Follows {
		hypes[f.Artist.ID] = f.Hype
	}
	var (
		name     string
		best     Hype
		followed bool
	)
	for _, a := range c.Artists {
		h, ok := hypes[a.ID]
		switch {
		case name == "":
			name, best, followed = a.Name, h, ok
		case ok && (!followed || h.IsAbove(best)):
			name, best, followed = a.Name, h, true
		}
	}
	return name
}

// fill replaces the detail's placeholders.
func (s *LoadedSubject) fill(lang, detail string, c *Concert) string {
	var start, end, amount string
	if s.TicketSale != nil {
		start = formatNotificationTime(s.TicketSale.SaleStartTime, s.TimeZone, lang)
		end = formatNotificationTime(s.TicketSale.SaleEndTime, s.TimeZone, lang)
	}
	if s.Order != nil && s.Order.Refund != nil {
		amount = FormatYen(s.Order.Refund.Amount)
	}
	return strings.NewReplacer(
		"{n}", strconv.Itoa(len(s.Concerts)),
		"{start}", start,
		"{end}", end,
		"{date}", formatConcertDate(c.LocalDate, lang),
		"{venue}", c.VenueName(),
		"{amount}", amount,
	).Replace(detail)
}

// link is the in-app link the message opens; it does not depend on the
// language.
func (n *Notification) link(s *LoadedSubject, c *Concert) string {
	switch n.Type {
	case NotificationTypeNewConcerts, NotificationTypeLotteryLoss, NotificationTypeConcertCancelled:
		if c.Series != nil && c.Series.IsFirstParty() {
			return "/events/" + c.ID
		}
		return "/concerts/" + c.ID
	case NotificationTypeTicketSaleAnnouncement, NotificationTypeSalesReminder:
		return "/concerts/" + n.Subject.EventID
	case NotificationTypeLotteryWin, NotificationTypeResaleWin:
		return ticketsLink
	case NotificationTypeResaleEntryLapsed, NotificationTypeResaleEntryLost:
		return ResaleEntriesLink
	default:
		return "/orders/" + n.Subject.OrderID
	}
}

// tag groups the messages that replace each other on a device: one per artist
// for new_concerts, one per TicketSale for an announcement, one per TicketSale
// and stage for a reminder, and one per type and subject otherwise.
func (n *Notification) tag() string {
	switch n.Type {
	case NotificationTypeNewConcerts:
		return "concert-" + n.Subject.ArtistID
	case NotificationTypeTicketSaleAnnouncement:
		return "ticket-sale-" + n.Subject.TicketSaleID
	case NotificationTypeSalesReminder:
		return fmt.Sprintf("ticket-sale-%s-stage-%d", n.Subject.TicketSaleID, n.Subject.Stage)
	case NotificationTypeLotteryLoss:
		return string(n.Type) + "-" + n.Subject.LotteryEntryID
	case NotificationTypeResaleEntryLapsed, NotificationTypeResaleEntryLost:
		return string(n.Type) + "-" + n.Subject.ResaleEntryID
	default:
		return string(n.Type) + "-" + n.Subject.OrderID
	}
}

// WeekdaysJA are the Japanese weekday abbreviations, indexed by time.Weekday.
var WeekdaysJA = [7]string{"日", "月", "火", "水", "木", "金", "土"}

// formatNotificationTime formats t in tz: "10/22(木) 23:59" in Japanese and
// "Oct 22 (Thu) 23:59" in English.
func formatNotificationTime(t time.Time, tz *time.Location, lang string) string {
	local := t.In(tz)
	if lang == "ja" {
		return fmt.Sprintf("%d/%d(%s) %s", int(local.Month()), local.Day(), WeekdaysJA[local.Weekday()], local.Format("15:04"))
	}
	return local.Format("Jan 2 (Mon) 15:04")
}

// formatConcertDate formats a concert's date: "11/3" in Japanese and "Nov 3"
// in English.
func formatConcertDate(d time.Time, lang string) string {
	if lang == "ja" {
		return fmt.Sprintf("%d/%d", int(d.Month()), d.Day())
	}
	return d.Format("Jan 2")
}

// FormatYen formats an amount with thousands separators: 6000 -> "6,000".
func FormatYen(amount int64) string {
	s := strconv.FormatInt(amount, 10)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// fallbackTimeZone is the time zone of a fan with none set or an unknown one.
const fallbackTimeZone = "Asia/Tokyo"

// TimeZoneOf parses an IANA time zone name. It never returns nil: an empty or
// unrecognised name falls back to Asia/Tokyo, then UTC when tzdata is missing.
func TimeZoneOf(name string) *time.Location {
	if strings.TrimSpace(name) != "" {
		if tz, err := time.LoadLocation(name); err == nil {
			return tz
		}
	}
	if tz, err := time.LoadLocation(fallbackTimeZone); err == nil {
		return tz
	}
	return time.UTC
}
