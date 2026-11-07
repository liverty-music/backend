package entity_test

import (
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/stretchr/testify/assert"
)

var (
	messageTZ  = time.FixedZone("JST", 9*60*60)
	messageNow = time.Date(2026, 11, 1, 12, 0, 0, 0, messageTZ)
)

// messageConcert is the 11/20 concert at ぴあアリーナMM of the tour 前世 2026 by
// ヨルシカ, starting after messageNow, so its resale is open.
func messageConcert() *entity.Concert {
	start := time.Date(2026, 11, 20, 18, 0, 0, 0, messageTZ)
	return &entity.Concert{
		ID:        "event-1",
		LocalDate: time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC),
		StartTime: &start,
		Venue:     &entity.Venue{Name: "ぴあアリーナMM"},
		Series:    &entity.Series{Title: "前世 2026"},
		Artists:   []*entity.Artist{{ID: "artist-1", Name: "ヨルシカ"}},
	}
}

// messageSubject is the loaded subject of messageConcert with the given records.
func messageSubject(s entity.LoadedSubject) *entity.LoadedSubject {
	if s.Concerts == nil {
		s.Concerts = []*entity.Concert{messageConcert()}
	}
	s.TimeZone = messageTZ
	s.Now = messageNow
	return &s
}

// messageSale opens on 10/6 19:00 and closes on 10/22 23:59.
func messageSale(method entity.TicketSaleMethod) *entity.TicketSale {
	return &entity.TicketSale{
		ID:            "sale-1",
		Method:        method,
		SaleStartTime: time.Date(2026, 10, 6, 19, 0, 0, 0, messageTZ),
		SaleEndTime:   time.Date(2026, 10, 22, 23, 59, 0, 0, messageTZ),
	}
}

func refundedOrder() *entity.Order {
	return &entity.Order{ID: "order-1", Refund: &entity.OrderRefund{Amount: 12000}}
}

