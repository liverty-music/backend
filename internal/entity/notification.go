package entity

import (
	"context"
	"log/slog"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
)

// NotificationType identifies the kind of user-facing notification, mirroring
// the producer that raised it. It is persisted verbatim in notifications.type.
type NotificationType string

const (
	// NotificationTypeNewConcerts is a new-concert alert for a followed artist.
	NotificationTypeNewConcerts NotificationType = "new_concerts"
	// NotificationTypeSalesReminder is a ticket sale reminder for a tracked event.
	NotificationTypeSalesReminder NotificationType = "sales_reminder"
	// NotificationTypeTicketSaleAnnouncement announces a newly discovered ticket sale.
	NotificationTypeTicketSaleAnnouncement NotificationType = "ticket_sale_announcement"
	// NotificationTypeLotteryWin tells a fan they won a lottery and opens the
	// Tickets screen.
	NotificationTypeLotteryWin NotificationType = "lottery_win"
	// NotificationTypeResaleWin tells a fan they won an official resale entry
	// and opens the Tickets screen.
	NotificationTypeResaleWin NotificationType = "resale_win"
	// NotificationTypeLotteryLoss tells a fan their lottery entry lost and
	// opens the event's page.
	NotificationTypeLotteryLoss NotificationType = "lottery_loss"
	// NotificationTypeResaleEntryLapsed tells a fan their official resale
	// entry lapsed before a ticket was found and nothing was charged.
	NotificationTypeResaleEntryLapsed NotificationType = "resale_entry_lapsed"
	// NotificationTypeResaleEntryLost tells a fan their official resale entry
	// ended without a ticket and nothing was charged.
	NotificationTypeResaleEntryLost NotificationType = "resale_entry_lost"
	// NotificationTypeConcertCancelled tells a fan with a Paid Order that its
	// concert is cancelled (中止) and its tickets are refunded.
	NotificationTypeConcertCancelled NotificationType = "concert_cancelled"
	// NotificationTypeOrderRefunded tells a fan their Order was refunded for
	// a cancellation, with the amount.
	NotificationTypeOrderRefunded NotificationType = "order_refunded"
)

// NotificationSubject names the records a Notification is about. Which fields
// are set depends on the type; see [NotificationSubject.Validate]. It is the
// notifications.subject column and part of the deliver-notification workflow
// input, so its JSON names must not change without a compatibility plan.
type NotificationSubject struct {
	// ArtistID is the notified artist of a new_concerts Notification.
	ArtistID string `json:"artist_id,omitempty"`
	// EventIDs are the concerts matched for the fan of a new_concerts
	// Notification.
	EventIDs []string `json:"event_ids,omitempty"`
	// EventID is the fan's linked event of a ticket sale Notification.
	EventID string `json:"event_id,omitempty"`
	// TicketSaleID is the sale of a ticket sale Notification.
	TicketSaleID string `json:"ticket_sale_id,omitempty"`
	// Stage is the stage of a sales_reminder Notification.
	Stage ReminderStage `json:"stage,omitempty"`
	// OrderID is the Order of a win, cancellation or refund Notification.
	OrderID string `json:"order_id,omitempty"`
	// LotteryEntryID is the entry of a lottery_loss Notification.
	LotteryEntryID string `json:"lottery_entry_id,omitempty"`
	// ResaleEntryID is the entry of a resale entry Notification.
	ResaleEntryID string `json:"resale_entry_id,omitempty"`
}

// Each subject field as one bit, to compare what a subject names with what its
// type needs.
const (
	subjectArtist uint8 = 1 << iota
	subjectEvents
	subjectEvent
	subjectTicketSale
	subjectStage
	subjectOrder
	subjectLotteryEntry
	subjectResaleEntry
)

