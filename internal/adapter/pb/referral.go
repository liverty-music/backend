package pb

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	organizerticketsalev1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/organizer/ticket_sale/v1"

	"github.com/liverty-music/backend/internal/entity"
)

// ToReferralCode maps a request's optional referral code to the domain
// value; nil when the request carries none. The format was checked by
// protovalidate at the boundary.
func ToReferralCode(c *entityv1.ReferralCode) *entity.ReferralCode {
	if c == nil {
		return nil
	}
	code := entity.ReferralCode(c.GetValue())
	return &code
}

// FromReferralCodeStats maps an event's purchases per referral code to
// Protobuf, keeping their order; the direct group carries no code.
func FromReferralCodeStats(stats []entity.ReferralCodeStats) []*organizerticketsalev1.ReferralCodeStats {
	out := make([]*organizerticketsalev1.ReferralCodeStats, 0, len(stats))
	for _, s := range stats {
		pb := &organizerticketsalev1.ReferralCodeStats{
			OrderCount:  int32(s.OrderCount),
			TicketCount: int32(s.TicketCount),
			Amount:      s.Amount,
		}
		if s.Code != nil {
			pb.Code = &entityv1.ReferralCode{Value: string(*s.Code)}
		}
		out = append(out, pb)
	}
	return out
}