// TestNotification_Message_CopyTable checks each row of the message table in
// both languages: title, body with its tour title, link and tag.
func TestNotification_Message_CopyTable(t *testing.T) {
	t.Parallel()

	const tour = "\n前世 2026"
	lotteryEntry := func(r entity.LotteryEntryLostReason) *entity.LotteryEntry {
		return &entity.LotteryEntry{ID: "entry-1", OrderID: "order-1", LostReason: r}
	}
	resaleEntry := func(r entity.ResaleEntryLostReason) *entity.ResaleEntry {
		return &entity.ResaleEntry{ID: "entry-1", OrderID: "order-1", LostReason: r}
	}
	saleSubject := entity.NotificationSubject{TicketSaleID: "sale-1", EventID: "event-1"}
	reminder := func(stage entity.ReminderStage) entity.NotificationSubject {
		return entity.NotificationSubject{TicketSaleID: "sale-1", EventID: "event-1", Stage: stage}
	}
	order := entity.NotificationSubject{OrderID: "order-1"}
	lotteryID := entity.NotificationSubject{LotteryEntryID: "entry-1"}
	resaleID := entity.NotificationSubject{ResaleEntryID: "entry-1"}

	tests := []struct {
		name              string
		typ               entity.NotificationType
		subject           entity.NotificationSubject
		loaded            entity.LoadedSubject
		wantLink, wantTag string
		jaTitle, jaBody   string
		enTitle, enBody   string
	}{
		{
			name: "new concerts", typ: entity.NotificationTypeNewConcerts,
			subject:  entity.NotificationSubject{ArtistID: "artist-1", EventIDs: []string{"event-1", "event-2"}},
			loaded:   entity.LoadedSubject{Artist: &entity.Artist{ID: "artist-1", Name: "ヨルシカ"}, Concerts: []*entity.Concert{messageConcert(), messageConcert()}},
			wantLink: "/concerts/event-1", wantTag: "concert-artist-1",
			jaTitle: "[ヨルシカ] 新着ライブ発表!!", jaBody: "新しいライブが2件見つかりました",
			enTitle: "[ヨルシカ] New Concerts", enBody: "2 new concerts found",
		},
		{
			name: "one new concert", typ: entity.NotificationTypeNewConcerts,
			subject:  entity.NotificationSubject{ArtistID: "artist-1", EventIDs: []string{"event-1"}},
			loaded:   entity.LoadedSubject{Artist: &entity.Artist{ID: "artist-1", Name: "ヨルシカ"}},
			wantLink: "/concerts/event-1", wantTag: "concert-artist-1",
			jaTitle: "[ヨルシカ] 新着ライブ発表!!", jaBody: "新しいライブが1件見つかりました",
			enTitle: "[ヨルシカ] New Concerts", enBody: "1 new concert found",
		},
		{
			name: "announcement first come", typ: entity.NotificationTypeTicketSaleAnnouncement, subject: saleSubject,
			loaded:   entity.LoadedSubject{TicketSale: messageSale(entity.TicketSaleMethodFirstCome)},
			wantLink: "/concerts/event-1", wantTag: "ticket-sale-sale-1",
			jaTitle: "[ヨルシカ] 先着販売のお知らせ", jaBody: "10/6(火) 19:00から先着販売スタート!!",
			enTitle: "[ヨルシカ] First-Come Ticket Sale", enBody: "Ticket sale (first come) starts Oct 6 (Tue) 19:00!",
		},
		{
			name: "announcement lottery", typ: entity.NotificationTypeTicketSaleAnnouncement, subject: saleSubject,
			loaded:   entity.LoadedSubject{TicketSale: messageSale(entity.TicketSaleMethodLottery)},
			wantLink: "/concerts/event-1", wantTag: "ticket-sale-sale-1",
			jaTitle: "[ヨルシカ] 抽選受付のお知らせ", jaBody: "10/6(火) 19:00から抽選申し込みスタート!!",
			enTitle: "[ヨルシカ] Ticket Lottery", enBody: "Ticket lottery entry starts Oct 6 (Tue) 19:00!",
		},
		{
			name: "reminder apply open first come", typ: entity.NotificationTypeSalesReminder, subject: reminder(entity.ReminderStageApplyOpen),
			loaded:   entity.LoadedSubject{TicketSale: messageSale(entity.TicketSaleMethodFirstCome)},
			wantLink: "/concerts/event-1", wantTag: "ticket-sale-sale-1-stage-1",
			jaTitle: "[ヨルシカ] まもなく先着販売開始", jaBody: "10/6(火) 19:00から先着販売スタート!!",
			enTitle: "[ヨルシカ] First-Come Sale Starting Soon", enBody: "Ticket sale (first come) starts Oct 6 (Tue) 19:00!",
		},
		{
			name: "reminder apply open lottery", typ: entity.NotificationTypeSalesReminder, subject: reminder(entity.ReminderStageApplyOpen),
			loaded:   entity.LoadedSubject{TicketSale: messageSale(entity.TicketSaleMethodLottery)},
			wantLink: "/concerts/event-1", wantTag: "ticket-sale-sale-1-stage-1",
			jaTitle: "[ヨルシカ] 抽選受付スタート", jaBody: "抽選申し込みがスタートしました!! 締切は10/22(木) 23:59",
			enTitle: "[ヨルシカ] Ticket Lottery Open", enBody: "Ticket lottery entry is open! Closes Oct 22 (Thu) 23:59.",
		},
		{
			name: "reminder apply close", typ: entity.NotificationTypeSalesReminder, subject: reminder(entity.ReminderStageApplyClose24H),
			loaded:   entity.LoadedSubject{TicketSale: messageSale(entity.TicketSaleMethodLottery)},
			wantLink: "/concerts/event-1", wantTag: "ticket-sale-sale-1-stage-2",
			jaTitle: "[ヨルシカ] 抽選締切が近づいています", jaBody: "抽選申し込みの締切は10/22(木) 23:59!!",
			enTitle: "[ヨルシカ] Ticket Lottery Closing Soon", enBody: "Ticket lottery entry closes Oct 22 (Thu) 23:59!",
		},
		{
			name: "lottery win", typ: entity.NotificationTypeLotteryWin, subject: order,
			loaded:   entity.LoadedSubject{Order: &entity.Order{ID: "order-1"}},
			wantLink: "/tickets", wantTag: "lottery_win-order-1",
			jaTitle: "[ヨルシカ] 抽選に当選しました", jaBody: "11/20のぴあアリーナMM公演のチケット抽選に当選しました!!タップしてチケットを表示",
			enTitle: "[ヨルシカ] You Won the Ticket Lottery", enBody: "You won the ticket lottery for ぴあアリーナMM on Nov 20! Tap to see your tickets.",
		},
		{
			name: "resale win", typ: entity.NotificationTypeResaleWin, subject: order,
			loaded:   entity.LoadedSubject{Order: &entity.Order{ID: "order-1"}},
			wantLink: "/tickets", wantTag: "resale_win-order-1",
			jaTitle: "[ヨルシカ] 公式リセールに当選しました", jaBody: "11/20のぴあアリーナMM公演の公式リセールに当選しました!!タップしてチケットを表示",
			enTitle: "[ヨルシカ] You Won the Official Resale", enBody: "You won the official resale for ぴあアリーナMM on Nov 20! Tap to see your tickets.",
		},
		{
			name: "lottery loss not drawn", typ: entity.NotificationTypeLotteryLoss, subject: lotteryID,
			loaded:   entity.LoadedSubject{LotteryEntry: lotteryEntry(entity.LotteryEntryLostReasonNotDrawn)},
			wantLink: "/concerts/event-1", wantTag: "lottery_loss-entry-1",
			jaTitle: "[ヨルシカ] 抽選結果のお知らせ", jaBody: "11/20のぴあアリーナMM公演のチケット抽選が落選となりました。",
			enTitle: "[ヨルシカ] Ticket Lottery Result", enBody: "Your lottery entry for ぴあアリーナMM on Nov 20 was not drawn.",
		},
		{
			name: "lottery loss card not charged", typ: entity.NotificationTypeLotteryLoss, subject: lotteryID,
			loaded:   entity.LoadedSubject{LotteryEntry: lotteryEntry(entity.LotteryEntryLostReasonCardNotCharged)},
			wantLink: "/concerts/event-1", wantTag: "lottery_loss-entry-1",
			jaTitle: "[ヨルシカ] 当選を取り消しました", jaBody: "カードで決済できなかったため、11/20のぴあアリーナMM公演の当選を取り消しました。",
			enTitle: "[ヨルシカ] Lottery Win Cancelled", enBody: "Your win for ぴあアリーナMM on Nov 20 was cancelled because your card could not be charged.",
		},
		{
			name: "lottery loss concert cancelled", typ: entity.NotificationTypeLotteryLoss, subject: lotteryID,
			loaded:   entity.LoadedSubject{LotteryEntry: lotteryEntry(entity.LotteryEntryLostReasonConcertCancelled)},
			wantLink: "/concerts/event-1", wantTag: "lottery_loss-entry-1",
			jaTitle: "[ヨルシカ] 公演が中止になりました", jaBody: "11/20のぴあアリーナMM公演が中止になったため、抽選のお申し込みは終了しました。請求はありません。",
			enTitle: "[ヨルシカ] Concert Cancelled", enBody: "The concert at ぴあアリーナMM on Nov 20 was cancelled, so your lottery entry ended. You were not charged.",
		},
		{
			name: "resale lapsed while resale is open", typ: entity.NotificationTypeResaleEntryLapsed, subject: resaleID,
			loaded:   entity.LoadedSubject{ResaleEntry: resaleEntry(entity.ResaleEntryLostReasonUnspecified)},
			wantLink: "/resale-entries", wantTag: "resale_entry_lapsed-entry-1",
			jaTitle: "[ヨルシカ] 公式リセールの期限切れ", jaBody: "カードの与信の有効期限が来たため申し込みが終了しました。再度、リセール申し込みをお願いします。",
			enTitle: "[ヨルシカ] Official Resale Entry Expired", enBody: "Your entry ended because the card authorization expired. Please apply again.",
		},
		{
			name: "resale lost no ticket left", typ: entity.NotificationTypeResaleEntryLost, subject: resaleID,
			loaded:   entity.LoadedSubject{ResaleEntry: resaleEntry(entity.ResaleEntryLostReasonNoTicketLeft)},
			wantLink: "/resale-entries", wantTag: "resale_entry_lost-entry-1",
			jaTitle: "[ヨルシカ] 公式リセールの抽選結果", jaBody: "チケットの割り当てはありませんでした。",
			enTitle: "[ヨルシカ] Official Resale Result", enBody: "No ticket was allocated to your entry.",
		},
		{
			name: "resale lost concert cancelled", typ: entity.NotificationTypeResaleEntryLost, subject: resaleID,
			loaded:   entity.LoadedSubject{ResaleEntry: resaleEntry(entity.ResaleEntryLostReasonConcertCancelled)},
			wantLink: "/resale-entries", wantTag: "resale_entry_lost-entry-1",
			jaTitle: "[ヨルシカ] 公式リセールの終了", jaBody: "公演中止のため申し込みが終了しました。",
			enTitle: "[ヨルシカ] Official Resale Ended", enBody: "Your entry ended because the concert was cancelled.",
		},
		{
			name: "resale lost card not charged while resale is open", typ: entity.NotificationTypeResaleEntryLost, subject: resaleID,
			loaded:   entity.LoadedSubject{ResaleEntry: resaleEntry(entity.ResaleEntryLostReasonCardNotCharged)},
			wantLink: "/resale-entries", wantTag: "resale_entry_lost-entry-1",
			jaTitle: "[ヨルシカ] リセール当選の取り消し", jaBody: "決済を完了できなかったため、11/20のぴあアリーナMM公演の当選を取り消しました。請求はありません。別のカードで再度お申し込みいただけます。",
			enTitle: "[ヨルシカ] Resale Win Cancelled", enBody: "Your win for ぴあアリーナMM on Nov 20 was cancelled because the payment could not be completed. You were not charged. You can apply again with another card.",
		},
		{
			name: "resale lost payment not completed before the show", typ: entity.NotificationTypeResaleEntryLost, subject: resaleID,
			loaded:   entity.LoadedSubject{ResaleEntry: resaleEntry(entity.ResaleEntryLostReasonPaymentNotCompletedBeforeShow)},
			wantLink: "/resale-entries", wantTag: "resale_entry_lost-entry-1",
			jaTitle: "[ヨルシカ] リセール当選の取り消し", jaBody: "決済を完了できなかったため、11/20のぴあアリーナMM公演の当選を取り消しました。請求はありません。",
			enTitle: "[ヨルシカ] Resale Win Cancelled", enBody: "Your win for ぴあアリーナMM on Nov 20 was cancelled because the payment could not be completed. You were not charged.",
		},
		{
			name: "resale lost listing cancelled", typ: entity.NotificationTypeResaleEntryLost, subject: resaleID,
			loaded:   entity.LoadedSubject{ResaleEntry: resaleEntry(entity.ResaleEntryLostReasonListingCancelled)},
			wantLink: "/resale-entries", wantTag: "resale_entry_lost-entry-1",
			jaTitle: "[ヨルシカ] リセール当選の取り消し", jaBody: "お申し込みに割り当てたチケットを販売できなくなったため、11/20のぴあアリーナMM公演の当選を取り消しました。お支払いいただいた金額はカードに返金します。",
			enTitle: "[ヨルシカ] Resale Win Cancelled", enBody: "Your win for ぴあアリーナMM on Nov 20 was cancelled because the ticket could no longer be sold. The amount charged is refunded to your card.",
		},
		{
			name: "concert cancelled", typ: entity.NotificationTypeConcertCancelled, subject: order,
			loaded:   entity.LoadedSubject{Order: &entity.Order{ID: "order-1"}},
			wantLink: "/concerts/event-1", wantTag: "concert_cancelled-order-1",
			jaTitle: "[ヨルシカ] 公演中止のお知らせ", jaBody: "11/20のぴあアリーナMM公演は中止となりました。チケット代金はカードに返金されます。",
			enTitle: "[ヨルシカ] Concert Cancelled", enBody: "The concert at ぴあアリーナMM on Nov 20 has been cancelled. Your tickets will be refunded to your card.",
		},
		{
			name: "order refunded", typ: entity.NotificationTypeOrderRefunded, subject: order,
			loaded:   entity.LoadedSubject{Order: refundedOrder()},
			wantLink: "/orders/order-1", wantTag: "order_refunded-order-1",
			jaTitle: "[ヨルシカ] 返金が完了しました", jaBody: "11/20のぴあアリーナMM公演のチケット代金12,000円をカードに返金しました。",
			enTitle: "[ヨルシカ] Refund Complete", enBody: "JPY 12,000 for the concert at ぴあアリーナMM on Nov 20 has been refunded to your card.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			n := &entity.Notification{Type: tt.typ, Subject: tt.subject}

			ja := n.Message("ja", messageSubject(tt.loaded))
			assert.Equal(t, tt.jaTitle, ja.Title)
			assert.Equal(t, tt.jaBody+tour, ja.Body)

			en := n.Message("en", messageSubject(tt.loaded))
			assert.Equal(t, tt.enTitle, en.Title)
			assert.Equal(t, tt.enBody+tour, en.Body)

			for _, got := range []*entity.NotificationPayload{ja, en} {
				assert.Equal(t, tt.wantLink, got.Data[entity.NotificationDataKeyURL])
				assert.Equal(t, tt.wantTag, got.Tag)
			}
		})
	}
}

