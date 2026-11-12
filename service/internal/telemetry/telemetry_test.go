package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestSharedAttributesReachEverySpan(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(shared{}), sdktrace.WithSpanProcessor(rec))
	ctx := Share(context.Background(), SessionID.String("th_1"))
	ctx = Share(ctx, UserID.String("app:owner"))
	ctx, parent := tp.Tracer("t").Start(ctx, "parent")
	_, child := tp.Tracer("other").Start(ctx, "child")
	child.End()
	parent.End()
	_, plain := tp.Tracer("t").Start(context.Background(), "plain")
	plain.End()
	for _, s := range rec.Ended() {
		got := attribute.NewSet(s.Attributes()...)
		_, hasSession := got.Value(SessionID)
		_, hasUser := got.Value(UserID)
		if want := s.Name() != "plain"; hasSession != want || hasUser != want {
			t.Errorf("%s: session %v user %v", s.Name(), hasSession, hasUser)
		}
	}
}
