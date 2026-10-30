package messaging

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/components/forwarder"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/protobuf/proto"

	wsql "github.com/ThreeDotsLabs/watermill-sql/v4/pkg/sql"
)

// OutboxTopic is the watermill-sql topic every announcement is recorded on;
// its rows live in the table watermill_events_outbox.
const OutboxTopic = "events_outbox"

// outboxChunkSize bounds the rows of one INSERT: PostgreSQL takes at most
// 65,535 parameters and each row uses three.
const outboxChunkSize = 5000

// ContentTypeProtobuf is the ce_datacontenttype of an announcement whose
// payload is a binary protobuf message.
const ContentTypeProtobuf = "application/protobuf"

// Announcement is one message to record: the entity after the change, and
// the CloudEvents subject (ce_subject) when the receiver needs the entity the
// message is about named apart from the payload.
type Announcement struct {
	// Payload is the changed entity, or the entities of one type one
	// operation changed together.
	Payload proto.Message
	// Subject is the ce_subject attribute; empty leaves it unset.
	Subject string
}

// Outbox records announcements in the transaction of the change they
// announce. A forwarder in workflow-worker delivers each recorded row to its
// NATS subject and deletes it (see NewForwarder).
type Outbox struct {
	logger watermill.LoggerAdapter
	now    func() time.Time
}

// NewOutbox returns an Outbox.
func NewOutbox(logger watermill.LoggerAdapter) *Outbox {
	return &Outbox{logger: logger, now: time.Now}
}

// Record records one announcement of subject on tx. The announcement gets a
// new UUIDv7 id, used as its CloudEvents id and its message UUID, so every
// delivery of it carries the same id. It is delivered only if tx commits.
func (o *Outbox) Record(ctx context.Context, tx pgx.Tx, subject string, a Announcement) error {
	return o.RecordAll(ctx, tx, subject, []Announcement{a})
}

// RecordAll records announcements of one subject on tx, each with its own id,
// in one multi-row INSERT per 5,000 announcements. Recording none does
// nothing.
func (o *Outbox) RecordAll(ctx context.Context, tx pgx.Tx, subject string, as []Announcement) error {
	if len(as) == 0 {
		return nil
	}
	sqlPub, err := wsql.NewPublisher(wsql.TxFromPgx(tx), wsql.PublisherConfig{SchemaAdapter: outboxSchema()}, o.logger)
	if err != nil {
		return fmt.Errorf("create outbox publisher: %w", err)
	}
	pub := forwarder.NewPublisher(sqlPub, forwarder.PublisherConfig{ForwarderTopic: OutboxTopic})

	for start := 0; start < len(as); start += outboxChunkSize {
		end := min(start+outboxChunkSize, len(as))
		msgs := make([]*message.Message, 0, end-start)
		for _, a := range as[start:end] {
			msg, err := o.newMessage(ctx, subject, a)
			if err != nil {
				return err
			}
			msgs = append(msgs, msg)
		}
		if err := pub.Publish(subject, msgs...); err != nil {
			return fmt.Errorf("record %d %s announcements: %w", len(msgs), subject, err)
		}
	}
	return nil
}

// newMessage builds the message of one announcement: the binary protobuf
// payload, the CloudEvents attributes and the caller's trace context.
func (o *Outbox) newMessage(ctx context.Context, subject string, a Announcement) (*message.Message, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("new announcement id: %w", err)
	}
	payload, err := proto.Marshal(a.Payload)
	if err != nil {
		return nil, fmt.Errorf("marshal %s announcement: %w", subject, err)
	}

	msg := message.NewMessage(id.String(), payload)
	msg.SetContext(ctx)
	msg.Metadata.Set("ce_specversion", specVersion)
	msg.Metadata.Set("ce_source", source)
	msg.Metadata.Set("ce_id", id.String())
	msg.Metadata.Set("ce_type", subject)
	msg.Metadata.Set("ce_time", o.now().UTC().Format(time.RFC3339Nano))
	msg.Metadata.Set("ce_datacontenttype", ContentTypeProtobuf)
	if a.Subject != "" {
		msg.Metadata.Set("ce_subject", a.Subject)
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(msg.Metadata))
	return msg, nil
}

// outboxSchema is the watermill-sql layout of watermill_events_outbox,
// created by the Atlas migration 20261030130000_create_events_outbox.sql.
func outboxSchema() wsql.PostgreSQLQueueSchema {
	return wsql.PostgreSQLQueueSchema{}
}

// NewForwarder returns the forwarder that delivers recorded announcements:
// it reads watermill_events_outbox through conn, publishes each row to its
// subject through pub (the NATS publisher, whose message id deduplicates a
// redelivery) and deletes the row once JetStream acknowledged it. A row whose
// envelope cannot be read never succeeds, so it is logged and deleted; any
// other failure keeps the row for the next attempt. Run it with Run and stop
// it with Close.
func NewForwarder(conn wsql.Conn, pub message.Publisher, logger watermill.LoggerAdapter) (*forwarder.Forwarder, error) {
	sub, err := wsql.NewSubscriber(wsql.BeginnerFromPgx(conn), wsql.SubscriberConfig{
		SchemaAdapter:  outboxSchema(),
		OffsetsAdapter: wsql.PostgreSQLQueueOffsetsAdapter{DeleteOnAck: true},
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("create outbox subscriber: %w", err)
	}
	fwd, err := forwarder.NewForwarder(sub, pub, logger, forwarder.Config{
		ForwarderTopic:      OutboxTopic,
		AckWhenCannotUnwrap: true,
	})
	if err != nil {
		return nil, fmt.Errorf("create outbox forwarder: %w", err)
	}
	return fwd, nil
}

// Undelivered is the oldest announcement still in the outbox.
type Undelivered struct {
	// Subject is the NATS subject it is recorded for.
	Subject string
	// RecordTime is when it was recorded.
	RecordTime time.Time
}

// oldestUndeliveredQuery reads the oldest row of the outbox with the subject
// in its envelope.
const oldestUndeliveredQuery = `
	SELECT payload->>'destination_topic', created_at
	FROM watermill_events_outbox
	ORDER BY "offset"
	LIMIT 1
`

// OldestUndelivered returns the oldest announcement not yet delivered, or
// false when every recorded announcement was delivered.
func OldestUndelivered(ctx context.Context, conn wsql.Conn) (Undelivered, bool, error) {
	rows, err := conn.Query(ctx, oldestUndeliveredQuery)
	if err != nil {
		return Undelivered{}, false, fmt.Errorf("read the oldest undelivered announcement: %w", err)
	}
	u, err := pgx.CollectExactlyOneRow(rows, func(row pgx.CollectableRow) (Undelivered, error) {
		var u Undelivered
		err := row.Scan(&u.Subject, &u.RecordTime)
		return u, err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Undelivered{}, false, nil
	}
	if err != nil {
		return Undelivered{}, false, fmt.Errorf("read the oldest undelivered announcement: %w", err)
	}
	return u, true, nil
}
