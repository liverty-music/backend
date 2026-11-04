package rdb

import (
	"context"
	"log/slog"

	"github.com/google/uuid"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"

	"github.com/liverty-music/backend/internal/entity"
)

// CardAuthorizationProvider is the payment provider side of card
// authorizations: it opens, reads, captures and releases them by the payment
// provider's ID and reports everything but the platform's record. The Stripe
// and no-op providers of package payment implement it.
type CardAuthorizationProvider interface {
	// Create opens a Pending card authorization that carries the purchase and
	// returns it with its ProviderID. A retried Create for the same Checkout
	// returns the first one.
	Create(ctx context.Context, purchase entity.CardAuthorizationPurchase, amountJPY int64) (*entity.CardAuthorization, error)
	// Get reads the card authorization whose payment provider's ID is
	// providerID.
	Get(ctx context.Context, providerID string) (*entity.CardAuthorization, error)
	// Capture charges the card authorization whose payment provider's ID is
	// providerID.
	Capture(ctx context.Context, providerID string) error
	// Release gives back the card authorization whose payment provider's ID
	// is providerID.
	Release(ctx context.Context, providerID string) error
}

// CardAuthorizationRepository implements [entity.CardAuthorizationRepository]:
// it keeps each card authorization's record (its platform id and the payment
// provider's ID) in card_authorizations and leaves everything else to the
// payment provider.
type CardAuthorizationRepository struct {
	db       *Database
	provider CardAuthorizationProvider
}

// Compile-time interface compliance check.
var _ entity.CardAuthorizationRepository = (*CardAuthorizationRepository)(nil)

// NewCardAuthorizationRepository creates a CardAuthorizationRepository over
// the payment provider.
func NewCardAuthorizationRepository(db *Database, provider CardAuthorizationProvider) *CardAuthorizationRepository {
	return &CardAuthorizationRepository{db: db, provider: provider}
}

const (
	// cardAuthorizationRecordQuery records a card authorization once per
	// payment provider ID and returns the id of its record: a retried Create
	// of a Checkout gets back the record of its first attempt.
	cardAuthorizationRecordQuery = `
		WITH inserted AS (
			INSERT INTO card_authorizations (id, provider_card_authorization_id)
			VALUES ($1, $2)
			ON CONFLICT (provider_card_authorization_id) DO NOTHING
			RETURNING id
		)
		SELECT id FROM inserted
		UNION ALL
		SELECT id FROM card_authorizations WHERE provider_card_authorization_id = $2
		LIMIT 1
	`
	cardAuthorizationProviderIDQuery = `SELECT provider_card_authorization_id FROM card_authorizations WHERE id = $1`
	cardAuthorizationIDQuery         = `SELECT id FROM card_authorizations WHERE provider_card_authorization_id = $1`
)

// Create implements [entity.CardAuthorizationRepository]. It opens the card
// authorization with the payment provider first and records it after, so a
// record always names a card authorization that exists; one whose record
// failed is never returned to the fan and lapses holding nothing, because its
// secret never reached the browser. A retried Checkout gets back its first
// card authorization and, by its payment provider's ID, its first record.
func (r *CardAuthorizationRepository) Create(ctx context.Context, purchase entity.CardAuthorizationPurchase, amountJPY int64) (*entity.CardAuthorization, error) {
	ca, err := r.provider.Create(ctx, purchase, amountJPY)
	if err != nil {
		return nil, err
	}
	var recorded string
	if err := r.db.Pool.QueryRow(ctx, cardAuthorizationRecordQuery, entity.NewID(), ca.ProviderID).Scan(&recorded); err != nil {
		return nil, toAppErr(err, "failed to record the card authorization",
			slog.String("provider_card_authorization_id", ca.ProviderID))
	}
	ca.ID = recorded
	return ca, nil
}

// Get implements [entity.CardAuthorizationRepository].
func (r *CardAuthorizationRepository) Get(ctx context.Context, id string) (*entity.CardAuthorization, error) {
	providerID, err := r.providerID(ctx, id)
	if err != nil {
		return nil, err
	}
	ca, err := r.provider.Get(ctx, providerID)
	if err != nil {
		return nil, err
	}
	ca.ID = id
	return ca, nil
}

// GetByProviderCardAuthorizationID implements
// [entity.CardAuthorizationRepository].
func (r *CardAuthorizationRepository) GetByProviderCardAuthorizationID(ctx context.Context, providerID string) (*entity.CardAuthorization, error) {
	var id string
	if err := r.db.Pool.QueryRow(ctx, cardAuthorizationIDQuery, providerID).Scan(&id); err != nil {
		return nil, toAppErr(err, "failed to find the card authorization",
			slog.String("provider_card_authorization_id", providerID))
	}
	ca, err := r.provider.Get(ctx, providerID)
	if err != nil {
		return nil, err
	}
	ca.ID = id
	return ca, nil
}

// Capture implements [entity.CardAuthorizationRepository].
func (r *CardAuthorizationRepository) Capture(ctx context.Context, id string) error {
	providerID, err := r.providerID(ctx, id)
	if err != nil {
		return err
	}
	return r.provider.Capture(ctx, providerID)
}

// Release implements [entity.CardAuthorizationRepository].
func (r *CardAuthorizationRepository) Release(ctx context.Context, id string) error {
	providerID, err := r.providerID(ctx, id)
	if err != nil {
		return err
	}
	return r.provider.Release(ctx, providerID)
}

// providerID returns the payment provider's ID of the card authorization with
// the id. An id that is not a UUID names no card authorization.
func (r *CardAuthorizationRepository) providerID(ctx context.Context, id string) (string, error) {
	if _, err := uuid.Parse(id); err != nil {
		return "", apperr.New(codes.NotFound, "no card authorization has the id", slog.String("card_authorization_id", id))
	}
	var providerID string
	if err := r.db.Pool.QueryRow(ctx, cardAuthorizationProviderIDQuery, id).Scan(&providerID); err != nil {
		return "", toAppErr(err, "failed to find the card authorization", slog.String("card_authorization_id", id))
	}
	return providerID, nil
}
