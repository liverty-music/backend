package mapper_test

import (
	"context"
	"testing"

	userv1 "buf.build/gen/go/liverty-music/schema/protocolbuffers/go/liverty_music/rpc/user/v1"
	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/entity"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetExternalUserID(t *testing.T) {
	t.Parallel()

	type args struct {
		ctx context.Context
	}
	tests := []struct {
		name    string
		args    args
		want    string
		wantErr error
	}{
		{
			name: "valid claims with non-empty Sub",
			args: args{
				ctx: auth.WithClaims(context.Background(), &auth.Claims{Sub: "ext-user-123"}),
			},
			want:    "ext-user-123",
			wantErr: nil,
		},
		{
			name: "missing claims in context",
			args: args{
				ctx: context.Background(),
			},
			want:    "",
			wantErr: connect.NewError(connect.CodeUnauthenticated, nil),
		},
		{
			name: "nil claims in context",
			args: args{
				ctx: auth.WithClaims(context.Background(), nil),
			},
			want:    "",
			wantErr: connect.NewError(connect.CodeUnauthenticated, nil),
		},
		{
			name: "empty Sub claim",
			args: args{
				ctx: auth.WithClaims(context.Background(), &auth.Claims{Sub: "", Email: "test@example.com"}),
			},
			want:    "",
			wantErr: connect.NewError(connect.CodeUnauthenticated, nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := mapper.GetExternalUserID(tt.args.ctx)

			if tt.wantErr == nil {
				assert.NoError(t, err)
				assert.Equal(t, tt.want, got)
			} else {
				assert.Error(t, err)
				var connectErr *connect.Error
				assert.ErrorAs(t, err, &connectErr)
				assert.Equal(t, connect.CodeUnauthenticated, connectErr.Code())
			}
		})
	}
}

func TestUserToProto(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                  string
		user                  *entity.User
		wantNil               bool
		wantPreferredLanguage string // empty when field not yet set (pre-BSR-gen)
	}{
		{
			name:    "nil user returns nil",
			user:    nil,
			wantNil: true,
		},
		{
			name: "user with preferred_language populated",
			user: &entity.User{
				ID:                "u-1",
				Email:             "test@example.com",
				ProviderUserID:    "ext-1",
				Name:              "Taro",
				PreferredLanguage: "ja",
			},
			wantPreferredLanguage: "ja",
		},
		{
			name: "user with empty preferred_language (legacy / not-yet-backfilled row)",
			user: &entity.User{
				ID:                "u-2",
				Email:             "legacy@example.com",
				ProviderUserID:    "ext-2",
				Name:              "Legacy User",
				PreferredLanguage: "",
			},
			wantPreferredLanguage: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pb := mapper.UserToProto(tt.user)

			if tt.wantNil {
				assert.Nil(t, pb)
				return
			}
			require.NotNil(t, pb)
			assert.Equal(t, tt.user.ID, pb.GetId().GetValue())
			assert.Equal(t, tt.user.Email, pb.GetEmail().GetValue())
			assert.Equal(t, tt.user.ProviderUserID, pb.GetProviderUserId().GetValue())
			assert.Equal(t, tt.user.Name, pb.GetName())
			assert.Equal(t, tt.wantPreferredLanguage, pb.GetPreferredLanguage())
		})
	}
}

func TestUserToProto_ProfileFields(t *testing.T) {
	t.Parallel()

	t.Run("user with home and language", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/user "User with home and language"
		pb := mapper.UserToProto(&entity.User{
			ID: "u-1", PreferredLanguage: "ja",
			Home: &entity.Home{CountryCode: "JP", Level1: "JP-13"},
		})

		assert.Equal(t, "ja", pb.GetPreferredLanguage())
		assert.Equal(t, "JP", pb.GetHome().GetCountryCode())
		assert.Equal(t, "JP-13", pb.GetHome().GetLevel_1())
	})

	t.Run("user without optional values", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/user "User without optional values"
		pb := mapper.UserToProto(&entity.User{ID: "u-1"})

		assert.Nil(t, pb.PreferredLanguage)
		assert.Nil(t, pb.GetHome())
		assert.Nil(t, pb.GetFullName())
		assert.Nil(t, pb.GetPhoneNumber())
	})

	t.Run("returning buyer", func(t *testing.T) {
		t.Parallel()
		// @spec components/adapter/fan/api/rpc/user "Returning buyer"
		pb := mapper.UserToProto(&entity.User{
			ID:          "u-1",
			FullName:    "山田 花子",
			PhoneNumber: "+819012345678",
		})

		assert.Equal(t, "山田 花子", pb.GetFullName().GetValue())
		assert.Equal(t, "+819012345678", pb.GetPhoneNumber().GetValue())
	})
}

func TestNewUserFromCreateRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		claims   *auth.Claims
		req      *userv1.CreateRequest
		wantNil  bool
		wantLang string
	}{
		{
			name:    "nil claims returns nil",
			claims:  nil,
			req:     &userv1.CreateRequest{},
			wantNil: true,
		},
		{
			name: "carries preferred_language from request",
			claims: &auth.Claims{
				Sub:   "ext-123",
				Email: "user@example.com",
				Name:  "Hana",
			},
			req: func() *userv1.CreateRequest {
				r := &userv1.CreateRequest{}
				r.SetPreferredLanguage("ja")
				return r
			}(),
			wantLang: "ja",
		},
		{
			name: "absent preferred_language is propagated as empty string",
			claims: &auth.Claims{
				Sub:   "ext-456",
				Email: "empty@example.com",
				Name:  "Empty",
			},
			req:      &userv1.CreateRequest{},
			wantLang: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := mapper.NewUserFromCreateRequest(tt.claims, tt.req)

			if tt.wantNil {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tt.claims.Sub, got.ProviderUserID)
			assert.Equal(t, tt.claims.Email, got.Email)
			assert.Equal(t, tt.claims.Name, got.Name)
			assert.Equal(t, tt.wantLang, got.PreferredLanguage)
		})
	}
}