func TestNotification_Message(t *testing.T) {
	t.Parallel()

	cancelled := func(artists ...*entity.Artist) *entity.LoadedSubject {
		c := messageConcert()
		c.Artists = artists
		return messageSubject(entity.LoadedSubject{Order: &entity.Order{ID: "order-1"}, Concerts: []*entity.Concert{c}})
	}
	a := &entity.Artist{ID: "a", Name: "A"}
	b := &entity.Artist{ID: "b", Name: "B"}
	concertCancelled := &entity.Notification{Type: entity.NotificationTypeConcertCancelled, Subject: entity.NotificationSubject{OrderID: "order-1"}}

	t.Run("lottery loss in Japanese", func(t *testing.T) {
		// @spec components/entity/notification "Lottery loss in Japanese"
		t.Parallel()
		n := &entity.Notification{Type: entity.NotificationTypeLotteryLoss, Subject: entity.NotificationSubject{LotteryEntryID: "entry-1"}}

		got := n.Message("ja", messageSubject(entity.LoadedSubject{
			LotteryEntry: &entity.LotteryEntry{LostReason: entity.LotteryEntryLostReasonNotDrawn},
		}))

		assert.Equal(t, "[ヨルシカ] 抽選結果のお知らせ", got.Title)
		assert.Equal(t, "11/20のぴあアリーナMM公演のチケット抽選が落選となりました。\n前世 2026", got.Body)
	})

	t.Run("co-headlined concert", func(t *testing.T) {
		// @spec components/entity/notification "Co-headlined concert"
		t.Parallel()
		s := cancelled(a, b)
		s.Follows = []*entity.FollowedArtist{{Artist: a, Hype: entity.HypeHome}, {Artist: b, Hype: entity.HypeAway}}

		assert.Equal(t, "[B] Concert Cancelled", concertCancelled.Message("en", s).Title)
	})

	t.Run("equal hype keeps display order", func(t *testing.T) {
		t.Parallel()
		s := cancelled(a, b)
		s.Follows = []*entity.FollowedArtist{{Artist: b, Hype: entity.HypeHome}, {Artist: a, Hype: entity.HypeHome}}

		assert.Equal(t, "[A] Concert Cancelled", concertCancelled.Message("en", s).Title)
	})

	t.Run("a followed performer beats an earlier one the fan does not follow", func(t *testing.T) {
		t.Parallel()
		s := cancelled(a, b)
		s.Follows = []*entity.FollowedArtist{{Artist: b, Hype: entity.HypeWatch}}

		assert.Equal(t, "[B] Concert Cancelled", concertCancelled.Message("en", s).Title)
	})

	t.Run("performers not followed", func(t *testing.T) {
		// @spec components/entity/notification "Performers not followed"
		t.Parallel()
		n := &entity.Notification{Type: entity.NotificationTypeLotteryWin, Subject: entity.NotificationSubject{OrderID: "order-1"}}
		s := cancelled(a, b)

		assert.Equal(t, "[A] You Won the Ticket Lottery", n.Message("en", s).Title)
	})

	t.Run("language other than Japanese", func(t *testing.T) {
		// @spec components/entity/notification "Language other than Japanese"
		t.Parallel()

		assert.Equal(t, "[A] Concert Cancelled", concertCancelled.Message("fr", cancelled(a)).Title)
	})

	t.Run("lapsed after resale closed", func(t *testing.T) {
		// @spec components/entity/notification "Lapsed after resale closed"
		t.Parallel()
		n := &entity.Notification{Type: entity.NotificationTypeResaleEntryLapsed, Subject: entity.NotificationSubject{ResaleEntryID: "entry-1"}}
		s := messageSubject(entity.LoadedSubject{ResaleEntry: &entity.ResaleEntry{}})
		s.Now = *s.Concerts[0].StartTime

		assert.Equal(t, "Your entry ended because the card authorization expired.\n前世 2026", n.Message("en", s).Body)
		assert.Equal(t, "カードの与信の有効期限が来たため申し込みが終了しました。\n前世 2026", n.Message("ja", s).Body)
	})

	t.Run("card not charged after resale closed", func(t *testing.T) {
		t.Parallel()
		n := &entity.Notification{Type: entity.NotificationTypeResaleEntryLost, Subject: entity.NotificationSubject{ResaleEntryID: "entry-1"}}
		s := messageSubject(entity.LoadedSubject{ResaleEntry: &entity.ResaleEntry{LostReason: entity.ResaleEntryLostReasonCardNotCharged}})
		s.Now = *s.Concerts[0].StartTime

		assert.NotContains(t, n.Message("en", s).Body, "apply again")
	})

	t.Run("two reminder stages", func(t *testing.T) {
		// @spec components/entity/notification "Two reminder stages"
		t.Parallel()
		s := messageSubject(entity.LoadedSubject{TicketSale: messageSale(entity.TicketSaleMethodLottery)})
		tag := func(stage entity.ReminderStage) string {
			n := &entity.Notification{Type: entity.NotificationTypeSalesReminder, Subject: entity.NotificationSubject{TicketSaleID: "sale-1", EventID: "event-1", Stage: stage}}
			return n.Message("ja", s).Tag
		}

		assert.NotEqual(t, tag(entity.ReminderStageApplyOpen), tag(entity.ReminderStageApplyClose24H))
	})

	t.Run("same artist again", func(t *testing.T) {
		// @spec components/entity/notification "Same artist again"
		t.Parallel()
		s := messageSubject(entity.LoadedSubject{Artist: &entity.Artist{ID: "artist-1", Name: "ヨルシカ"}})
		tag := func(eventID string) string {
			n := &entity.Notification{Type: entity.NotificationTypeNewConcerts, Subject: entity.NotificationSubject{ArtistID: "artist-1", EventIDs: []string{eventID}}}
			return n.Message("ja", s).Tag
		}

		assert.Equal(t, tag("event-1"), tag("event-2"))
	})

	t.Run("first-party concert links to its event page", func(t *testing.T) {
		t.Parallel()
		c := messageConcert()
		organizer := "org-1"
		c.Series.OrganizerID = &organizer
		s := messageSubject(entity.LoadedSubject{Order: &entity.Order{ID: "order-1"}, Concerts: []*entity.Concert{c}})

		assert.Equal(t, "/events/event-1", concertCancelled.Message("ja", s).Data[entity.NotificationDataKeyURL])
	})
}
