// Package telemetry traces what heyrobots does with OpenTelemetry, in the
// GenAI semantic conventions, so any tool that reads OTLP (Jaeger, Grafana
// Tempo, Langfuse, Phoenix) can show it.
//
// ADK already emits spans for agent runs, model calls and tool calls. This
// package turns them on and adds heyrobots' own spans around them. Each
// task gets one trace: its trace id comes from the task's id, so the
// router's accept, every Assignment, every tool call and every send of that
// task land in the same trace, however far apart in time they happen.
//
// Spans carry names, ids, counts, timings and outcomes, never message
// bodies, tool arguments or secrets. ADK's own spans leave out message
// content unless OTEL_INSTRUMENTATION_GENAI_CAPTURE_MESSAGE_CONTENT is set.
//
// Export is configured with the standard variables, such as
// OTEL_EXPORTER_OTLP_ENDPOINT. With none set, nothing leaves the machine.
package telemetry

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"os"
	"strings"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
	adktelemetry "google.golang.org/adk/v2/telemetry"
)

// Name is the instrumentation scope of heyrobots' own spans.
const Name = "heyrobots"

// Attribute keys. The gen_ai ones follow the GenAI semantic conventions;
// the heyrobots ones are ours.
const (
	GenAIOperation      = attribute.Key("gen_ai.operation.name")
	GenAIAgentName      = attribute.Key("gen_ai.agent.name")
	GenAIConversationID = attribute.Key("gen_ai.conversation.id")
	GenAIToolName       = attribute.Key("gen_ai.tool.name")

	TaskID       = attribute.Key("heyrobots.task.id")
	AgentID      = attribute.Key("heyrobots.agent.id")
	AssignmentID = attribute.Key("heyrobots.assignment.id")
	ThreadID     = attribute.Key("heyrobots.thread.id")
	Role         = attribute.Key("heyrobots.requester.role")
	Strength     = attribute.Key("heyrobots.mandate.strength")
	Toolsets     = attribute.Key("heyrobots.mandate.toolsets")
	Rules        = attribute.Key("heyrobots.mandate.rules")
	Reach        = attribute.Key("heyrobots.reach")
	NeedApproval = attribute.Key("heyrobots.needs_approval")
	Findings     = attribute.Key("heyrobots.taint.findings")
	Outcome      = attribute.Key("heyrobots.outcome")
	Source       = attribute.Key("heyrobots.source")
	Action       = attribute.Key("heyrobots.action")
	EffectID     = attribute.Key("heyrobots.effect.id")
	MCPServer    = attribute.Key("heyrobots.mcp.server")
	Quarantined  = attribute.Key("heyrobots.quarantined")

	// Langfuse reads these on every span: the conversation as the session,
	// the requester as the user, and what kind of step a span is.
	SessionID       = attribute.Key("langfuse.session.id")
	UserID          = attribute.Key("langfuse.user.id")
	ObservationType = attribute.Key("langfuse.observation.type")
)

// Tracer returns heyrobots' tracer from the global provider. Until Setup
// installs one, its spans cost nothing and go nowhere.
func Tracer() trace.Tracer { return otel.Tracer(Name) }

// Setup installs the global tracer provider when an OTLP endpoint is
// configured. It returns a function that flushes and stops it.
func Setup(ctx context.Context, version string) (shutdown func(context.Context) error, exporting bool, err error) {
	if strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) == "" && strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT")) == "" {
		return func(context.Context) error { return nil }, false, nil
	}
	name := os.Getenv("OTEL_SERVICE_NAME")
	if name == "" {
		name = "heyrobotsd"
	}
	res, err := resource.New(ctx, resource.WithAttributes(semconv.ServiceName(name), semconv.ServiceVersion(version)))
	if err != nil {
		return nil, false, err
	}
	// The exporter reads the standard OTLP variables. heyrobots supplies the
	// ids, so a task's spans share one trace.
	exp, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, false, err
	}
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res), sdktrace.WithIDGenerator(IDs{}), sdktrace.WithSpanProcessor(shared{}))
	// ADK registers it globally, with its own spans' content setting.
	p, err := adktelemetry.New(ctx, adktelemetry.WithResource(res), adktelemetry.WithTracerProvider(tp))
	if err != nil {
		return nil, false, err
	}
	p.SetGlobalOtelProviders()
	return p.Shutdown, true, nil
}

// TaskContext returns ctx with the task's trace as its parent, so spans
// started from it join the task's trace.
func TaskContext(ctx context.Context, taskID string) context.Context {
	tid, sid := taskIDs(taskID)
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled, Remote: true})
	return trace.ContextWithRemoteSpanContext(ctx, sc)
}

// StartTask records that a task was accepted, as the root span of its
// trace. Every later span of the task is a child of it.
func StartTask(ctx context.Context, taskID string, attrs ...attribute.KeyValue) {
	opts := []trace.SpanStartOption{trace.WithNewRoot(), trace.WithAttributes(append(attrs, TaskID.String(taskID))...)}
	// The Eval that accepted the task has its own trace; link to it.
	if link := trace.LinkFromContext(ctx); link.SpanContext.IsValid() {
		opts = append(opts, trace.WithLinks(link))
	}
	ctx = context.WithValue(trace.ContextWithSpanContext(ctx, trace.SpanContext{}), rootKey{}, taskID)
	_, span := Tracer().Start(ctx, "heyrobots.task "+taskID, opts...)
	span.End()
}

// Fail marks a span failed.
func Fail(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
}

type rootKey struct{}

type sharedKey struct{}

// Share returns ctx with attributes every span started from it carries,
// ADK's included: Langfuse filters sessions and users per span, not per
// trace.
func Share(ctx context.Context, attrs ...attribute.KeyValue) context.Context {
	prev, _ := ctx.Value(sharedKey{}).([]attribute.KeyValue)
	return context.WithValue(ctx, sharedKey{}, append(append([]attribute.KeyValue{}, prev...), attrs...))
}

// shared copies the context's shared attributes onto each new span.
type shared struct{}

func (shared) OnStart(ctx context.Context, s sdktrace.ReadWriteSpan) {
	if attrs, ok := ctx.Value(sharedKey{}).([]attribute.KeyValue); ok {
		s.SetAttributes(attrs...)
	}
}
func (shared) OnEnd(sdktrace.ReadOnlySpan)      {}
func (shared) Shutdown(context.Context) error   { return nil }
func (shared) ForceFlush(context.Context) error { return nil }

// taskIDs derives a task's trace id and root span id from its id.
func taskIDs(taskID string) (trace.TraceID, trace.SpanID) {
	h := sha256.Sum256([]byte("heyrobots task " + taskID))
	var tid trace.TraceID
	var sid trace.SpanID
	copy(tid[:], h[:16])
	copy(sid[:], h[16:24])
	return tid, sid
}

// IDs gives a task's root span the ids derived from the task, and random
// ids to everything else.
type IDs struct{}

var _ sdktrace.IDGenerator = IDs{}

// NewIDs implements sdktrace.IDGenerator.
func (IDs) NewIDs(ctx context.Context) (trace.TraceID, trace.SpanID) {
	if taskID, ok := ctx.Value(rootKey{}).(string); ok {
		return taskIDs(taskID)
	}
	var tid trace.TraceID
	var sid trace.SpanID
	_, _ = rand.Read(tid[:])
	_, _ = rand.Read(sid[:])
	return tid, sid
}

// NewSpanID implements sdktrace.IDGenerator.
func (IDs) NewSpanID(context.Context, trace.TraceID) trace.SpanID {
	var sid trace.SpanID
	_, _ = rand.Read(sid[:])
	return sid
}
