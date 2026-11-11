package rdb

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/messaging"
)

// Announcer records announcements on the transaction of the change they
// announce; *messaging.Outbox implements it. Announcing repositories take one
// in their constructor.
type Announcer interface {
	// Record records one announcement of subject on tx; it is delivered only
	// if tx commits.
	//
	// # Possible errors
	//
	//   - Internal: the payload cannot be encoded.
	//
	// Any other error is a failure to record the announcement.
	Record(ctx context.Context, tx pgx.Tx, subject string, a messaging.Announcement) error
	// RecordAll records announcements of one subject on tx, as Record does.
	// Recording none does nothing.
	//
	// # Possible errors
	//
	//   - Internal: a payload cannot be encoded.
	//
	// Any other error is a failure to record the announcements.
	RecordAll(ctx context.Context, tx pgx.Tx, subject string, as []messaging.Announcement) error
}

// announced lists every type a repository may announce: the changed entity,
// or the Concerts of one Series announced together. Announcing any other type
// does not compile. The Outbox encodes a payload through
// pb.AnnouncementEncoder, which must map every type listed here.
type announced interface {
	*entity.Artist |
		*entity.Checkout |
		[]*entity.Concert |
		*entity.Event |
		*entity.Follow |
		*entity.LotteryEntry |
		*entity.Order |
		*entity.Organizer |
		*entity.Passkey |
		*entity.PushSubscription |
		*entity.ResaleEntry |
		*entity.ResaleListing |
		*entity.SeriesImage |
		*entity.Ticket |
		*entity.TicketJourney |
		*entity.TicketSale |
		*entity.User
}

// announcement is one payload to announce with announceAllAbout, and the
// entity it is about (ce_subject).
type announcement[T announced] struct {
	Payload T
	About   string
}

// announce records one announcement of subject on tx, carrying payload. A
// failure to record it is Unavailable, so the caller rolls back the change
// and the operation can be repeated; a payload the Outbox cannot encode is
// Internal.
func announce[T announced](ctx context.Context, out Announcer, tx pgx.Tx, subject string, payload T) error {
	return announceAbout(ctx, out, tx, subject, "", payload)
}

// announceAbout records one announcement of subject on tx, as announce does,
// with about as its ce_subject.
func announceAbout[T announced](ctx context.Context, out Announcer, tx pgx.Tx, subject, about string, payload T) error {
	if err := out.Record(ctx, tx, subject, messaging.Announcement{Payload: payload, Subject: about}); err != nil {
		return recordFailure(err, "failed to record the announcement", slog.String("subject", subject))
	}
	return nil
}

// announceAll records an announcement of subject on tx for each payload, as
// announce does. Recording none does nothing.
func announceAll[T announced](ctx context.Context, out Announcer, tx pgx.Tx, subject string, payloads []T) error {
	as := make([]announcement[T], 0, len(payloads))
	for _, p := range payloads {
		as = append(as, announcement[T]{Payload: p})
	}
	return announceAllAbout(ctx, out, tx, subject, as)
}

// announceAllAbout records announcements of subject on tx, as announceAll
// does, each with its own ce_subject.
func announceAllAbout[T announced](ctx context.Context, out Announcer, tx pgx.Tx, subject string, as []announcement[T]) error {
	if len(as) == 0 {
		return nil
	}
	msgs := make([]messaging.Announcement, 0, len(as))
	for _, a := range as {
		msgs = append(msgs, messaging.Announcement{Payload: a.Payload, Subject: a.About})
	}
	if err := out.RecordAll(ctx, tx, subject, msgs); err != nil {
		return recordFailure(err, "failed to record the announcements",
			slog.String("subject", subject), slog.Int("count", len(as)))
	}
	return nil
}

// recordFailure returns the error of a failed Record: an encoding failure
// (Internal) as it is, anything else as Unavailable.
func recordFailure(err error, msg string, attrs ...slog.Attr) error {
	if errors.Is(err, apperr.ErrInternal) {
		return err
	}
	return apperr.Wrap(err, codes.Unavailable, msg, attrs...)
}
