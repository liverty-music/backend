package di

import (
	"context"
	"log/slog"
	"slices"

	"github.com/liverty-music/backend/pkg/protoreg"
	"github.com/pannpers/go-apperr/apperr"
	"github.com/pannpers/go-apperr/apperr/codes"
	"github.com/pannpers/go-logging/logging"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

// acceptedProtoExtensionConflicts lists extension number collisions between
// third-party generated packages that every binary links and that are known to
// be harmless.
//
// The images run with GOLANG_PROTOBUF_REGISTRATION_CONFLICT=ignore (see the
// Dockerfile), so the protobuf runtime reports nothing on its own. These entries
// are logged at WARN; any collision not listed here is logged at ERROR so the
// workload's ERROR-log alert surfaces it.
var acceptedProtoExtensionConflicts = []protoreg.ExtensionConflict{
	{
		// Pocket Sign (pocketsign/shared/options/v1) and the Zitadel Go SDK
		// (zitadel/protoc/v2) both claim 50001 in the vendor range. Both are
		// server-side annotation options that neither SDK nor our code reflects
		// on at runtime.
		Message: "google.protobuf.MethodOptions",
		Number:  50001,
		Extensions: []protoreflect.FullName{
			"pocketsign.shared.options.v1.detail_error",
			"zitadel.protoc_gen_zitadel.v2.options",
		},
	},
}

// logProtoExtensionConflicts reports every extension number collision in the
// global protobuf registry through the structured logger.
func logProtoExtensionConflicts(ctx context.Context, logger *logging.Logger) {
	for _, c := range protoreg.FindExtensionConflicts(protoregistry.GlobalTypes.RangeExtensions) {
		attrs := []slog.Attr{
			slog.String("extended_message", string(c.Message)),
			slog.Int("extension_number", int(c.Number)),
			slog.Any("extensions", c.Extensions),
		}
		if slices.ContainsFunc(acceptedProtoExtensionConflicts, c.Equal) {
			logger.Warn(ctx, "accepted protobuf extension number conflict", attrs...)
			continue
		}
		logger.Error(ctx, "unexpected protobuf extension number conflict",
			apperr.New(codes.Internal, "protobuf extension number registered more than once"), attrs...)
	}
}
