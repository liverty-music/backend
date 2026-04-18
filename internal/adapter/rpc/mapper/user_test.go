package mapper_test

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/liverty-music/backend/internal/adapter/rpc/mapper"
	"github.com/liverty-music/backend/internal/infrastructure/auth"
	"github.com/stretchr/testify/assert"
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
