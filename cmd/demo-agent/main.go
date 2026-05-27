// Command demo-agent emits a curated set of synthetic OTel traces to the
// otel-viewer's OTLP receiver. The fixture demonstrates:
//
//   - The same task processed by two models (claude-opus-4-7 and
//     claude-haiku-4-5) so the Compare view has something to align.
//   - A shadow run (replay_group_id) tied to a primary run so the
//     replay-group view has data.
//   - A run with gen_ai.feedback events at different scores so the feedback
//     triage and analytics filters have signal.
//   - A run that exercises tool calls and a status error so the frequency
//     table has non-trivial error rates.
//
// No actual LLM API is called — all token counts, messages and outputs are
// fabricated. The point is to populate the UI with realistic shapes.
package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func main() {
	endpoint := flag.String("endpoint", "http://localhost:8080/v1/traces", "OTLP/HTTP traces endpoint")
	token := flag.String("token", os.Getenv("OTEL_VIEWER_TOKEN"), "Bearer token (optional)")
	flag.Parse()

	traces := []*tracepb.TracesData{
		summarizeTask("claude-opus-4-7", "task-summarize", nil, nil, false),
		summarizeTask("claude-haiku-4-5", "task-summarize", nil, nil, false),
		summarizeTask("claude-haiku-4-5", "task-summarize", strPtr("rg-summarize-1"), strPtr("primary-trace-id-placeholder"), true),
		researchTask("claude-opus-4-7", "task-research", false),
		researchTask("claude-haiku-4-5", "task-research", true),  // haiku fails the search tool
		summarizeTask("claude-opus-4-7", "task-summarize", strPtr("rg-summarize-1"), nil, false),
	}
	for i, td := range traces {
		if err := send(*endpoint, *token, td); err != nil {
			fmt.Fprintf(os.Stderr, "send #%d: %v\n", i, err)
			os.Exit(1)
		}
		fmt.Printf("sent trace %d\n", i+1)
	}
	fmt.Println("done")
}

func strPtr(s string) *string { return &s }

// summarizeTask emits a 2-step trace: planner LLM + final answer (no tool).
// When primaryTraceID is non-nil and isShadow=true the run is tagged as a
// shadow execution of that primary trace.
func summarizeTask(model, taskID string, replayGroup, _ *string, isShadow bool) *tracepb.TracesData {
	traceID := randomID(16)
	spanID := randomID(8)
	start := time.Now().Add(-3 * time.Minute)
	end := start.Add(800 * time.Millisecond)
	attrs := []*commonpb.KeyValue{
		kv("gen_ai.system", sv("anthropic")),
		kv("gen_ai.request.model", sv(model)),
		kv("gen_ai.response.model", sv(model)),
		kv("gen_ai.operation.name", sv("chat")),
		kv("gen_ai.usage.input_tokens", iv(120)),
		kv("gen_ai.usage.output_tokens", iv(60)),
		kv("gen_ai.task.id", sv(taskID)),
		kv("gen_ai.task.name", sv("summarize-email")),
	}
	if replayGroup != nil {
		attrs = append(attrs, kv("gen_ai.replay.group", sv(*replayGroup)))
	}
	if isShadow {
		// Shadow runs reference the primary trace id; here we use a fixed
		// sentinel since the user manually wires this up in real workflows.
		attrs = append(attrs, kv("gen_ai.replay.of", sv("primary-trace-id-placeholder")))
	}
	events := []*tracepb.Span_Event{
		{
			Name:         "gen_ai.user.message",
			TimeUnixNano: uint64(start.Add(50 * time.Millisecond).UnixNano()),
			Attributes:   []*commonpb.KeyValue{kv("content", sv("Summarize the email below in one sentence: ..."))},
		},
		{
			Name:         "gen_ai.choice",
			TimeUnixNano: uint64(start.Add(700 * time.Millisecond).UnixNano()),
			Attributes:   []*commonpb.KeyValue{kv("content", sv("The customer is asking for a refund."))},
		},
	}
	// Add feedback on the opus run only, so we have score variation.
	if model == "claude-opus-4-7" && replayGroup == nil {
		events = append(events, &tracepb.Span_Event{
			Name:         "gen_ai.feedback",
			TimeUnixNano: uint64(end.UnixNano()),
			Attributes: []*commonpb.KeyValue{
				kv("gen_ai.feedback.source", sv("user")),
				kv("gen_ai.feedback.score", fv(0.85)),
				kv("gen_ai.feedback.label", sv("good")),
			},
		})
	}
	if model == "claude-haiku-4-5" && replayGroup == nil {
		// Haiku produced a worse summary in this fictional run.
		events = append(events, &tracepb.Span_Event{
			Name:         "gen_ai.feedback",
			TimeUnixNano: uint64(end.UnixNano()),
			Attributes: []*commonpb.KeyValue{
				kv("gen_ai.feedback.source", sv("user")),
				kv("gen_ai.feedback.score", fv(0.25)),
				kv("gen_ai.feedback.label", sv("bad")),
				kv("gen_ai.feedback.comment", sv("Missed the refund context")),
			},
		})
	}
	return wrap(traceID, spanID, model, "chat "+model, start, end, attrs, events, nil)
}

