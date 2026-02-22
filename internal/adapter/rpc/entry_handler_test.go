package rpc_test

import (
	"context"
	"testing"

	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	entryv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/entry/v1"
	"connectrpc.com/connect"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/entity/mocks"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	ucmocks "github.com/liverty-music/backend/internal/usecase/mocks"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/pannpers/go-logging/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func entryAuthedCtx(sub string) context.Context {
	return auth.WithClaims(context.Background(), &auth.Claims{Sub: sub})
}

func TestEntryHandler_VerifyEntry(t *testing.T) {
	logger, _ := logging.New()

	tests := []struct {
		name     string
		req      *entryv1.VerifyEntryRequest
		setup    func(uc *ucmocks.MockEntryUseCase)
		wantCode connect.Code
		wantErr  bool
		wantOK   bool
	}{
		{
			name: "success - verified",
			req: &entryv1.VerifyEntryRequest{
				EventId:           &entityv1.EventId{Value: "event-1"},
				ProofJson:         `{"pi_a":["1","2"],"pi_b":[["3","4"],["5","6"]],"pi_c":["7","8"]}`,
				PublicSignalsJson: `["111","222","333"]`,
			},
			setup: func(uc *ucmocks.MockEntryUseCase) {
				uc.EXPECT().VerifyEntry(mock.Anything, mock.Anything).Return(&usecase.VerifyEntryResult{
					Verified: true,
					Message:  "entry verified",
				}, nil)
			},
			wantErr: false,
			wantOK:  true,
		},
		{
			name: "success - not verified (duplicate nullifier)",
			req: &entryv1.VerifyEntryRequest{
				EventId:           &entityv1.EventId{Value: "event-1"},
				ProofJson:         `{"pi_a":["1","2"]}`,
				PublicSignalsJson: `["111","222","333"]`,
			},
			setup: func(uc *ucmocks.MockEntryUseCase) {
				uc.EXPECT().VerifyEntry(mock.Anything, mock.Anything).Return(&usecase.VerifyEntryResult{
					Verified: false,
					Message:  "already checked in for this event",
				}, nil)
			},
			wantErr: false,
			wantOK:  false,
		},
		{
			name:     "nil request",
			req:      nil,
			setup:    func(_ *ucmocks.MockEntryUseCase) {},
			wantCode: connect.CodeInvalidArgument,
			wantErr:  true,
		},
		{
			name:     "missing event_id",
			req:      &entryv1.VerifyEntryRequest{ProofJson: "x", PublicSignalsJson: "y"},
			setup:    func(_ *ucmocks.MockEntryUseCase) {},
			wantCode: connect.CodeInvalidArgument,
			wantErr:  true,
		},
		{
			name:     "missing proof_json",
			req:      &entryv1.VerifyEntryRequest{EventId: &entityv1.EventId{Value: "event-1"}, PublicSignalsJson: "y"},
			setup:    func(_ *ucmocks.MockEntryUseCase) {},
			wantCode: connect.CodeInvalidArgument,
			wantErr:  true,
		},
		{
			name:     "missing public_signals_json",
			req:      &entryv1.VerifyEntryRequest{EventId: &entityv1.EventId{Value: "event-1"}, ProofJson: "x"},
			setup:    func(_ *ucmocks.MockEntryUseCase) {},
			wantCode: connect.CodeInvalidArgument,
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entryUC := ucmocks.NewMockEntryUseCase(t)
			userRepo := mocks.NewMockUserRepository(t)
			tc.setup(entryUC)

			h := handler.NewEntryHandler(entryUC, userRepo, logger)

			var req *connect.Request[entryv1.VerifyEntryRequest]
			if tc.req != nil {
				req = connect.NewRequest(tc.req)
			}

			resp, err := h.VerifyEntry(context.Background(), req)
			if tc.wantErr {
				require.Error(t, err)
				assert.Equal(t, tc.wantCode, connect.CodeOf(err))
				return
			}

			require.NoError(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, tc.wantOK, resp.Msg.Verified)
		})
	}
}

func TestEntryHandler_GetMerklePath(t *testing.T) {
	logger, _ := logging.New()

	tests := []struct {
		name     string
		ctx      context.Context
		req      *entryv1.GetMerklePathRequest
		setup    func(uc *ucmocks.MockEntryUseCase, ur *mocks.MockUserRepository)
		wantCode connect.Code
		wantErr  bool
	}{
		{
			name: "success",
			ctx:  entryAuthedCtx("ext-user-1"),
			req: &entryv1.GetMerklePathRequest{
				EventId: &entityv1.EventId{Value: "event-1"},
			},
			setup: func(uc *ucmocks.MockEntryUseCase, ur *mocks.MockUserRepository) {
				ur.EXPECT().GetByExternalID(mock.Anything, "ext-user-1").Return(&entity.User{
					ID:         "user-uuid-1",
					ExternalID: "ext-user-1",
				}, nil)
				uc.EXPECT().GetMerklePath(mock.Anything, "event-1", "user-uuid-1").Return(&usecase.MerklePathResult{
					MerkleRoot:   []byte("root-hash"),
					PathElements: [][]byte{[]byte("sibling-0"), []byte("sibling-1")},
					PathIndices:  []uint32{0, 1},
					Leaf:         []byte("leaf-hash"),
				}, nil)
			},
			wantErr: false,
		},
		{
			name:     "unauthenticated",
			ctx:      context.Background(),
			req:      &entryv1.GetMerklePathRequest{EventId: &entityv1.EventId{Value: "event-1"}},
			setup:    func(_ *ucmocks.MockEntryUseCase, _ *mocks.MockUserRepository) {},
			wantCode: connect.CodeUnauthenticated,
			wantErr:  true,
		},
		{
			name:     "nil request",
			ctx:      entryAuthedCtx("ext-user-1"),
			req:      nil,
			setup:    func(_ *ucmocks.MockEntryUseCase, _ *mocks.MockUserRepository) {},
			wantCode: connect.CodeInvalidArgument,
			wantErr:  true,
		},
		{
			name:     "missing event_id",
			ctx:      entryAuthedCtx("ext-user-1"),
			req:      &entryv1.GetMerklePathRequest{},
			setup:    func(_ *ucmocks.MockEntryUseCase, _ *mocks.MockUserRepository) {},
			wantCode: connect.CodeInvalidArgument,
			wantErr:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			entryUC := ucmocks.NewMockEntryUseCase(t)
			userRepo := mocks.NewMockUserRepository(t)
			tc.setup(entryUC, userRepo)

			h := handler.NewEntryHandler(entryUC, userRepo, logger)

			var req *connect.Request[entryv1.GetMerklePathRequest]
			if tc.req != nil {
				req = connect.NewRequest(tc.req)
			}

			resp, err := h.GetMerklePath(tc.ctx, req)
			if tc.wantErr {
				require.Error(t, err)
				assert.Equal(t, tc.wantCode, connect.CodeOf(err))
				return
			}

			require.NoError(t, err)
			assert.NotNil(t, resp)
			assert.Equal(t, []byte("root-hash"), resp.Msg.MerkleRoot)
			assert.Len(t, resp.Msg.PathElements, 2)
			assert.Len(t, resp.Msg.PathIndices, 2)
			assert.Equal(t, []byte("leaf-hash"), resp.Msg.Leaf)
		})
	}
}
