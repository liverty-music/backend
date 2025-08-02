package attr

// Key name for slog.Attr.
const (
	Address = "address"
	Error   = "error"
	Method  = "method"
	Request = "request"
	SpanID  = "span_id"  // Following https://opentelemetry.io/docs/specs/semconv/general/naming/.
	TraceID = "trace_id" // Following https://opentelemetry.io/docs/specs/semconv/general/naming/.
)
