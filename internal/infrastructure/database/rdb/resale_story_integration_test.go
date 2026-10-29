package rdb_test

import (
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The story tests of stories/resell-a-ticket list a ticket of a sold-out
// TicketStock, bought first come or won in a lottery, with
// ResaleListingUseCase.Create and read it with ResaleListingUseCase.List, over
// the first-come story's wiring (real Postgres, real Stripe test API). Buying
// a listing arrives with the change resell-tickets-by-lottery, so no listing
// is bought here.

// listOne lists the first of the fan's Tickets for the event and returns the
// listing as created.
func (s *firstComeStory) listOne(fan entity.UserID) *usecase.ResaleListingAt {
	s.t.Helper()
	tickets := s.eventTickets(fan)
	require.NotEmpty(s.t, tickets)
	listing, err := s.resale.Create(s.ctx, fan, tickets[0].ID)
	require.NoError(s.t, err)
	return listing
}

// ownListings returns the fan's listings as ResaleListingUseCase.List shows them.
func (s *firstComeStory) ownListings(fan entity.UserID) []*usecase.ResaleListingAt {
	s.t.Helper()
	listings, err := s.resale.List(s.ctx, fan)
	require.NoError(s.t, err)
	return listings
}

// assertValidForEntry asserts the fan holds count Tickets for the event, all
// 未入場 (Issued, not admitted) and valid for entry.
func (s *firstComeStory) assertValidForEntry(fan entity.UserID, count int) {
	s.t.Helper()
	tickets := s.eventTickets(fan)
	require.Len(s.t, tickets, count)
	for _, tk := range tickets {
		assert.Equal(s.t, entity.TicketStatusIssued, tk.Status)
		assert.True(s.t, tk.IsAdmissible(), "the Ticket stays valid for entry")
	}
}

// resaleOpenOnEventPage reports whether the event page offers resale for the
// stock, as TicketSaleUseCase.ListByEvent shows it.
func (s *firstComeStory) resaleOpenOnEventPage(stockID entity.TicketStockID) bool {
	s.t.Helper()
	sales, err := s.sales.ListByEvent(s.ctx, s.eventID)
	require.NoError(s.t, err)
	for _, v := range sales.Stocks {
		if v.Stock.ID == stockID {
			return v.ResaleOpen
		}
	}
	s.t.Fatalf("stock %s is not on the event page", stockID)
	return false
}

func TestResaleStory_OneOfTwoTicketsListed(t *testing.T) {
	// @spec stories/resell-a-ticket "One of two tickets listed"
	s := newFirstComeStory(t)
	stock := s.stock("S席", 5000, 2)
	s.openFirstCome()

	// -- a fan buys 2 S席 tickets first come, and S席 sells out --
	fan := s.newFan()
	s.buy(stock.ID, fan, 2, nil)
	require.Equal(t, entity.TicketStockStateSoldOut, s.stockAt(stock.ID).State())
	assert.True(t, s.resaleOpenOnEventPage(stock.ID))

	// -- the fan lists one --
	created := s.listOne(fan)
	assert.Equal(t, entity.ResaleListingStatusListed, created.Status)

	listings := s.ownListings(fan)
	require.Len(t, listings, 1)
	assert.Equal(t, entity.ResaleListingStatusListed, listings[0].Status, "the listing is Listed")
	assert.Equal(t, int64(5000), listings[0].Listing.Price, "at 5000 yen")
	assert.Equal(t, stock.ID, listings[0].Listing.TicketStockID)
	s.assertValidForEntry(fan, 2)

	// The same Ticket cannot be listed twice.
	_, err := s.resale.Create(s.ctx, fan, created.Listing.TicketID)
	assert.ErrorIs(t, err, apperr.ErrAlreadyExists)
}

func TestResaleStory_LotteryWonTicketListed(t *testing.T) {
	// @spec stories/resell-a-ticket "Lottery-won ticket listed"
	s := newFirstComeStory(t)
	stock := s.stock("S席", 5000, 2)

	// -- a fan wins 2 S席 tickets in the lottery presale, and S席 sells out --
	fan := s.newFan()
	s.winPresale(stock, fan, 2)
	require.Equal(t, entity.TicketStockStateSoldOut, s.stockAt(stock.ID).State())

	// -- the fan lists one --
	s.listOne(fan)
	listings := s.ownListings(fan)
	require.Len(t, listings, 1)
	assert.Equal(t, entity.ResaleListingStatusListed, listings[0].Status, "the listing is Listed")
	assert.Equal(t, int64(5000), listings[0].Listing.Price)
	s.assertValidForEntry(fan, 2)
}

// TestResaleStory_NotSoldOutIsNotListed checks the precondition of "A ticket
// of a sold-out seat type is listed": while the stock still has tickets, the
// primary sale sells them and no listing is made.
func TestResaleStory_NotSoldOutIsNotListed(t *testing.T) {
	s := newFirstComeStory(t)
	stock := s.stock("S席", 5000, 150)
	s.openFirstCome()

	fan := s.newFan()
	s.buy(stock.ID, fan, 2, nil)
	_, err := s.resale.Create(s.ctx, fan, s.eventTickets(fan)[0].ID)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "resale opens only once the stock sold out")
	assert.False(t, s.resaleOpenOnEventPage(stock.ID))
	assert.Empty(t, s.ownListings(fan))
}

func TestResaleStory_UnsoldAtTheDeadline(t *testing.T) {
	// @spec stories/resell-a-ticket "Unsold at the deadline"
	s := newFirstComeStory(t)
	stock := s.stock("S席", 5000, 2)
	s.openFirstCome()

	fan := s.newFan()
	s.buy(stock.ID, fan, 2, nil)
	listed := s.listOne(fan)

	// -- the admission window opens with the listing unsold --
	s.now = listed.Deadline
	listings := s.ownListings(fan)
	require.Len(t, listings, 1)
	assert.Equal(t, entity.ResaleListingStatusExpired, listings[0].Status, "the listing is no longer offered")
	assert.False(t, s.resaleOpenOnEventPage(stock.ID), "the event page offers no resale")

	// No ticket is listed any more.
	tickets := s.eventTickets(fan)
	var other entity.TicketID
	for _, tk := range tickets {
		if tk.ID != listed.Listing.TicketID {
			other = tk.ID
		}
	}
	_, err := s.resale.Create(s.ctx, fan, other)
	assert.ErrorIs(t, err, apperr.ErrFailedPrecondition, "nothing is listed after the deadline")

	// -- the listed Ticket stays valid for entry; admission at the venue is
	//    TicketUseCase.Admit's, covered by TestTicketUseCase_Admit_Integration --
	s.assertValidForEntry(fan, 2)
}