// subjectFieldsByType is what each type's subject names, and nothing else.
var subjectFieldsByType = map[NotificationType]uint8{
	NotificationTypeNewConcerts:            subjectArtist | subjectEvents,
	NotificationTypeTicketSaleAnnouncement: subjectTicketSale | subjectEvent,
	NotificationTypeSalesReminder:          subjectTicketSale | subjectStage | subjectEvent,
	NotificationTypeLotteryWin:             subjectOrder,
	NotificationTypeResaleWin:              subjectOrder,
	NotificationTypeLotteryLoss:            subjectLotteryEntry,
	NotificationTypeResaleEntryLapsed:      subjectResaleEntry,
	NotificationTypeResaleEntryLost:        subjectResaleEntry,
	NotificationTypeConcertCancelled:       subjectOrder,
	NotificationTypeOrderRefunded:          subjectOrder,
}

// fields returns the bits of the fields s sets.
func (s NotificationSubject) fields() uint8 {
	var named uint8
	for bit, set := range map[uint8]bool{
		subjectArtist:       s.ArtistID != "",
		subjectEvents:       len(s.EventIDs) > 0,
		subjectEvent:        s.EventID != "",
		subjectTicketSale:   s.TicketSaleID != "",
		subjectStage:        s.Stage != 0,
		subjectOrder:        s.OrderID != "",
		subjectLotteryEntry: s.LotteryEntryID != "",
		subjectResaleEntry:  s.ResaleEntryID != "",
	} {
		if set {
			named |= bit
		}
	}
	return named
}

// Validate reports whether s names exactly the records typ is about.
//
// # Possible errors
//
//   - InvalidArgument: the type is unknown, or the subject lacks a record the
//     type needs, names one it does not, or has an undefined stage.
func (s NotificationSubject) Validate(typ NotificationType) error {
	want, known := subjectFieldsByType[typ]
	if !known {
		return apperr.New(codes.InvalidArgument, "unknown notification type", slog.String("type", string(typ)))
	}
	if s.fields() != want {
		return apperr.New(codes.InvalidArgument, "notification subject does not match its type", slog.String("type", string(typ)))
	}
	if want&subjectStage != 0 && !s.Stage.IsValid() {
		return apperr.New(codes.InvalidArgument, "notification subject has an undefined stage", slog.Int("stage", int(s.Stage)))
	}
	return nil
}

// Notification is the durable record of one message sent to one fan: what it
// is about and whether the fan has read it. It exists whether or not the push
// reached the fan. Its message is rendered from the subject each time.
type Notification struct {
	// ID is the unique notification identifier (UUIDv7, application-generated).
	// It is propagated into the dispatched push payload's data.notification_id
	// as the end-to-end correlation key.
	ID string
	// UserID is the recipient user.
	UserID string
	// Type is the notification kind.
	Type NotificationType
	// Subject names the records the Notification is about.
	Subject NotificationSubject
	// CreateTime is when the record was created.
	CreateTime time.Time
	// ReadTime is when the user marked the notification read; nil until read.
	ReadTime *time.Time
}

// NotificationRepository defines persistence operations for the notification log.
//
// MarkRead is user-scoped so one user cannot alter another user's record.
type NotificationRepository interface {
	// Create stores n with its fan, type and subject, unread, assigning its id
	// (UUIDv7) and create time onto n.
	//
	// # Possible errors
	//
	//   - InvalidArgument: the subject is invalid for the type.
	//   - FailedPrecondition: the fan does not exist.
	Create(ctx context.Context, n *Notification) error

	// MarkRead sets read_at for the (userID, id) notification. It is idempotent:
	// marking an already-read notification is a no-op success.
	MarkRead(ctx context.Context, userID, id string) error

	// Get retrieves the notification identified by id.
	//
	// # Possible errors
	//
	//   - NotFound: no notification with that id exists.
	Get(ctx context.Context, id string) (*Notification, error)

	// ListByUser returns a user's notifications newest first, at most limit.
	// A user with none gets an empty slice.
	//
	// # Possible errors
	//
	//   - InvalidArgument: limit is below 1.
	ListByUser(ctx context.Context, userID string, limit int) ([]*Notification, error)
}
