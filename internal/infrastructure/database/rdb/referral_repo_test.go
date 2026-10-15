package rdb_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/database/rdb"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"uuid"
)

// seedCheckoutWithStatus inserts a checkout of count tickets for amount yen
// on the ticket type in the given status, with the commit and capture times
// that status requires, and returns its id.
func seedCheckoutWithStatus(t *testing.T, ticketTypeID entity.TicketTypeID, status entity.CheckoutStatus, count int, amount int64) entity.CheckoutID {
	t.Helper()
	id := entity.NewID()
	now := time.Now()
	var authorization, commitAt, captureAt any
	switch status {
	case entity.CheckoutStatusCommitted:
		authorization, commitAt = "pi_"+id, now
	case entity.CheckoutStatusCompleted:
		authorization, commitAt, captureAt = "pi_"+id, now, now
	}
	_, err := testDB.Pool.Exec(context.Background(), `
		INSERT INTO checkouts (id, ticket_type_id, user_id, ticket_count, amount, card_authorization_id, status, hold_expire_at, commit_at, capture_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		id, string(ticketTypeID), seedFanUser(t), count, amount, authorization, int16(status), now.Add(15*time.Minute), commitAt, captureAt)
	require.NoError(t, err)
	return entity.CheckoutID(id)
}

// seedLotteryEntryWithState inserts a lottery entry of count tickets on the
// ticket type in the given state for userID and returns its id.
func seedLotteryEntryWithState(t *testing.T, ticketTypeID entity.TicketTypeID, userID string, state entity.LotteryEntryState, count int) entity.LotteryEntryID {
	t.Helper()
	id := entity.NewID()
	var authorization any
	if state != entity.LotteryEntryStateDraft && state != entity.LotteryEntryStateWithdrawn {
		authorization = "pi_" + id
	}
	_, err := testDB.Pool.Exec(context.Background(), `
		INSERT INTO lottery_entries (id, ticket_type_id, user_id, ticket_count, card_authorization_id, state)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		id, string(ticketTypeID), userID, count, authorization, int16(state))
	require.NoError(t, err)
	return entity.LotteryEntryID(id)
}

// seedOrderOf inserts an order of the given status and amount for the source.
func seedOrderOf(t *testing.T, source entity.ReferralSource, status entity.OrderStatus, amount int64) {
	t.Helper()
	id := entity.NewID()
	var checkoutID, entryID any
	if source.CheckoutID != "" {
		checkoutID = string(source.CheckoutID)
	} else {
		entryID = string(source.LotteryEntryID)
	}
	_, err := testDB.Pool.Exec(context.Background(), `
		INSERT INTO orders (id, user_id, checkout_id, lottery_entry_id, provider, provider_payment_id, status, amount, currency, pay_at)
		VALUES ($1, $2, $3, $4, 1, $5, $6, $7, 'JPY', now())`,
		id, seedFanUser(t), checkoutID, entryID, "pi_"+id, int16(status), amount)
	require.NoError(t, err)
}

// referralOf returns the source's referral code and create time; found is
// false when the source has no referral.
func referralOf(t *testing.T, source entity.ReferralSource) (code string, createTime time.Time, found bool) {
	t.Helper()
	rows, err := testDB.Pool.Query(context.Background(), `
		SELECT code, created_at FROM referrals
		WHERE checkout_id = $1 OR lottery_entry_id = $2`,
		nullableID(string(source.CheckoutID)), nullableID(string(source.LotteryEntryID)))
	require.NoError(t, err)
	defer rows.Close()
	n := 0
	for rows.Next() {
		require.NoError(t, rows.Scan(&code, &createTime))
		n++
	}
	require.NoError(t, rows.Err())
	require.LessOrEqual(t, n, 1, "at most one referral per source")
	return code, createTime, n == 1
}

func nullableID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

