package usecase_test

import (
	"context"
	"errors"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newTestTicketUC(t *testing.T, repo *mocks.MockTicketRepository, minter *mocks.MockTicketMinter) usecase.TicketUseCase {
	t.Helper()
	logger, _ := logging.New()
	return usecase.NewTicketUseCase(repo, minter, logger)
}

func TestMintTicket_Validation(t *testing.T) {
	t.Parallel()

	logger, _ := logging.New()

	tests := []struct {
		name   string
		params *usecase.MintTicketParams
	}{
		{"nil params", nil},
		{"empty EventID", &usecase.MintTicketParams{UserID: "u1", RecipientAddress: "0xabc"}},
		{"empty UserID", &usecase.MintTicketParams{EventID: "e1", RecipientAddress: "0xabc"}},
		{"empty RecipientAddress", &usecase.MintTicketParams{EventID: "e1", UserID: "u1"}},
		{"invalid RecipientAddress", &usecase.MintTicketParams{EventID: "e1", UserID: "u1", RecipientAddress: "not-an-address"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			uc := usecase.NewTicketUseCase(nil, nil, logger)
			_, err := uc.MintTicket(context.Background(), tc.params)
			assert.Error(t, err)
			assert.True(t, errors.Is(err, apperr.ErrInvalidArgument), "expected InvalidArgument, got %v", err)
		})
	}
}

func TestMintTicket_IdempotencyDB(t *testing.T) {
	t.Parallel()

	// When a ticket already exists in the DB, return it without minting.
	repo := mocks.NewMockTicketRepository(t)
	minter := mocks.NewMockTicketMinter(t)
	uc := newTestTicketUC(t, repo, minter)

	existing := &entity.Ticket{ID: "ticket-1", EventID: "event-1", UserID: "user-1", TokenID: 42}
	repo.EXPECT().GetByEventAndUser(anyCtx, "event-1", "user-1").Return(existing, nil)

	got, err := uc.MintTicket(context.Background(), &usecase.MintTicketParams{
		EventID:          "event-1",
		UserID:           "user-1",
		RecipientAddress: "0xaAbBcCdDeEfF0011223344556677889900aAbBcC",
	})

	require.NoError(t, err)
	assert.Equal(t, existing, got)
	minter.AssertNotCalled(t, "Mint")
	minter.AssertNotCalled(t, "IsTokenMinted")
}

func TestMintTicket_HappyPath(t *testing.T) {
	t.Parallel()

	repo := mocks.NewMockTicketRepository(t)
	minter := mocks.NewMockTicketMinter(t)
	uc := newTestTicketUC(t, repo, minter)

	created := &entity.Ticket{ID: "ticket-2", EventID: "event-1", UserID: "user-1", TokenID: 99}

	repo.EXPECT().GetByEventAndUser(anyCtx, "event-1", "user-1").Return(nil, apperr.ErrNotFound)
	repo.EXPECT().EventExists(anyCtx, "event-1").Return(true, nil)
	minter.EXPECT().IsTokenMinted(anyCtx, mock.AnythingOfType("uint64")).Return(false, nil)
	minter.EXPECT().Mint(anyCtx, "0xaAbBcCdDeEfF0011223344556677889900aAbBcC", mock.AnythingOfType("uint64")).Return("0xdeadbeef", nil)
	repo.EXPECT().Create(anyCtx, mock.MatchedBy(func(p *entity.NewTicket) bool {
		return p.EventID == "event-1" && p.UserID == "user-1" && p.TxHash == "0xdeadbeef"
	})).Return(created, nil)

	got, err := uc.MintTicket(context.Background(), &usecase.MintTicketParams{
		EventID:          "event-1",
		UserID:           "user-1",
		RecipientAddress: "0xaAbBcCdDeEfF0011223344556677889900aAbBcC",
	})

	require.NoError(t, err)
	assert.Equal(t, created, got)
}

func TestMintTicket_EventNotFound(t *testing.T) {
	t.Parallel()

	// When the event does not exist, return NotFound before on-chain mint.
	repo := mocks.NewMockTicketRepository(t)
	minter := mocks.NewMockTicketMinter(t)
	uc := newTestTicketUC(t, repo, minter)

	repo.EXPECT().GetByEventAndUser(anyCtx, "event-999", "user-1").Return(nil, apperr.ErrNotFound)
	repo.EXPECT().EventExists(anyCtx, "event-999").Return(false, nil)

	_, err := uc.MintTicket(context.Background(), &usecase.MintTicketParams{
		EventID:          "event-999",
		UserID:           "user-1",
		RecipientAddress: "0xaAbBcCdDeEfF0011223344556677889900aAbBcC",
	})

	require.Error(t, err)
	assert.True(t, errors.Is(err, apperr.ErrNotFound), "expected NotFound, got %v", err)
	minter.AssertNotCalled(t, "Mint")
	minter.AssertNotCalled(t, "IsTokenMinted")
}

func TestMintTicket_AlreadyMintedOwnerMatches(t *testing.T) {
	t.Parallel()

	// Token is on-chain but no DB record — owner matches → reconcile and save.
	repo := mocks.NewMockTicketRepository(t)
	minter := mocks.NewMockTicketMinter(t)
	uc := newTestTicketUC(t, repo, minter)

	recipient := "0xaAbBcCdDeEfF0011223344556677889900aAbBcC"
	created := &entity.Ticket{ID: "ticket-3", EventID: "event-1", UserID: "user-1"}

	repo.EXPECT().GetByEventAndUser(anyCtx, "event-1", "user-1").Return(nil, apperr.ErrNotFound)
	repo.EXPECT().EventExists(anyCtx, "event-1").Return(true, nil)
	minter.EXPECT().IsTokenMinted(anyCtx, mock.AnythingOfType("uint64")).Return(true, nil)
	// OwnerOf returns the same address as RecipientAddress (case-insensitive).
	minter.EXPECT().OwnerOf(anyCtx, mock.AnythingOfType("uint64")).Return(recipient, nil)
	repo.EXPECT().Create(anyCtx, mock.MatchedBy(func(p *entity.NewTicket) bool {
		return p.EventID == "event-1" && p.UserID == "user-1"
	})).Return(created, nil)

	got, err := uc.MintTicket(context.Background(), &usecase.MintTicketParams{
		EventID:          "event-1",
		UserID:           "user-1",
		RecipientAddress: recipient,
	})

	require.NoError(t, err)
	assert.Equal(t, created, got)
	minter.AssertNotCalled(t, "Mint")
}

func TestMintTicket_AlreadyMintedOwnerMismatch(t *testing.T) {
	t.Parallel()

	// Token is on-chain but owned by a different address → PermissionDenied.
	repo := mocks.NewMockTicketRepository(t)
	minter := mocks.NewMockTicketMinter(t)
	uc := newTestTicketUC(t, repo, minter)

	repo.EXPECT().GetByEventAndUser(anyCtx, "event-1", "user-1").Return(nil, apperr.ErrNotFound)
	repo.EXPECT().EventExists(anyCtx, "event-1").Return(true, nil)
	minter.EXPECT().IsTokenMinted(anyCtx, mock.AnythingOfType("uint64")).Return(true, nil)
	minter.EXPECT().OwnerOf(anyCtx, mock.AnythingOfType("uint64")).Return("0x0000000000000000000000000000000000000001", nil)

	_, err := uc.MintTicket(context.Background(), &usecase.MintTicketParams{
		EventID:          "event-1",
		UserID:           "user-1",
		RecipientAddress: "0xaAbBcCdDeEfF0011223344556677889900aAbBcC",
	})

	require.Error(t, err)
	assert.True(t, errors.Is(err, apperr.ErrPermissionDenied), "expected PermissionDenied, got %v", err)
}

func TestMintTicket_ConcurrentMintIdempotency(t *testing.T) {
	t.Parallel()

	// If Create returns AlreadyExists (concurrent mint), fetch and return the existing record.
	repo := mocks.NewMockTicketRepository(t)
	minter := mocks.NewMockTicketMinter(t)
	uc := newTestTicketUC(t, repo, minter)

	existing := &entity.Ticket{ID: "ticket-4", EventID: "event-1", UserID: "user-1"}

	repo.EXPECT().GetByEventAndUser(anyCtx, "event-1", "user-1").Return(nil, apperr.ErrNotFound).Once()
	repo.EXPECT().EventExists(anyCtx, "event-1").Return(true, nil)
	minter.EXPECT().IsTokenMinted(anyCtx, mock.AnythingOfType("uint64")).Return(false, nil)
	minter.EXPECT().Mint(anyCtx, mock.Anything, mock.AnythingOfType("uint64")).Return("0xdeadbeef", nil)
	repo.EXPECT().Create(anyCtx, mock.Anything).Return(nil, apperr.ErrAlreadyExists)
	repo.EXPECT().GetByEventAndUser(anyCtx, "event-1", "user-1").Return(existing, nil).Once()

	got, err := uc.MintTicket(context.Background(), &usecase.MintTicketParams{
		EventID:          "event-1",
		UserID:           "user-1",
		RecipientAddress: "0xaAbBcCdDeEfF0011223344556677889900aAbBcC",
	})

	require.NoError(t, err)
	assert.Equal(t, existing, got)
}

func TestMintTicket_IsTokenMintedError(t *testing.T) {
	t.Parallel()

	// RPC error from IsTokenMinted should be propagated as Internal.
	repo := mocks.NewMockTicketRepository(t)
	minter := mocks.NewMockTicketMinter(t)
	uc := newTestTicketUC(t, repo, minter)

	repo.EXPECT().GetByEventAndUser(anyCtx, "event-1", "user-1").Return(nil, apperr.ErrNotFound)
	repo.EXPECT().EventExists(anyCtx, "event-1").Return(true, nil)
	minter.EXPECT().IsTokenMinted(anyCtx, mock.AnythingOfType("uint64")).Return(false, errors.New("rpc error: node timeout"))

	_, err := uc.MintTicket(context.Background(), &usecase.MintTicketParams{
		EventID:          "event-1",
		UserID:           "user-1",
		RecipientAddress: "0xaAbBcCdDeEfF0011223344556677889900aAbBcC",
	})

	require.Error(t, err)
	assert.True(t, errors.Is(err, apperr.ErrInternal), "expected Internal, got %v", err)
}

func TestGetTicket_EmptyID(t *testing.T) {
	t.Parallel()
	logger, _ := logging.New()
	uc := usecase.NewTicketUseCase(nil, nil, logger)
	_, err := uc.GetTicket(context.Background(), "")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, apperr.ErrInvalidArgument))
}

func TestListTicketsForUser_EmptyID(t *testing.T) {
	t.Parallel()
	logger, _ := logging.New()
	uc := usecase.NewTicketUseCase(nil, nil, logger)
	_, err := uc.ListTicketsForUser(context.Background(), "")
	assert.Error(t, err)
	assert.True(t, errors.Is(err, apperr.ErrInvalidArgument))
}
