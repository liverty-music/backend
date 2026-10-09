package rdb_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recordingPublisher records published events and fails the first n
// publishes.
type recordingPublisher struct {
	mu        sync.Mutex
	failFirst int
	published []string
	payloads  [][]byte
}

func (p *recordingPublisher) PublishEventWithID(_ context.Context, subject, id string, data any) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failFirst > 0 {
		p.failFirst--
		return errors.New("nats unavailable")
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	p.published = append(p.published, subject+"/"+id)
	p.payloads = append(p.payloads, raw)
	return nil
}

func TestOutboxRelay_RelayPending(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 18, 0, 0, 0, time.UTC)

	insert := func(t *testing.T, messageID string) {
		t.Helper()
		_, err := testDB.Pool.Exec(ctx, `INSERT INTO outbox (id, subject, message_id, payload, recorded_at) VALUES ($1, $2, $3, $4, $5)`,
			entity.NewID(), entity.SubjectOrderPaid, messageID, []byte(`{"order_id":"`+messageID+`"}`), now)
		require.NoError(t, err)
	}

	t.Run("a committed row is published once, even when the first publish fails", func(t *testing.T) {
		orderID := entity.NewID()
		insert(t, orderID)
		pub := &recordingPublisher{failFirst: 1}
		relay := rdb.NewOutboxRelay(testDB, pub, func() time.Time { return now })

		sent, err := relay.RelayPending(ctx)
		require.NoError(t, err)
		assert.Equal(t, 0, sent, "the failed publish leaves the row unsent")

		sent, err = relay.RelayPending(ctx)
		require.NoError(t, err)
		assert.Equal(t, 1, sent)

		sent, err = relay.RelayPending(ctx)
		require.NoError(t, err)
		assert.Equal(t, 0, sent, "a sent row is not published again")

		assert.Equal(t, []string{entity.SubjectOrderPaid + "/" + orderID}, pub.published)
		assert.JSONEq(t, `{"order_id":"`+orderID+`"}`, string(pub.payloads[0]), "the payload is published as recorded")
		var attempts int
		var sentAt *time.Time
		require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT attempts, sent_at FROM outbox WHERE message_id = $1`, orderID).Scan(&attempts, &sentAt))
		assert.Equal(t, 1, attempts)
		require.NotNil(t, sentAt)
	})

	t.Run("a rolled-back transaction publishes nothing", func(t *testing.T) {
		orderID := entity.NewID()
		tx, err := testDB.Pool.Begin(ctx)
		require.NoError(t, err)
		_, err = tx.Exec(ctx, `INSERT INTO outbox (id, subject, message_id, payload, recorded_at) VALUES ($1, $2, $3, $4, $5)`,
			entity.NewID(), entity.SubjectOrderPaid, orderID, []byte(`{}`), now)
		require.NoError(t, err)
		require.NoError(t, tx.Rollback(ctx))
		pub := &recordingPublisher{}

		sent, err := rdb.NewOutboxRelay(testDB, pub, func() time.Time { return now }).RelayPending(ctx)

		require.NoError(t, err)
		assert.Equal(t, 0, sent)
		assert.Empty(t, pub.published)
	})

	t.Run("two relays never publish the same row", func(t *testing.T) {
		for range 20 {
			insert(t, entity.NewID())
		}
		pub := &recordingPublisher{}
		relay := rdb.NewOutboxRelay(testDB, pub, func() time.Time { return now })

		var wg sync.WaitGroup
		for range 3 {
			wg.Go(func() {
				_, err := relay.RelayPending(ctx)
				assert.NoError(t, err)
			})
		}
		wg.Wait()
		_, err := relay.RelayPending(ctx)
		require.NoError(t, err)

		assert.Len(t, pub.published, 20)
		seen := map[string]bool{}
		for _, p := range pub.published {
			assert.False(t, seen[p], "published twice: %s", p)
			seen[p] = true
		}
	})
}
