package mapper

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/liverty-music/backend/internal/entity"
)

// PasskeyToProto maps a recorded Passkey to its wire form: its provider
// passkey id, its provider name when the provider list names its AAGUID, and
// its create time.
func PasskeyToProto(p *entity.Passkey) *entityv1.Passkey {
	out := &entityv1.Passkey{
		Id:         &entityv1.PasskeyId{Value: p.ProviderPasskeyID},
		CreateTime: timestamppb.New(p.CreateTime),
	}
	if name := p.ProviderName(); name != "" {
		out.ProviderName = &entityv1.PasskeyProviderName{Value: name}
	}
	return out
}

// PasskeysToProto maps recorded Passkeys to their wire form, keeping order.
func PasskeysToProto(passkeys []*entity.Passkey) []*entityv1.Passkey {
	out := make([]*entityv1.Passkey, 0, len(passkeys))
	for _, p := range passkeys {
		out = append(out, PasskeyToProto(p))
	}
	return out
}
