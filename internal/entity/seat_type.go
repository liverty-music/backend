package entity

import (
	"context"
	"errors"
	"unicode/utf8"
)

// SeatTypeID is the opaque identifier for a single SeatType.
//
// Mirrors liverty_music.entity.v1.SeatTypeId.value (a UUID string).
type SeatTypeID string

const (
	// SeatTypeMinPrice is the lowest tax-inclusive ticket price in yen.
	SeatTypeMinPrice int64 = 1
	// SeatTypeMaxPrice is the highest tax-inclusive ticket price in yen.
	SeatTypeMaxPrice int64 = 1_000_000
	// SeatTypeMaxNameLength is the longest seat type name in characters.
	SeatTypeMaxNameLength = 50
)

// SeatType is one kind of ticket (席種) a Series sells, such as S席, A席 or
// オールスタンディング: its name and the price of one ticket. It does not
// assign seats. Every event of the Series that sells it does so through a
// TicketStock. Its name and price never change once it is created; a different
// price is a different SeatType.
//
// Mirrors liverty_music.entity.v1.SeatType.
type SeatType struct {
	// ID is the surrogate primary key (UUIDv7).
	ID SeatTypeID
	// SeriesID is the Series that sells it.
	SeriesID string
	// Name is the label fans and members see, unique within the Series.
	Name string
	// Price is one ticket's tax-inclusive (税込) price in yen: the face value
	// (券面価格) of its tickets.
	Price int64
}

// NewSeatType returns a SeatType of the Series with a generated UUIDv7 id.
func NewSeatType(seriesID, name string, price int64) *SeatType {
	return &SeatType{
		ID:       SeatTypeID(NewID()),
		SeriesID: seriesID,
		Name:     name,
		Price:    price,
	}
}

// Validate reports whether the name is 1 to 50 characters and the price is 1
// to 1,000,000 yen. The entity layer returns stdlib errors; callers wrap them
// with an apperr code.
func (s *SeatType) Validate() error {
	if n := utf8.RuneCountInString(s.Name); n < 1 || n > SeatTypeMaxNameLength {
		return errors.New("seat type name must be 1 to 50 characters")
	}
	if s.Price < SeatTypeMinPrice || s.Price > SeatTypeMaxPrice {
		return errors.New("price must be 1 to 1,000,000 yen")
	}
	return nil
}

// AmountFor returns the amount in yen for count tickets: the price times the
// count.
func (s *SeatType) AmountFor(count int) int64 {
	return s.Price * int64(count)
}

// SeatTypeRepository persists SeatTypes. Implementations live in
// internal/infrastructure/database/rdb/.
//
// Interfaces are defined where consumed (AGENTS.md rule).
type SeatTypeRepository interface {
	// Create stores the SeatType and returns it.
	//
	// # Possible errors
	//
	//  - InvalidArgument: the SeatType breaks the SeatType rules.
	//  - FailedPrecondition: the Series does not exist or is not first-party.
	//  - AlreadyExists: the Series already has a SeatType with the same name.
	//  - Internal: database failure.
	Create(ctx context.Context, seatType *SeatType) (*SeatType, error)

	// Get returns the SeatType with the given id.
	//
	// # Possible errors
	//
	//  - NotFound: no SeatType has the id.
	//  - Internal: database failure.
	Get(ctx context.Context, id SeatTypeID) (*SeatType, error)

	// Delete removes the SeatType with the given id.
	//
	// # Possible errors
	//
	//  - FailedPrecondition: a TicketStock uses the SeatType, also one created
	//    while Delete runs; nothing is removed.
	//  - NotFound: no SeatType has the id.
	//  - Internal: database failure.
	Delete(ctx context.Context, id SeatTypeID) error

	// ListBySeries returns every SeatType of the Series, highest price first,
	// then by name. It returns an empty slice when the Series has none or does
	// not exist.
	//
	// # Possible errors
	//
	//  - Internal: database failure.
	ListBySeries(ctx context.Context, seriesID string) ([]*SeatType, error)
}
