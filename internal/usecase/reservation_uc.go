package usecase

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"

	"github.com/liverty-music/backend/internal/entity"
)

// ReservationUseCase runs a signed-in fan's first-come checkout: holding the
// tickets, reading the checkout, and opening the card hold, plus the job that
// ends lapsed checkouts and gives back leftover card holds.
type ReservationUseCase interface {
	// Start holds count tickets of the sale for the fan for 15 minutes, or
	// resumes the fan's holding checkout with the same count.
	//
	// # Possible errors
	//
	//  - NotFound: the sale does not exist.
	//  - FailedPrecondition: the event is not published, the sale is not
	//    OnSale or AllHeld, or the per-account limit would be exceeded.
	//  - InvalidArgument: count is below 1 or above the sale's per-account
	//    limit.
	//  - ResourceExhausted: not enough tickets remain.
	Start(ctx context.Context, userID entity.UserID, saleID entity.TicketSaleID, count int) (*entity.Reservation, error)

	// Get returns the fan's own checkout as of now: a Held checkout whose hold
	// has lapsed is returned Expired.
	//
	// # Possible errors
	//
	//  - PermissionDenied: the Reservation does not exist or is another fan's.
	Get(ctx context.Context, userID entity.UserID, reservationID entity.ReservationID) (*entity.Reservation, error)

	// Authorize records the holder identity for the fan's holding checkout,
	// opens the card hold for its amount, saves the identity on the fan, and
	// returns the hold's confirmation secret (never stored).
	//
	// # Possible errors
	//
	//  - PermissionDenied: the Reservation does not exist or is another fan's.
	//  - FailedPrecondition: the Reservation is not holding.
	//  - InvalidArgument: the identity breaks the holder identity rules.
	//  - Unavailable: card payments cannot be reached.
	Authorize(ctx context.Context, userID entity.UserID, reservationID entity.ReservationID, identity entity.HolderIdentity) (string, error)

	// ReleaseExpired makes lapsed Held checkouts Expired and gives back the
	// card hold of every ended checkout, reporting a charged hold on an ended
	// checkout as needing an operator. One Reservation's failure never stops
	// the others.
	//
	// # Possible errors
	//
	//  - The listing's error.
	ReleaseExpired(ctx context.Context) error
}

// reservationUseCase implements [ReservationUseCase].
type reservationUseCase struct {
	reservationRepo entity.ReservationRepository
	saleRepo        entity.TicketSaleRepository
	userRepo        entity.UserRepository
	eventState      EventPublishStatePort
	auth            entity.ReservationAuthorizationPort
	clock           Clock
	logger          *logging.Logger
}

// Compile-time interface compliance check.
var _ ReservationUseCase = (*reservationUseCase)(nil)

// NewReservationUseCase constructs a ReservationUseCase.
func NewReservationUseCase(
	reservationRepo entity.ReservationRepository,
	saleRepo entity.TicketSaleRepository,
	userRepo entity.UserRepository,
	eventState EventPublishStatePort,
	auth entity.ReservationAuthorizationPort,
	clock Clock,
	logger *logging.Logger,
) ReservationUseCase {
	return &reservationUseCase{
		reservationRepo: reservationRepo,
		saleRepo:        saleRepo,
		userRepo:        userRepo,
		eventState:      eventState,
		auth:            auth,
		clock:           clock,
		logger:          logger,
	}
}

// Start implements [ReservationUseCase].
func (uc *reservationUseCase) Start(ctx context.Context, userID entity.UserID, saleID entity.TicketSaleID, count int) (*entity.Reservation, error) {
	now := uc.clock()
	sale, err := uc.saleRepo.Get(ctx, saleID, now)
	if err != nil {
		return nil, err
	}
	published, err := uc.eventState.IsEventPublished(ctx, sale.EventID)
	if err != nil {
		return nil, err
	}
	if !published {
		return nil, apperr.New(codes.FailedPrecondition, "the concert is not published")
	}
	if state := sale.StateAt(now); state != entity.TicketSaleStateOnSale && state != entity.TicketSaleStateAllHeld {
		return nil, apperr.New(codes.FailedPrecondition, "the sale is not on sale")
	}
	if count < 1 || count > sale.PerAccountLimit {
		return nil, apperr.New(codes.InvalidArgument, "the count must be 1 to the sale's per-account limit")
	}

	return uc.reservationRepo.GetOrCreateHeld(ctx, saleID, userID, count, now)
}

