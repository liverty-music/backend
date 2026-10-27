package mapper

import (
	proto "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"github.com/liverty-music/backend/internal/entity"
)

// deletionBlockerKindToProto maps each blocker kind to its proto enum value.
var deletionBlockerKindToProto = map[entity.DeletionBlockerKind]proto.DeletionBlocker_Kind{
	entity.DeletionBlockerKindUpcomingTicket:      proto.DeletionBlocker_KIND_UPCOMING_TICKET,
	entity.DeletionBlockerKindListedResale:        proto.DeletionBlocker_KIND_LISTED_RESALE,
	entity.DeletionBlockerKindPendingLotteryEntry: proto.DeletionBlocker_KIND_PENDING_LOTTERY_ENTRY,
	entity.DeletionBlockerKindPaymentInProgress:   proto.DeletionBlocker_KIND_PAYMENT_IN_PROGRESS,
}

// DeletionBlockersToProto converts deletion blockers to protobuf, keeping
// their order. A kind with no proto value maps to KIND_UNSPECIFIED, which
// response validation rejects.
func DeletionBlockersToProto(blockers []entity.DeletionBlocker) []*proto.DeletionBlocker {
	pbs := make([]*proto.DeletionBlocker, len(blockers))
	for i, b := range blockers {
		pbs[i] = &proto.DeletionBlocker{
			Kind:  deletionBlockerKindToProto[b.Kind],
			Count: int32(b.Count),
		}
	}
	return pbs
}
