package di_test

import (
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/liverty-music/backend/internal/di"
	"github.com/liverty-music/backend/pkg/config"
	"github.com/stretchr/testify/assert"
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

	t.Run("without the identity service no recovery link sender is built", func(t *testing.T) {
		t.Parallel()
		cfg := &config.AccountEmailConfig{}
		cfg.Postmark.ServerToken = "server-token"

		assert.Nil(t, di.NewPasskeyRegistrationLinkSender(cfg, di.NewBootstrapLoggerTo(io.Discard)))
	})

	t.Run("with the identity service the worker builds the sender", func(t *testing.T) {
		t.Parallel()
		cfg := &config.AccountEmailConfig{IdentityServiceURL: "http://identity:9100"}
		cfg.Postmark.ServerToken = "server-token"

		assert.NotNil(t, di.NewPasskeyRegistrationLinkSender(cfg, di.NewBootstrapLoggerTo(io.Discard)))
	})

	t.Run("only the identity service configuration reads an identity-provider key", func(t *testing.T) {
		t.Parallel()

		for _, typ := range []reflect.Type{reflect.TypeFor[config.ServerConfig](), reflect.TypeFor[config.ConsumerConfig](), reflect.TypeFor[config.WorkerConfig]()} {
			for _, name := range envNames(typ) {
				assert.False(t, strings.HasPrefix(name, "ZITADEL_MACHINE_KEY"), "%s declares %s", typ.Name(), name)
			}
		}
		assert.Contains(t, envNames(reflect.TypeFor[config.IdentityConfig]()), "ZITADEL_MACHINE_KEY_FOR_FAN_PASSKEY_RECOVERY_PATH")
	})
}
