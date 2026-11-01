package pb

import (
	entityv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/entity/v1"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/liverty-music/backend/internal/entity"
)

// FromPasskey maps a recorded Passkey to its wire form: its provider
// passkey id, its User, its provider name when the provider list names its
// AAGUID, its name when the identity provider gave one, and its create time.
func FromPasskey(p *entity.Passkey) *entityv1.Passkey {
	out := &entityv1.Passkey{
		Id:         &entityv1.PasskeyId{Value: p.ProviderPasskeyID},
		CreateTime: timestamppb.New(p.CreateTime),
		UserId:     &entityv1.UserId{Value: p.UserID},
	}
	if name := p.ProviderName(); name != "" {
		out.ProviderName = &entityv1.PasskeyProviderName{Value: name}
	}
	if p.Name != nil {
		out.Name = &entityv1.PasskeyName{Value: *p.Name}
	}
	return out
}

// FromPasskeys maps recorded Passkeys to their wire form, keeping order.
func FromPasskeys(passkeys []*entity.Passkey) []*entityv1.Passkey {
	out := make([]*entityv1.Passkey, 0, len(passkeys))
	for _, p := range passkeys {
		out = append(out, FromPasskey(p))
	}
	return out
}