// ownReservation reads the Reservation and fails with PermissionDenied,
// without revealing whether it exists, unless it is the fan's.
func (uc *reservationUseCase) ownReservation(ctx context.Context, userID entity.UserID, id entity.ReservationID) (*entity.Reservation, error) {
	res, err := uc.reservationRepo.Get(ctx, id)
	if err != nil {
		if errors.Is(err, apperr.ErrNotFound) {
			return nil, apperr.New(codes.PermissionDenied, "the reservation is not the caller's")
		}
		return nil, err
	}
	if res.UserID != userID {
		return nil, apperr.New(codes.PermissionDenied, "the reservation is not the caller's")
	}
	return res, nil
}

// Get implements [ReservationUseCase].
func (uc *reservationUseCase) Get(ctx context.Context, userID entity.UserID, reservationID entity.ReservationID) (*entity.Reservation, error) {
	res, err := uc.ownReservation(ctx, userID, reservationID)
	if err != nil {
		return nil, err
	}
	asOf := *res
	asOf.Status = res.StatusAt(uc.clock())
	return &asOf, nil
}

// Authorize implements [ReservationUseCase].
func (uc *reservationUseCase) Authorize(ctx context.Context, userID entity.UserID, reservationID entity.ReservationID, identity entity.HolderIdentity) (string, error) {
	res, err := uc.ownReservation(ctx, userID, reservationID)
	if err != nil {
		return "", err
	}
	now := uc.clock()
	if !res.IsHoldingAt(now) {
		return "", apperr.New(codes.FailedPrecondition, "the hold has lapsed; start again")
	}
	sale, err := uc.saleRepo.Get(ctx, res.TicketSaleID, now)
	if err != nil {
		return "", err
	}

	ref, secret, err := uc.auth.CreateAuthorization(ctx, res.Amount, entity.AuthorizationMetadata{
		ReservationID: res.ID,
		TicketSaleID:  res.TicketSaleID,
		EventID:       sale.EventID,
	})
	if err != nil {
		return "", err
	}
	if err := uc.reservationRepo.SetAuthorization(ctx, reservationID, identity, ref); err != nil {
		return "", err
	}
	if _, err := uc.userRepo.UpdateHolderIdentity(ctx, string(userID), identity); err != nil {
		return "", err
	}
	return secret, nil
}

// ReleaseExpired implements [ReservationUseCase].
func (uc *reservationUseCase) ReleaseExpired(ctx context.Context) error {
	now := uc.clock()
	due, err := uc.reservationRepo.ListDue(ctx, now)
	if err != nil {
		return err
	}
	for _, res := range due {
		switch res.Status {
		case entity.ReservationStatusHeld:
			if _, err := uc.reservationRepo.Release(ctx, res.ID, now); err != nil {
				uc.logger.Warn(ctx, "release sweep: failed to release reservation; retrying next run",
					slog.String("reservation_id", string(res.ID)), slog.Any("error", err))
			}
		case entity.ReservationStatusExpired, entity.ReservationStatusReleased:
			uc.giveBackCardHold(ctx, res, now)
		}
	}
	return nil
}

// giveBackCardHold releases an ended checkout's card hold and records it. A
// charged hold is reported as needing an operator.
func (uc *reservationUseCase) giveBackCardHold(ctx context.Context, res *entity.Reservation, now time.Time) {
	if err := uc.auth.CancelAuthorization(ctx, res.AuthorizationRef); err != nil {
		if errors.Is(err, apperr.ErrFailedPrecondition) {
			uc.logger.Error(ctx, LogKeyNeedsOperator, errors.New("the card hold of an ended checkout was charged"),
				slog.String("reservation_id", string(res.ID)),
				slog.String("payment_ref", res.AuthorizationRef),
				slog.String("status", res.Status.String()),
			)
			return
		}
		uc.logger.Warn(ctx, "release sweep: failed to cancel card hold; retrying next run",
			slog.String("reservation_id", string(res.ID)), slog.Any("error", err))
		return
	}
	if err := uc.reservationRepo.RecordAuthorizationRelease(ctx, res.ID, now); err != nil {
		uc.logger.Warn(ctx, "release sweep: failed to record card hold release; retrying next run",
			slog.String("reservation_id", string(res.ID)), slog.Any("error", err))
	}
}
