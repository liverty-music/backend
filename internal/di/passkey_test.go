package di_test

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/liverty-music/backend/internal/di"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// envNames returns every envconfig name declared in the struct type t and
// the structs it embeds or holds.
func envNames(t reflect.Type) []string {
	var names []string
	for f := range t.Fields() {
		if name := f.Tag.Get("envconfig"); name != "" {
			names = append(names, name)
		}
		if ft := f.Type; ft.Kind() == reflect.Struct {
			names = append(names, envNames(ft)...)
		}
	}
	return names
}

func TestPasskeyRecoveryWiring(t *testing.T) {
	t.Parallel()

	t.Run("without the fan-passkey-recovery key the consumer starts with the recovery link handler disabled", func(t *testing.T) {
		t.Parallel()
		cfg := &config.ConsumerConfig{}
		cfg.Postmark.ServerToken = "server-token"

		sender, err := di.NewPasskeyRegistrationLinkSender(context.Background(), cfg, di.NewBootstrapLoggerTo(io.Discard))

		require.NoError(t, err)
		assert.Nil(t, sender, "no identity-provider recovery client is built")
		assert.Empty(t, di.PasskeyRecoveryBehaviorNames(false), "no send-passkey-recovery-link behavior")
	})

	t.Run("with the key the consumer registers the send-passkey-recovery-link behavior", func(t *testing.T) {
		t.Parallel()

		assert.Equal(t, []string{"send-passkey-recovery-link USER.passkey_recovery_requested"}, di.PasskeyRecoveryBehaviorNames(true))
	})

	t.Run("the fan API configuration never reads the fan-passkey-recovery key", func(t *testing.T) {
		t.Parallel()

		for _, name := range envNames(reflect.TypeFor[config.ServerConfig]()) {
			assert.False(t, strings.Contains(name, "FAN_PASSKEY_RECOVERY"), "ServerConfig declares %s", name)
		}
		assert.Contains(t, envNames(reflect.TypeFor[config.ConsumerConfig]()), "ZITADEL_MACHINE_KEY_FOR_FAN_PASSKEY_RECOVERY_PATH")
	})
}
