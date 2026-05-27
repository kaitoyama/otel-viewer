package normalize_test

import (
	"crypto/rand"
	"testing"

	"github.com/stretchr/testify/assert"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/kaitoyama/otel-viewer/internal/model"
	"github.com/kaitoyama/otel-viewer/internal/normalize"
)

func sv(s string) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: s}}
}
func iv(n int64) *commonpb.AnyValue {
	return &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: n}}
}

func kv(k string, v *commonpb.AnyValue) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: k, Value: v}
}

func randID(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func TestNormalize_GenAI_LLMAndTool(t *testing.T) {
	traceID := randID(16)
	llmSpanID := randID(8)
	toolSpanID := randID(8)

	td := &tracepb.TracesData{
		ResourceSpans: []*tracepb.ResourceSpans{{
			Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
				kv("service.name", sv("agent-app")),
			}},
			ScopeSpans: []*tracepb.ScopeSpans{{
				Scope: &commonpb.InstrumentationScope{Name: "openai"},
				Spans: []*tracepb.Span{
					{
						TraceId:           traceID,
						SpanId:            llmSpanID,
						Name:              "chat claude-opus-4-7",
						StartTimeUnixNano: 1_000_000_000,
						EndTimeUnixNano:   2_000_000_000,
						Attributes: []*commonpb.KeyValue{
							kv("gen_ai.system", sv("anthropic")),
							kv("gen_ai.request.model", sv("claude-opus-4-7")),
							kv("gen_ai.response.model", sv("claude-opus-4-7")),
							kv("gen_ai.operation.name", sv("chat")),
							kv("gen_ai.usage.input_tokens", iv(120)),
							kv("gen_ai.usage.output_tokens", iv(40)),
							kv("gen_ai.task.id", sv("triage-001")),
							kv("gen_ai.conversation.id", sv("conv-1")),
						},
						Events: []*tracepb.Span_Event{
							{
								Name:         "gen_ai.user.message",
								TimeUnixNano: 1_100_000_000,
								Attributes: []*commonpb.KeyValue{
									kv("content", sv("Please summarize this email")),
								},
							},
							{
								Name:         "gen_ai.choice",
								TimeUnixNano: 1_900_000_000,
								Attributes: []*commonpb.KeyValue{
									kv("content", sv("Here is the summary…")),
								},
							},
							{
								Name:         "gen_ai.feedback",
								TimeUnixNano: 1_950_000_000,
								Attributes: []*commonpb.KeyValue{
									kv("gen_ai.feedback.source", sv("user")),
									kv("gen_ai.feedback.score", iv(1)),
									kv("gen_ai.feedback.label", sv("thumbs_up")),
								},
							},
						},
					},
					{
						TraceId:           traceID,
						SpanId:            toolSpanID,
						ParentSpanId:      llmSpanID,
						Name:              "execute_tool search_web",
						StartTimeUnixNano: 2_100_000_000,
						EndTimeUnixNano:   2_300_000_000,
						Attributes: []*commonpb.KeyValue{
							kv("gen_ai.tool.name", sv("search_web")),
							kv("gen_ai.tool.call.arguments", sv(`{"q":"otel"}`)),
							kv("gen_ai.tool.call.result", sv(`{"hits":3}`)),
						},
					},
				},
			}},
		}},
	}

	res := normalize.Normalize(td.ResourceSpans, "otlp")
	if !assert.NotNil(t, res) {
		return
	}
	r := res.Run
	assert.Equal(t, "claude-opus-4-7", r.Model)
	assert.Equal(t, "anthropic", r.Vendor)
	assert.Len(t, r.Steps, 2)

	// LLM step has the usage we set; tool step is classified as ToolCall.
	var planner, tool *model.AgentStep
	for i := range r.Steps {
		switch r.Steps[i].Kind {
		case model.StepKindPlannerLLM:
			planner = &r.Steps[i]
		case model.StepKindToolCall:
			tool = &r.Steps[i]
		}
	}
	if assert.NotNil(t, planner) {
		assert.Equal(t, int64(120), planner.Usage.InputTokens)
		assert.Equal(t, int64(40), planner.Usage.OutputTokens)
		assert.Equal(t, int64(160), planner.Usage.TotalTokens)
		assert.NotEmpty(t, planner.Messages)
		assert.Contains(t, planner.Output, "summary")
	}
	if assert.NotNil(t, tool) {
		assert.Equal(t, "search_web", tool.Tool.Name)
		assert.Contains(t, tool.Tool.Arguments, "otel")
	}

	// Feedback event extracted.
	if assert.Len(t, res.FeedbackEvents, 1) {
		fb := res.FeedbackEvents[0]
		assert.Equal(t, "user", fb.Source)
		assert.Equal(t, "thumbs_up", fb.Label)
	}

	// Run-level totals roll up.
	assert.Equal(t, int64(160), r.Totals.TotalTokens)
}

