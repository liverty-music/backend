package mapper_test

import (
	"testing"

	adminorderv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/admin/order/v1"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/usecase"
	"github.com/stretchr/testify/assert"
)

func TestProtoRefundReasonToDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args adminorderv1.RefundReason
		want usecase.RefundReason
	}{
		{
			name: "return Unspecified for UNSPECIFIED",
			args: adminorderv1.RefundReason_REFUND_REASON_UNSPECIFIED,
			want: usecase.RefundReasonUnspecified,
		},
		{
			name: "return Cancellation for CANCELLATION",
			args: adminorderv1.RefundReason_REFUND_REASON_CANCELLATION,
			want: usecase.RefundReasonCancellation,
		},
		{
			name: "return Dispute for DISPUTE",
			args: adminorderv1.RefundReason_REFUND_REASON_DISPUTE,
			want: usecase.RefundReasonDispute,
		},
		{
			// 2 was the withdrawn postponement-window reason; an old client
			// sending it must be rejected by the use case as Unspecified.
			name: "return Unspecified for the withdrawn value 2",
			args: adminorderv1.RefundReason(2),
			want: usecase.RefundReasonUnspecified,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, mapper.ProtoRefundReasonToDomain(tt.args))
		})
	}
}
