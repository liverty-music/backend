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

func TestRequireUserIDMatch(t *testing.T) {
	t.Parallel()

	const callerUserID = "11111111-1111-1111-1111-111111111111"

	tests := []struct {
		name      string
		reqUserID string
		wantErr   bool
		wantCode  connect.Code
	}{
		{
			name:      "matching user_id passes",
			reqUserID: callerUserID,
			wantErr:   false,
		},
		{
			name:      "mismatched user_id returns PermissionDenied",
			reqUserID: "22222222-2222-2222-2222-222222222222",
			wantErr:   true,
			wantCode:  connect.CodePermissionDenied,
		},
		{
			name:      "empty user_id returns InvalidArgument",
			reqUserID: "",
			wantErr:   true,
			wantCode:  connect.CodeInvalidArgument,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := mapper.RequireUserIDMatch(callerUserID, tt.reqUserID)

			if !tt.wantErr {
				assert.NoError(t, err)
				return
			}
			assert.Error(t, err)
			var connectErr *connect.Error
			assert.ErrorAs(t, err, &connectErr)
			assert.Equal(t, tt.wantCode, connectErr.Code())
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
				ExternalID:        "ext-1",
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
				ExternalID:        "ext-2",
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
			assert.Equal(t, tt.user.ExternalID, pb.GetExternalId().GetValue())
			assert.Equal(t, tt.user.Name, pb.GetName())
			assert.Equal(t, tt.wantPreferredLanguage, pb.GetPreferredLanguage())
		})
	}
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
			assert.Equal(t, tt.claims.Sub, got.ExternalID)
			assert.Equal(t, tt.claims.Email, got.Email)
			assert.Equal(t, tt.claims.Name, got.Name)
			assert.Equal(t, tt.wantLang, got.PreferredLanguage)
		})
	}
}
