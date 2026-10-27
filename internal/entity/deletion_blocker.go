package entity

// DeletionBlockerKind is a kind of in-progress activity that stops a User from
// being deleted.
type DeletionBlockerKind int

const (
	// DeletionBlockerKindUnspecified is the zero value and is never returned.
	DeletionBlockerKindUnspecified DeletionBlockerKind = 0
	// DeletionBlockerKindUpcomingTicket is an Issued Ticket for an Event whose
	// local date is today or later in Japan time.
	DeletionBlockerKindUpcomingTicket DeletionBlockerKind = 1
	// DeletionBlockerKindListedResale is a ResaleListing in status Listed.
	DeletionBlockerKindListedResale DeletionBlockerKind = 2
	// DeletionBlockerKindPendingLotteryEntry is a LotteryEntry in state
	// Entered, awaiting its draw.
	DeletionBlockerKindPendingLotteryEntry DeletionBlockerKind = 3
	// DeletionBlockerKindPaymentInProgress is a payment in progress: a
	// Checkout Committed, or Held before its hold expiry, or a LotteryEntry in
	// state Won whose Order is not issued yet.
	DeletionBlockerKindPaymentInProgress DeletionBlockerKind = 4
)

// DeletionBlocker is one kind of in-progress activity a User has, with how
// many of that kind. A User with any blocker cannot be deleted.
type DeletionBlocker struct {
	// Kind is what is still in progress.
	Kind DeletionBlockerKind
	// Count is how many of the kind the User has, at least 1.
	Count int
}
