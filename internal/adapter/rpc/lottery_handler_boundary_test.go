package rpc_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	lotteryv1connect "buf.build/gen/go/liverty-music/schema/connectrpc/go/liverty_music/rpc/lottery/v1/lotteryv1connect"
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	lotteryv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/lottery/v1"
	"connectrpc.com/connect"
	"connectrpc.com/validate"
	handler "github.com/liverty-music/backend/internal/adapter/rpc"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

// TestLotteryHandler_Apply_Boundary drives Apply through a real Connect server
// with the production validation interceptor in front, so it checks what the
// fan-facing boundary accepts — not only what the handler does with a request
// that has already passed validation.
func TestLotteryHandler_Apply_Boundary(t *testing.T) {
	t.Parallel()

	const sub = "ext-user-boundary"

	validReq := func() *lotteryv1.ApplyRequest {
		return &lotteryv1.ApplyRequest{
			PhaseId:              &entityv1.LotterySalesPhaseId{Value: "01920000-0000-7000-8000-000000000001"},
			RequestedTicketCount: 2,
			Identity: &entityv1.ApplicantIdentity{
				FullName:    "山田太郎",
				PhoneNumber: "+819012345678",
			},
			Authorization: &entityv1.PaymentAuthorization{
				PaymentIntentRef: "pi_test_ref",
			},
		}
	}

	tests := []struct {
		name        string
		mutate      func(req *lotteryv1.ApplyRequest)
		wantCode    connect.Code
		wantApplied bool
	}{
		{
			// @spec components/adapter/fan/api/rpc/lottery "E.164 phone number"
			name:        "passes the boundary and runs Apply with an E.164 phone number",
			mutate:      func(_ *lotteryv1.ApplyRequest) {},
			wantApplied: true,
		},
		{
			// @spec components/adapter/fan/api/rpc/lottery "Domestic-format phone number"
			name: "return INVALID_ARGUMENT for a domestic-format phone number",
			mutate: func(req *lotteryv1.ApplyRequest) {
				req.Identity.PhoneNumber = "090-1234-5678"
			},
			wantCode: connect.CodeInvalidArgument,
		},
		{
			// @spec components/adapter/fan/api/rpc/lottery "Zero tickets"
			name: "return INVALID_ARGUMENT for a requested ticket count of 0",
			mutate: func(req *lotteryv1.ApplyRequest) {
				req.RequestedTicketCount = 0
			},
			wantCode: connect.CodeInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newLotteryHandlerFixture(t)
			var applied atomic.Int32
			f.lotteryUC.applyFn = func(_ context.Context, in usecase.ApplyInput) (*entity.TicketApplication, error) {
				applied.Add(1)
				return &entity.TicketApplication{
					PhaseID:              in.PhaseID,
					ApplicantID:          in.ApplicantID,
					RequestedTicketCount: in.RequestedTicketCount,
					State:                entity.TicketApplicationStateApplied,
				}, nil
			}
			if tt.wantApplied {
				f.seedUserRepo(sub, "internal-user-boundary")
			}

			client := newLotteryBoundaryClient(t, f.h, sub)
			req := validReq()
			tt.mutate(req)

			resp, err := client.Apply(context.Background(), connect.NewRequest(req))

			if !tt.wantApplied {
				require.Error(t, err)
				assert.Nil(t, resp)
				assert.Equal(t, tt.wantCode, connect.CodeOf(err))
				assert.Zero(t, applied.Load(), "the usecase must not run for an invalid request")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, int32(1), applied.Load())
			assert.True(t, proto.Equal(req.GetPhaseId(), resp.Msg.GetApplication().GetPhaseId()))
		})
	}
}

// newLotteryBoundaryClient serves h behind the validation interceptor, then an
// interceptor standing in for the authn middleware that attaches sub's claims.
func newLotteryBoundaryClient(t *testing.T, h *handler.LotteryHandler, sub string) lotteryv1connect.LotteryServiceClient {
	t.Helper()

	withClaims := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			return next(auth.WithClaims(ctx, &auth.Claims{Sub: sub}), req)
		}
	})
	path, svc := lotteryv1connect.NewLotteryServiceHandler(
		h,
		connect.WithInterceptors(validate.NewInterceptor(), withClaims),
	)
	mux := http.NewServeMux()
	mux.Handle(path, svc)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return lotteryv1connect.NewLotteryServiceClient(srv.Client(), srv.URL)
}