func TestReferralRepository_Record(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewReferralRepository(testDB)
	ctx := context.Background()
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	t.Run("first code", func(t *testing.T) {
		// @spec components/entity/referral/record "First code"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 100, 0)
		source := entity.ReferralSource{CheckoutID: seedCheckoutWithStatus(t, tt.ID, entity.CheckoutStatusHeld, 2, 6000)}

		require.NoError(t, repo.Record(ctx, source, "member-a", at))

		code, createTime, found := referralOf(t, source)
		require.True(t, found)
		assert.Equal(t, "member-a", code)
		assert.True(t, at.Equal(createTime))
	})

	t.Run("same code again", func(t *testing.T) {
		// @spec components/entity/referral/record "Same code again"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 100, 0)
		source := entity.ReferralSource{CheckoutID: seedCheckoutWithStatus(t, tt.ID, entity.CheckoutStatusHeld, 2, 6000)}
		require.NoError(t, repo.Record(ctx, source, "member-a", at))

		require.NoError(t, repo.Record(ctx, source, "member-a", at.Add(time.Minute)))

		code, createTime, found := referralOf(t, source)
		require.True(t, found)
		assert.Equal(t, "member-a", code)
		assert.True(t, at.Equal(createTime))
	})

	t.Run("last link wins", func(t *testing.T) {
		// @spec components/entity/referral/record "Last link wins"
		tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 0)
		source := entity.ReferralSource{LotteryEntryID: seedLotteryEntryWithState(t, tt.ID, seedFanUser(t), entity.LotteryEntryStateDraft, 2)}
		require.NoError(t, repo.Record(ctx, source, "member-a", at))

		require.NoError(t, repo.Record(ctx, source, "member-b", at.Add(time.Hour)))

		code, createTime, found := referralOf(t, source)
		require.True(t, found)
		assert.Equal(t, "member-b", code)
		assert.True(t, at.Equal(createTime), "the create time is unchanged")
	})

	t.Run("confirmed source keeps its code", func(t *testing.T) {
		// @spec components/entity/referral/record "Confirmed source keeps its code"
		tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 0)
		entryID := seedLotteryEntryWithState(t, tt.ID, seedFanUser(t), entity.LotteryEntryStateDraft, 2)
		source := entity.ReferralSource{LotteryEntryID: entryID}
		require.NoError(t, repo.Record(ctx, source, "member-a", at))
		_, err := testDB.Pool.Exec(ctx, `UPDATE lottery_entries SET state = 1, card_authorization_id = 'pi_x' WHERE id = $1`, string(entryID))
		require.NoError(t, err)

		require.NoError(t, repo.Record(ctx, source, "member-b", at.Add(time.Hour)))

		code, _, found := referralOf(t, source)
		require.True(t, found)
		assert.Equal(t, "member-a", code)
	})

	t.Run("confirmed source without a code", func(t *testing.T) {
		// @spec components/entity/referral/record "Confirmed source without a code"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 100, 0)
		source := entity.ReferralSource{CheckoutID: seedCheckoutWithStatus(t, tt.ID, entity.CheckoutStatusCommitted, 2, 6000)}

		require.NoError(t, repo.Record(ctx, source, "member-a", at))

		_, _, found := referralOf(t, source)
		assert.False(t, found)
	})

	t.Run("concurrent records", func(t *testing.T) {
		// @spec components/entity/referral/record "Concurrent records"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 100, 0)
		source := entity.ReferralSource{CheckoutID: seedCheckoutWithStatus(t, tt.ID, entity.CheckoutStatusHeld, 2, 6000)}

		var wg sync.WaitGroup
		errs := make([]error, 2)
		for i, code := range []entity.ReferralCode{"member-a", "member-b"} {
			wg.Go(func() { errs[i] = repo.Record(ctx, source, code, at) })
		}
		wg.Wait()

		require.NoError(t, errs[0])
		require.NoError(t, errs[1])
		code, _, found := referralOf(t, source)
		require.True(t, found)
		assert.Contains(t, []string{"member-a", "member-b"}, code)
	})

	t.Run("unknown source", func(t *testing.T) {
		// @spec components/entity/referral/record "Unknown source"
		source := entity.ReferralSource{CheckoutID: entity.CheckoutID(uuid.NewV7().String())}

		err := repo.Record(ctx, source, "member-a", at)

		assert.ErrorIs(t, err, apperr.ErrFailedPrecondition)
		_, _, found := referralOf(t, source)
		assert.False(t, found)
	})

	t.Run("two sources", func(t *testing.T) {
		source := entity.ReferralSource{CheckoutID: "c", LotteryEntryID: "le"}

		err := repo.Record(ctx, source, "member-a", at)

		assert.ErrorIs(t, err, apperr.ErrInvalidArgument)
	})
}

