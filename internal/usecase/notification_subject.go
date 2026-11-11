package usecase

import (
	"context"
	"errors"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/entity"
)

// SubjectDeps are what Deliver and List read to render a Notification: the fan
// and the records its subject names. All are required in a process that runs
// either.
type SubjectDeps struct {
	Users         entity.UserRepository
	Follows       entity.FollowRepository
	Artists       entity.ArtistRepository
	Concerts      ConcertsByIDsReader
	Orders        entity.OrderRepository
	TicketSales   entity.TicketSaleRepository
	Entries       entity.LotteryEntryRepository
	ResaleEntries entity.ResaleEntryRepository
	// Now is the clock (time.Now in production).
	Now Clock
}

// errSubjectGone is the NotFound, with the reason NOTIFICATION_SUBJECT_GONE,
// that load returns when a record the subject names no longer exists. Loading
// a subject reads several records, so NotFound alone does not say the subject
// is gone.
var errSubjectGone = apperr.New(codes.NotFound, "subject no longer exists",
	apperr.WithReason("NOTIFICATION_SUBJECT_GONE"))

// load reads the records n's subject names, with the fan's follows, time zone
// and the current time, for [entity.Notification.Message].
func (d SubjectDeps) load(ctx context.Context, n *entity.Notification, user *entity.User, follows []*entity.FollowedArtist) (*entity.LoadedSubject, error) {
	s := &entity.LoadedSubject{
		Follows:  follows,
		TimeZone: entity.TimeZoneOf(user.TimeZone),
		Now:      d.Now(),
	}
	if err := d.loadRecords(ctx, n, s); err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, errSubjectGone
		}
		return nil, err
	}
	return s, nil
}

// loadRecords fills s with the records of n's type.
func (d SubjectDeps) loadRecords(ctx context.Context, n *entity.Notification, s *entity.LoadedSubject) error {
	subject := n.Subject
	var err error
	switch n.Type {
	case entity.NotificationTypeNewConcerts:
		if s.Artist, err = d.Artists.Get(ctx, subject.ArtistID); err != nil {
			return err
		}
		return d.loadConcerts(ctx, s, subject.EventIDs)
	case entity.NotificationTypeTicketSaleAnnouncement, entity.NotificationTypeSalesReminder:
		if s.TicketSale, err = d.TicketSales.Get(ctx, entity.TicketSaleID(subject.TicketSaleID)); err != nil {
			return err
		}
		return d.loadConcerts(ctx, s, []string{subject.EventID})
	case entity.NotificationTypeLotteryLoss:
		if s.LotteryEntry, err = d.Entries.Get(ctx, entity.LotteryEntryID(subject.LotteryEntryID)); err != nil {
			return err
		}
		return d.loadConcertOfOrder(ctx, s, s.LotteryEntry.OrderID)
	case entity.NotificationTypeResaleEntryLapsed, entity.NotificationTypeResaleEntryLost:
		if s.ResaleEntry, err = d.ResaleEntries.Get(ctx, entity.ResaleEntryID(subject.ResaleEntryID)); err != nil {
			return err
		}
		return d.loadConcertOfOrder(ctx, s, s.ResaleEntry.OrderID)
	default:
		if s.Order, err = d.Orders.Get(ctx, entity.OrderID(subject.OrderID)); err != nil {
			return err
		}
		return d.loadConcertOfOrder(ctx, s, s.Order.ID)
	}
}

// loadConcertOfOrder reads the concert of the Order's event.
func (d SubjectDeps) loadConcertOfOrder(ctx context.Context, s *entity.LoadedSubject, orderID entity.OrderID) error {
	eventID, err := d.Orders.GetEventID(ctx, orderID)
	if err != nil {
		return err
	}
	return d.loadConcerts(ctx, s, []string{eventID})
}

// loadConcerts reads the concerts of ids; one that no longer exists makes the
// subject gone.
func (d SubjectDeps) loadConcerts(ctx context.Context, s *entity.LoadedSubject, ids []string) error {
	concerts, err := d.Concerts.ListByIDs(ctx, ids)
	if err != nil {
		return err
	}
	if len(concerts) != len(ids) {
		return errSubjectGone
	}
	s.Concerts = concerts
	return nil
}
