package rdb

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"
)

// OutboxPublisher publishes one event with a stable message id. The messaging
// EventPublisher satisfies it; the id lets JetStream deduplicate a republished
// row within its Duplicates window.
type OutboxPublisher interface {
	PublishEventWithID(ctx context.Context, subject, id string, data any) error
}

// OutboxRelay publishes the events recorded in the outbox table (transactional
// outbox): a row written in a committed transaction is published at least
// once, and a rolled-back transaction leaves no row to publish.
type OutboxRelay struct {
	db        *Database
	publisher OutboxPublisher
	clock     func() time.Time
}

// outboxRelayBatchSize bounds the rows one relay pass locks and publishes.
const outboxRelayBatchSize = 100

const (
	// outboxLockUnsentQuery locks the oldest unsent rows, skipping rows a
	// concurrent relay already holds, so several API replicas never publish
	// the same row at once.
	outboxLockUnsentQuery = `
		SELECT id, subject, message_id, payload FROM outbox
		WHERE sent_at IS NULL
		ORDER BY recorded_at, id
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	`
	outboxMarkSentQuery   = `UPDATE outbox SET sent_at = $2 WHERE id = $1`
	outboxMarkFailedQuery = `UPDATE outbox SET attempts = attempts + 1 WHERE id = $1`
)

// NewOutboxRelay creates an outbox relay publishing through publisher.
func NewOutboxRelay(db *Database, publisher OutboxPublisher, clock func() time.Time) *OutboxRelay {
	return &OutboxRelay{db: db, publisher: publisher, clock: clock}
}

// outboxRow is one locked, unsent outbox row.
type outboxRow struct {
	id        string
	subject   string
	messageID string
	payload   []byte
}

// RelayPending publishes the oldest unsent rows and marks each published one
// sent. A row whose publish fails stays unsent, with its attempt count raised,
// and is retried on the next pass. It returns the number of rows published.
func (r *OutboxRelay) RelayPending(ctx context.Context) (int, error) {
	tx, err := r.db.Pool.Begin(ctx)
	if err != nil {
		return 0, toAppErr(err, "failed to begin outbox relay")
	}
	// Rollback is a no-op after a successful Commit.
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, outboxLockUnsentQuery, outboxRelayBatchSize)
	if err != nil {
		return 0, toAppErr(err, "failed to lock unsent outbox rows")
	}
	var pending []outboxRow
	for rows.Next() {
		var row outboxRow
		if err := rows.Scan(&row.id, &row.subject, &row.messageID, &row.payload); err != nil {
			rows.Close()
			return 0, toAppErr(err, "failed to scan outbox row")
		}
		pending = append(pending, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, toAppErr(err, "failed to iterate outbox rows")
	}

	sent := 0
	for _, row := range pending {
		if err := r.publisher.PublishEventWithID(ctx, row.subject, row.messageID, json.RawMessage(row.payload)); err != nil {
			r.db.logger.Warn(ctx, "outbox relay: publish failed; retrying on the next pass",
				slog.String("outbox_id", row.id),
				slog.String("subject", row.subject),
				slog.String("message_id", row.messageID),
				slog.Any("error", err),
			)
			if _, err := tx.Exec(ctx, outboxMarkFailedQuery, row.id); err != nil {
				return sent, toAppErr(err, "failed to record outbox publish failure", slog.String("outbox_id", row.id))
			}
			continue
		}
		if _, err := tx.Exec(ctx, outboxMarkSentQuery, row.id, r.clock()); err != nil {
			return sent, toAppErr(err, "failed to mark outbox row sent", slog.String("outbox_id", row.id))
		}
		sent++
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, toAppErr(err, "failed to commit outbox relay")
	}
	return sent, nil
}