func TestReferral_LivesWithItsSource(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	repo := rdb.NewReferralRepository(testDB)
	ctx := context.Background()
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)

	t.Run("abandoned checkout removed with its concert", func(t *testing.T) {
		// @spec components/entity/referral "Abandoned checkout removed with its concert"
		cleanDatabase(t)
		venueID := seedVenue(t, "Referral Venue")
		artistID := seedArtist(t, "Referral Artist", uuid.NewV7().String())
		eventID := seedEvent(t, venueID, artistID, "Referral Show", "2026-08-01")
		tt := seedTicketTypeForEvent(t, eventID, entity.TicketSaleMethodFirstCome, 10, 4)
		checkoutID := seedCheckoutWithStatus(t, tt.ID, entity.CheckoutStatusHeld, 1, 3000)
		source := entity.ReferralSource{CheckoutID: checkoutID}
		require.NoError(t, repo.Record(ctx, source, "member-a", at))
		_, err := testDB.Pool.Exec(ctx, `UPDATE checkouts SET status = 5 WHERE id = $1`, string(checkoutID))
		require.NoError(t, err)

		require.NoError(t, rdb.NewConcertRepository(testDB).DeleteAndSuppress(ctx, eventID))

		_, _, found := referralOf(t, source)
		assert.False(t, found)
	})

	t.Run("lottery entry kept after its user is deleted", func(t *testing.T) {
		// @spec components/entity/referral "Lottery entry kept after its user is deleted"
		cleanDatabase(t)
		tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 0)
		userID := seedFanUser(t)
		entryID := seedLotteryEntryWithState(t, tt.ID, userID, entity.LotteryEntryStateDraft, 2)
		source := entity.ReferralSource{LotteryEntryID: entryID}
		require.NoError(t, repo.Record(ctx, source, "member-a", at))
		_, err := testDB.Pool.Exec(ctx, `UPDATE lottery_entries SET state = 1, card_authorization_id = 'pi_x' WHERE id = $1`, string(entryID))
		require.NoError(t, err)

		require.NoError(t, rdb.NewUserRepository(testDB).Delete(ctx, userID))

		var entries int
		require.NoError(t, testDB.Pool.QueryRow(ctx, `SELECT count(*) FROM lottery_entries WHERE id = $1`, string(entryID)).Scan(&entries))
		assert.Equal(t, 1, entries, "the lottery entry remains")
		code, _, found := referralOf(t, source)
		require.True(t, found, "its referral remains")
		assert.Equal(t, "member-a", code)
	})
}