func TestNormalize_OpenInference(t *testing.T) {
	traceID := randID(16)
	span := randID(8)
	td := &tracepb.TracesData{
		ResourceSpans: []*tracepb.ResourceSpans{{
			ScopeSpans: []*tracepb.ScopeSpans{{
				Spans: []*tracepb.Span{
					{
						TraceId:           traceID,
						SpanId:            span,
						Name:              "ChatOpenAI",
						StartTimeUnixNano: 1_000_000_000,
						EndTimeUnixNano:   2_000_000_000,
						Attributes: []*commonpb.KeyValue{
							kv("openinference.span.kind", sv("LLM")),
							kv("llm.model_name", sv("gpt-4")),
							kv("llm.token_count.prompt", iv(50)),
							kv("llm.token_count.completion", iv(20)),
							kv("input.value", sv(`[{"role":"user","content":"hi"}]`)),
							kv("output.value", sv("hello")),
						},
					},
				},
			}},
		}},
	}
	res := normalize.Normalize(td.ResourceSpans, "jsonl")
	if !assert.NotNil(t, res) || !assert.Len(t, res.Run.Steps, 1) {
		return
	}
	s := res.Run.Steps[0]
	assert.Equal(t, model.StepKindPlannerLLM, s.Kind)
	assert.Equal(t, "gpt-4", s.Model)
	assert.Equal(t, int64(50), s.Usage.InputTokens)
	if assert.Len(t, s.Messages, 1) {
		assert.Equal(t, "user", s.Messages[0].Role)
	}
	assert.Equal(t, "hello", s.Output)
}

func TestNormalize_ReplayLink(t *testing.T) {
	traceID := randID(16)
	span := randID(8)
	td := &tracepb.TracesData{
		ResourceSpans: []*tracepb.ResourceSpans{{
			ScopeSpans: []*tracepb.ScopeSpans{{
				Spans: []*tracepb.Span{
					{
						TraceId:           traceID,
						SpanId:            span,
						Name:              "shadow run",
						StartTimeUnixNano: 1,
						EndTimeUnixNano:   2,
						Attributes: []*commonpb.KeyValue{
							kv("gen_ai.replay.of", sv("aaaabbbbccccdddd")),
							kv("gen_ai.replay.group", sv("rg-1")),
							kv("gen_ai.request.model", sv("claude-haiku-4-5")),
						},
					},
				},
			}},
		}},
	}
	res := normalize.Normalize(td.ResourceSpans, "otlp")
	if !assert.NotNil(t, res) {
		return
	}
	if assert.NotNil(t, res.Run.ReplayOf) {
		assert.Equal(t, "aaaabbbbccccdddd", *res.Run.ReplayOf)
	}
	if assert.NotNil(t, res.Run.ReplayGroup) {
		assert.Equal(t, "rg-1", *res.Run.ReplayGroup)
	}
}
