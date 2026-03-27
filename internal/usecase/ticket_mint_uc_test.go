package usecase_test

import (
	"context"
	"testing"

	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const (
	mintTestEventID   = "event-123"
	mintTestUserID    = "user-456"
	mintTestAddress   = "0xAbCdEf0123456789AbCdEf0123456789AbCdEf01"
	mintTestTokenID   = uint64(42)
	mintTestTxHash    = "0xdeadbeef"
	mintPlaceholderTx = "0x0000000000000000000000000000000000000000000000000000000000000000"
)

func validMintParams() *usecase.MintTicketParams {
	return &usecase.MintTicketParams{
		EventID:          mintTestEventID,
		UserID:           mintTestUserID,
		RecipientAddress: mintTestAddress,
	}
}

func newTicketUC(t *testing.T, repo *mocks.MockTicketRepository, minter *mocks.MockTicketMinter) usecase.TicketUseCase {
	t.Helper()
	return usecase.NewTicketUseCase(repo, minter, noopMintMetrics{}, newTestLogger(t))
}

// --- validateMintParams ---

func TestTicketUseCase_ValidateMintParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		params  *usecase.MintTicketParams
		wantErr error
	}{
		{
			name:    "invalid Ethereum address returns InvalidArgument",
			params:  &usecase.MintTicketParams{EventID: mintTestEventID, UserID: mintTestUserID, RecipientAddress: "not-an-address"},
			wantErr: apperr.ErrInvalidArgument,
		},
		{
			name:    "valid params returns nil",
			params:  validMintParams(),
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			uc := usecase.NewTicketUseCase(nil, nil, noopMintMetrics{}, newTestLogger(t))

			err := usecase.ExportedValidateMintParams(uc, tt.params)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// --- checkExistingTicket ---

func TestTicketUseCase_CheckExistingTicket(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	existingTicket := &entity.Ticket{ID: "ticket-1", EventID: mintTestEventID, UserID: mintTestUserID}

	tests := []struct {
		name      string
		setup     func(*mocks.MockTicketRepository)
		wantFound bool
		wantErr   error
	}{
		{
			name: "ticket exists returns ticket and found=true",
			setup: func(r *mocks.MockTicketRepository) {
				r.EXPECT().GetByEventAndUser(ctx, mintTestEventID, mintTestUserID).Return(existingTicket, nil).Once()
			},
			wantFound: true,
		},
		{
			name: "ticket not found and event exists returns found=false",
			setup: func(r *mocks.MockTicketRepository) {
				r.EXPECT().GetByEventAndUser(ctx, mintTestEventID, mintTestUserID).Return(nil, apperr.ErrNotFound).Once()
				r.EXPECT().EventExists(ctx, mintTestEventID).Return(true, nil).Once()
			},
			wantFound: false,
		},
		{
			name: "ticket not found and event missing returns NotFound",
			setup: func(r *mocks.MockTicketRepository) {
				r.EXPECT().GetByEventAndUser(ctx, mintTestEventID, mintTestUserID).Return(nil, apperr.ErrNotFound).Once()
				r.EXPECT().EventExists(ctx, mintTestEventID).Return(false, nil).Once()
			},
			wantErr: apperr.ErrNotFound,
		},
		{
			name: "database error propagates",
			setup: func(r *mocks.MockTicketRepository) {
				r.EXPECT().GetByEventAndUser(ctx, mintTestEventID, mintTestUserID).
					Return(nil, apperr.New(codes.Internal, "db error")).Once()
			},
			wantErr: apperr.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo := mocks.NewMockTicketRepository(t)
			tt.setup(repo)
			uc := newTicketUC(t, repo, mocks.NewMockTicketMinter(t))

			ticket, found, err := usecase.ExportedCheckExistingTicket(uc, ctx, mintTestEventID, mintTestUserID)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantFound, found)
			if tt.wantFound {
				assert.Equal(t, existingTicket, ticket)
			} else {
				assert.Nil(t, ticket)
			}
		})
	}
}

// --- mintOrReconcile ---