func TestReferralRepository_ListStatsByEvent(t *testing.T) {
	if testDB == nil {
		t.Skip("no local database available")
	}
	cleanDatabase(t)
	repo := rdb.NewReferralRepository(testDB)
	ctx := context.Background()
	at := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	code := func(c entity.ReferralCode) *entity.ReferralCode { return &c }
	// paidCheckout makes a Completed checkout of count tickets on the ticket
	// type with a Paid order of amount yen, started with refCode unless empty.
	paidCheckout := func(t *testing.T, tt *entity.TicketType, refCode entity.ReferralCode, status entity.OrderStatus, count int, amount int64) {
		t.Helper()
		id := seedCheckoutWithStatus(t, tt.ID, entity.CheckoutStatusHeld, count, amount)
		source := entity.ReferralSource{CheckoutID: id}
		if refCode != "" {
			require.NoError(t, repo.Record(ctx, source, refCode, at))
		}
		_, err := testDB.Pool.Exec(ctx, `UPDATE checkouts SET status = 3, card_authorization_id = $2, commit_at = now(), capture_at = now() WHERE id = $1`, string(id), "pi_"+string(id))
		require.NoError(t, err)
		seedOrderOf(t, source, status, amount)
	}

	t.Run("two codes and direct purchases", func(t *testing.T) {
		// @spec components/entity/referral/list-stats-by-event "Two codes and direct purchases"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 100, 0)
		paidCheckout(t, tt, "member-a", entity.OrderStatusPaid, 2, 6500)
		paidCheckout(t, tt, "member-a", entity.OrderStatusPaid, 3, 9750)
		paidCheckout(t, tt, "", entity.OrderStatusPaid, 1, 3250)

		got, err := repo.ListStatsByEvent(ctx, tt.EventID)

		require.NoError(t, err)
		assert.Equal(t, []entity.ReferralCodeStats{
			{Code: code("member-a"), OrderCount: 2, TicketCount: 5, Amount: 16250},
			{Code: nil, OrderCount: 1, TicketCount: 1, Amount: 3250},
		}, got)
	})

	t.Run("lottery win counted once its order exists", func(t *testing.T) {
		// @spec components/entity/referral/list-stats-by-event "Lottery win counted once its Order exists"
		tt := seedTicketType(t, entity.TicketSaleMethodLottery, 100, 0)
		entryID := seedLotteryEntryWithState(t, tt.ID, seedFanUser(t), entity.LotteryEntryStateDraft, 2)
		source := entity.ReferralSource{LotteryEntryID: entryID}
		require.NoError(t, repo.Record(ctx, source, "member-b", at))
		_, err := testDB.Pool.Exec(ctx, `UPDATE lottery_entries SET state = 1, card_authorization_id = 'pi_' || id WHERE id = $1`, string(entryID))
		require.NoError(t, err)

		before, err := repo.ListStatsByEvent(ctx, tt.EventID)
		require.NoError(t, err)
		assert.Empty(t, before, "an Entered entry without an Order is not counted")

		_, err = testDB.Pool.Exec(ctx, `UPDATE lottery_entries SET state = 2 WHERE id = $1`, string(entryID))
		require.NoError(t, err)
		seedOrderOf(t, source, entity.OrderStatusPaid, 16000)

		after, err := repo.ListStatsByEvent(ctx, tt.EventID)
		require.NoError(t, err)
		assert.Equal(t, []entity.ReferralCodeStats{
			{Code: code("member-b"), OrderCount: 1, TicketCount: 2, Amount: 16000},
		}, after)
	})

	t.Run("refunded order excluded", func(t *testing.T) {
		// @spec components/entity/referral/list-stats-by-event "Refunded order excluded"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 100, 0)
		paidCheckout(t, tt, "member-a", entity.OrderStatusRefunded, 2, 6000)
		paidCheckout(t, tt, "", entity.OrderStatusPaid, 1, 3000)

		got, err := repo.ListStatsByEvent(ctx, tt.EventID)

		require.NoError(t, err)
		assert.Equal(t, []entity.ReferralCodeStats{
			{Code: nil, OrderCount: 1, TicketCount: 1, Amount: 3000},
		}, got)
	})

	t.Run("abandoned checkout not counted", func(t *testing.T) {
		// @spec components/entity/referral/list-stats-by-event "Abandoned checkout not counted"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 100, 0)
		id := seedCheckoutWithStatus(t, tt.ID, entity.CheckoutStatusHeld, 2, 6000)
		require.NoError(t, repo.Record(ctx, entity.ReferralSource{CheckoutID: id}, "member-c", at))
		_, err := testDB.Pool.Exec(ctx, `UPDATE checkouts SET status = 4 WHERE id = $1`, string(id))
		require.NoError(t, err)

		got, err := repo.ListStatsByEvent(ctx, tt.EventID)

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("other events not counted", func(t *testing.T) {
		// @spec components/entity/referral/list-stats-by-event "Other events not counted"
		events := seedFirstPartyEvents(t, "PUBLISHED", "PUBLISHED")
		this := seedTicketTypeForEvent(t, events[0], entity.TicketSaleMethodFirstCome, 100, 0)
		other := seedTicketTypeForEvent(t, events[1], entity.TicketSaleMethodFirstCome, 100, 0)
		paidCheckout(t, this, "member-a", entity.OrderStatusPaid, 1, 3000)
		paidCheckout(t, other, "member-a", entity.OrderStatusPaid, 2, 6000)

		got, err := repo.ListStatsByEvent(ctx, events[0])

		require.NoError(t, err)
		assert.Equal(t, []entity.ReferralCodeStats{
			{Code: code("member-a"), OrderCount: 1, TicketCount: 1, Amount: 3000},
		}, got)
	})

	t.Run("no purchases", func(t *testing.T) {
		// @spec components/entity/referral/list-stats-by-event "No purchases"
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 100, 0)

		got, err := repo.ListStatsByEvent(ctx, tt.EventID)

		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("groups ordered by order count then code", func(t *testing.T) {
		tt := seedTicketType(t, entity.TicketSaleMethodFirstCome, 100, 0)
		paidCheckout(t, tt, "zz", entity.OrderStatusPaid, 1, 3000)
		paidCheckout(t, tt, "b", entity.OrderStatusPaid, 1, 3000)
		paidCheckout(t, tt, "a-1", entity.OrderStatusPaid, 1, 3000)
		paidCheckout(t, tt, "a-1", entity.OrderStatusPaid, 1, 3000)

		got, err := repo.ListStatsByEvent(ctx, tt.EventID)

		require.NoError(t, err)
		require.Len(t, got, 3)
		assert.Equal(t, []*entity.ReferralCode{code("a-1"), code("b"), code("zz")}, []*entity.ReferralCode{got[0].Code, got[1].Code, got[2].Code})
	})
}
