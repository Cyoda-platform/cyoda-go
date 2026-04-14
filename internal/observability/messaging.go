package observability

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
)

// InjectTraceContext writes the active trace context from ctx into headers,
// using the configured global TextMapPropagator. The resulting headers can
// be transported across process boundaries (message bus, HTTP, etc.) and
// restored at the receiving end with ExtractTraceContext.
//
// The propagator writes lowercase W3C key names (traceparent, tracestate).
// Existing entries under those keys are overwritten.
//
// If headers is nil, the function is a no-op.
func InjectTraceContext(ctx context.Context, headers map[string]string) {
	if headers == nil {
		return
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(headers))
}

// ExtractTraceContext reads trace context from headers and returns a context
// derived from baseCtx with the remote span context attached. The returned
// context inherits baseCtx's cancellation tree (deadlines, parent cancels)
// — the remote span context is attached as propagation metadata, not as a
// new ctx root.
//
// Spans started from the returned context will be children of the remote
// span. Cancellable operations using the returned context still see
// baseCtx's cancellation.
//
// If headers is nil, returns baseCtx unchanged.
func ExtractTraceContext(baseCtx context.Context, headers map[string]string) context.Context {
	if headers == nil {
		return baseCtx
	}
	return otel.GetTextMapPropagator().Extract(baseCtx, propagation.MapCarrier(headers))
}
