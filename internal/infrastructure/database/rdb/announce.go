package rdb

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/infrastructure/messaging"
)

// Announcer records announcements on the transaction of the change they
// announce; *messaging.Outbox implements it. Announcing repositories take one
// in their constructor.
type Announcer interface {
	Record(ctx context.Context, tx pgx.Tx, subject string, a messaging.Announcement) error
	RecordAll(ctx context.Context, tx pgx.Tx, subject string, as []messaging.Announcement) error
}

// announce records one announcement of subject on tx. A failure to record it
// is Unavailable, so the caller rolls back the change and the operation can
// be repeated.
func announce(ctx context.Context, out Announcer, tx pgx.Tx, subject string, a messaging.Announcement) error {
	if err := out.Record(ctx, tx, subject, a); err != nil {
		return apperr.Wrap(err, codes.Unavailable, "failed to record the announcement", slog.String("subject", subject))
	}
	return nil
}