func TestTicketUseCase_MintOrReconcile(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name       string
		setup      func(*mocks.MockTicketMinter)
		wantTxHash string
		wantErr    error
	}{
		{
			name: "fresh mint returns txHash from minter",
			setup: func(m *mocks.MockTicketMinter) {
				m.EXPECT().IsTokenMinted(ctx, mock.AnythingOfType("uint64")).Return(false, nil).Once()
				m.EXPECT().Mint(ctx, mintTestAddress, mock.AnythingOfType("uint64")).Return(mintTestTxHash, nil).Once()
			},
			wantTxHash: mintTestTxHash,
		},
		{
			name: "reconcile with correct owner returns placeholder txHash",
			setup: func(m *mocks.MockTicketMinter) {
				m.EXPECT().IsTokenMinted(ctx, mock.AnythingOfType("uint64")).Return(true, nil).Once()
				m.EXPECT().OwnerOf(ctx, mock.AnythingOfType("uint64")).Return(mintTestAddress, nil).Once()
			},
			wantTxHash: mintPlaceholderTx,
		},
		{
			name: "reconcile with wrong owner returns PermissionDenied",
			setup: func(m *mocks.MockTicketMinter) {
				m.EXPECT().IsTokenMinted(ctx, mock.AnythingOfType("uint64")).Return(true, nil).Once()
				m.EXPECT().OwnerOf(ctx, mock.AnythingOfType("uint64")).
					Return("0x0000000000000000000000000000000000000000", nil).Once()
			},
			wantErr: apperr.ErrPermissionDenied,
		},
		{
			name: "on-chain check failure propagates error",
			setup: func(m *mocks.MockTicketMinter) {
				m.EXPECT().IsTokenMinted(ctx, mock.AnythingOfType("uint64")).
					Return(false, apperr.New(codes.Internal, "rpc error")).Once()
			},
			wantErr: apperr.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			minter := mocks.NewMockTicketMinter(t)
			tt.setup(minter)
			uc := newTicketUC(t, mocks.NewMockTicketRepository(t), minter)

			txHash, err := usecase.ExportedMintOrReconcile(uc, ctx, validMintParams())

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantTxHash, txHash)
		})
	}
}

// --- persistTicket ---

func TestTicketUseCase_PersistTicket(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	savedTicket := &entity.Ticket{ID: "ticket-new", EventID: mintTestEventID, UserID: mintTestUserID}
	existingTicket := &entity.Ticket{ID: "ticket-existing", EventID: mintTestEventID, UserID: mintTestUserID}

	tests := []struct {
		name       string
		setup      func(*mocks.MockTicketRepository)
		wantTicket *entity.Ticket
		wantErr    error
	}{
		{
			name: "successful insert returns ticket",
			setup: func(r *mocks.MockTicketRepository) {
				r.EXPECT().Create(ctx, mock.AnythingOfType("*entity.NewTicket")).
					Return(savedTicket, nil).Once()
			},
			wantTicket: savedTicket,
		},
		{
			name: "concurrent duplicate returns existing ticket",
			setup: func(r *mocks.MockTicketRepository) {
				r.EXPECT().Create(ctx, mock.AnythingOfType("*entity.NewTicket")).
					Return(nil, apperr.ErrAlreadyExists).Once()
				r.EXPECT().GetByEventAndUser(ctx, mintTestEventID, mintTestUserID).
					Return(existingTicket, nil).Once()
			},
			wantTicket: existingTicket,
		},
		{
			name: "database write error propagates",
			setup: func(r *mocks.MockTicketRepository) {
				r.EXPECT().Create(ctx, mock.AnythingOfType("*entity.NewTicket")).
					Return(nil, apperr.New(codes.Internal, "write error")).Once()
			},
			wantErr: apperr.ErrInternal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			repo := mocks.NewMockTicketRepository(t)
			tt.setup(repo)
			uc := newTicketUC(t, repo, mocks.NewMockTicketMinter(t))

			ticket, err := usecase.ExportedPersistTicket(uc, ctx, validMintParams(), mintTestTokenID, mintTestTxHash)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantTicket, ticket)
		})
	}
}