// researchTask emits a 4-step trace: planner -> tool search -> planner -> final.
// If failTool is true the search tool reports an error.
func researchTask(model, taskID string, failTool bool) *tracepb.TracesData {
	traceID := randomID(16)
	rootSpanID := randomID(8)
	toolSpanID := randomID(8)
	start := time.Now().Add(-2 * time.Minute)
	end := start.Add(1500 * time.Millisecond)

	rootAttrs := []*commonpb.KeyValue{
		kv("gen_ai.system", sv("anthropic")),
		kv("gen_ai.request.model", sv(model)),
		kv("gen_ai.response.model", sv(model)),
		kv("gen_ai.operation.name", sv("chat")),
		kv("gen_ai.usage.input_tokens", iv(220)),
		kv("gen_ai.usage.output_tokens", iv(110)),
		kv("gen_ai.task.id", sv(taskID)),
		kv("gen_ai.task.name", sv("research-question")),
		kv("gen_ai.agent.name", sv("researcher")),
	}
	rootEvents := []*tracepb.Span_Event{
		{
			Name:         "gen_ai.user.message",
			TimeUnixNano: uint64(start.Add(20 * time.Millisecond).UnixNano()),
			Attributes:   []*commonpb.KeyValue{kv("content", sv("What's the recommended retention for OTel data?"))},
		},
		{
			Name:         "gen_ai.choice",
			TimeUnixNano: uint64(end.UnixNano()),
			Attributes:   []*commonpb.KeyValue{kv("content", sv("Typical retention is 7–30 days for traces."))},
		},
	}

	toolStart := start.Add(300 * time.Millisecond)
	toolEnd := toolStart.Add(400 * time.Millisecond)
	toolAttrs := []*commonpb.KeyValue{
		kv("gen_ai.tool.name", sv("search_web")),
		kv("gen_ai.tool.call.arguments", sv(`{"q":"otel retention default"}`)),
	}
	var toolStatus *tracepb.Status
	if failTool {
		toolAttrs = append(toolAttrs, kv("gen_ai.tool.call.error", sv("rate limited")))
		toolStatus = &tracepb.Status{Code: tracepb.Status_STATUS_CODE_ERROR, Message: "rate limited by upstream"}
	} else {
		toolAttrs = append(toolAttrs, kv("gen_ai.tool.call.result", sv(`{"hits":3}`)))
	}

	toolSpan := &tracepb.Span{
		TraceId:           traceID,
		SpanId:            toolSpanID,
		ParentSpanId:      rootSpanID,
		Name:              "execute_tool search_web",
		Kind:              tracepb.Span_SPAN_KIND_CLIENT,
		StartTimeUnixNano: uint64(toolStart.UnixNano()),
		EndTimeUnixNano:   uint64(toolEnd.UnixNano()),
		Attributes:        toolAttrs,
		Status:            toolStatus,
	}
	return wrap(traceID, rootSpanID, model, "chat "+model, start, end, rootAttrs, rootEvents, []*tracepb.Span{toolSpan})
}

// wrap packages a root span (plus optional child spans) into a TracesData.
func wrap(traceID, spanID []byte, model, name string, start, end time.Time,
	attrs []*commonpb.KeyValue, events []*tracepb.Span_Event, children []*tracepb.Span,
) *tracepb.TracesData {
	spans := []*tracepb.Span{{
		TraceId:           traceID,
		SpanId:            spanID,
		Name:              name,
		Kind:              tracepb.Span_SPAN_KIND_CLIENT,
		StartTimeUnixNano: uint64(start.UnixNano()),
		EndTimeUnixNano:   uint64(end.UnixNano()),
		Attributes:        attrs,
		Events:            events,
	}}
	spans = append(spans, children...)
	_ = model
	return &tracepb.TracesData{
		ResourceSpans: []*tracepb.ResourceSpans{{
			Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
				kv("service.name", sv("demo-agent")),
				kv("service.version", sv("0.1.0")),
			}},
			ScopeSpans: []*tracepb.ScopeSpans{{
				Scope: &commonpb.InstrumentationScope{Name: "demo-agent"},
				Spans: spans,
			}},
		}},
	}
}

func send(endpoint, token string, td *tracepb.TracesData) error {
	body, err := protojson.Marshal(td)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%d: %s", resp.StatusCode, string(b))
	}
	return nil
}

func randomID(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func sv(s string) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: s}}
}
func iv(n int64) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: n}}
}
func fv(f float64) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: f}}
}
func kv(k string, v *commonpb.AnyValue) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: v}
}
