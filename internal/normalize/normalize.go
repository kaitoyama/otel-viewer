// Package normalize converts OTLP traces (proto representation) into the
// internal Run model. It understands two semantic conventions:
//
//   - OpenTelemetry GenAI semantic conventions (gen_ai.*)
//   - OpenInference (openinference.*, llm.*, tool.*, input.value, output.value)
//
// Spans that don't match either are kept as Other steps so they still show up
// in timelines, but most analytical features are driven by the typed steps.
package normalize

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"

	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/kaitoyama/otel-viewer/internal/model"
	"github.com/kaitoyama/otel-viewer/internal/store"
)

// Result is the output of Normalize.
type Result struct {
	Run    model.Run
	Events []store.EventRecord
	// FeedbackEvents are gen_ai.feedback events extracted from spans, ready to
	// be inserted via store.InsertFeedback. They carry their final TraceID.
	FeedbackEvents []model.FeedbackEntry
}

// Normalize converts a single trace's ResourceSpans set into a Run.
// Multiple ResourceSpans for the same trace should be passed together.
func Normalize(rss []*tracepb.ResourceSpans, ingestSource string) *Result {
	if len(rss) == 0 {
		return nil
	}
	allSpans := collectSpans(rss)
	if len(allSpans) == 0 {
		return nil
	}
	sort.Slice(allSpans, func(i, j int) bool {
		return allSpans[i].span.StartTimeUnixNano < allSpans[j].span.StartTimeUnixNano
	})

	traceID := hex.EncodeToString(allSpans[0].span.TraceId)
	run := model.Run{
		ID:           traceID,
		Tags:         map[string]string{},
		IngestSource: ingestSource,
	}

	var (
		earliestStart uint64 = ^uint64(0)
		latestEnd     uint64
		events        []store.EventRecord
		feedback      []model.FeedbackEntry
	)

	for i, sp := range allSpans {
		if sp.span.StartTimeUnixNano < earliestStart {
			earliestStart = sp.span.StartTimeUnixNano
		}
		if sp.span.EndTimeUnixNano > latestEnd {
			latestEnd = sp.span.EndTimeUnixNano
		}

		stepAttrs := attrMapFromKVList(sp.span.Attributes)
		stepAttrs.flattenInto(run.Tags)

		step := buildStep(i, sp.span, stepAttrs)
		// Run-level rollups: prefer outermost span values but treat per-step
		// fills as fallback if unset at run level.
		if run.Model == "" {
			run.Model = stepAttrs.FirstString("gen_ai.response.model", "gen_ai.request.model", "llm.model_name")
		}
		if run.Vendor == "" {
			run.Vendor = stepAttrs.FirstString("gen_ai.system", "llm.provider")
		}
		if conv := stepAttrs.FirstString("gen_ai.conversation.id"); conv != "" {
			run.Tags["gen_ai.conversation.id"] = conv
		}
		if tid := stepAttrs.FirstString("gen_ai.task.id"); tid != "" {
			run.Tags["gen_ai.task.id"] = tid
		}
		if tn := stepAttrs.FirstString("gen_ai.task.name"); tn != "" {
			run.Tags["gen_ai.task.name"] = tn
		}
		// Replay attributes (no stable OTel spec; we honor a small set).
		if v := stepAttrs.FirstString("gen_ai.replay.of", "replay_of"); v != "" && run.ReplayOf == nil {
			vv := v
			run.ReplayOf = &vv
		}
		if v := stepAttrs.FirstString("gen_ai.replay.group", "replay_group_id"); v != "" && run.ReplayGroup == nil {
			vv := v
			run.ReplayGroup = &vv
		}

		// Span events: messages, feedback, evaluation results.
		for _, ev := range sp.span.Events {
			ea := attrMapFromKVList(ev.Attributes)
			switch ev.Name {
			case "gen_ai.user.message", "gen_ai.system.message",
				"gen_ai.assistant.message", "gen_ai.tool.message":
				role := strings.TrimPrefix(strings.TrimSuffix(ev.Name, ".message"), "gen_ai.")
				step.Messages = append(step.Messages, model.Message{
					Role:       role,
					Content:    ea.FirstString("content", "gen_ai.content"),
					Name:       ea.String("name"),
					ToolCallID: ea.String("id"),
				})
			case "gen_ai.choice":
				step.Output = ea.FirstString("content", "message.content")
				if step.Output == "" {
					b, _ := json.Marshal(ea)
					step.Output = string(b)
				}
			case "gen_ai.feedback":
				score := ea.Float64("gen_ai.feedback.score")
				if score == 0 {
					score = ea.Float64("score")
				}
				fb := model.FeedbackEntry{
					TraceID: traceID,
					SpanID:  hex.EncodeToString(sp.span.SpanId),
					Source:  ea.FirstString("gen_ai.feedback.source", "source", "user"),
					Score:   score,
					Label:   ea.FirstString("gen_ai.feedback.label", "label"),
					Comment: ea.FirstString("gen_ai.feedback.comment", "comment"),
				}
				if fb.Source == "" {
					fb.Source = "evaluator"
				}
				if t := ev.TimeUnixNano; t > 0 {
					fb.CreatedAt = time.Unix(0, int64(t))
				}
				feedback = append(feedback, fb)
			case "gen_ai.evaluation.result":
				step.Evals = append(step.Evals, model.EvalResult{
					Name:    ea.FirstString("gen_ai.evaluation.name", "name"),
					Score:   ea.Float64("gen_ai.evaluation.score"),
					Label:   ea.FirstString("gen_ai.evaluation.label", "label"),
					Comment: ea.FirstString("gen_ai.evaluation.explanation", "comment"),
				})
			}
			pl := map[string]any{}
			for k, v := range ea {
				pl[k] = v
			}
			events = append(events, store.EventRecord{
				SpanID:     hex.EncodeToString(sp.span.SpanId),
				TimeNs:     int64(ev.TimeUnixNano),
				Name:       ev.Name,
				Attributes: pl,
			})
		}

		if sp.span.Status != nil && sp.span.Status.Code == tracepb.Status_STATUS_CODE_ERROR {
			step.StatusError = sp.span.Status.Message
		}

		// Roll usage up.
		run.Totals.InputTokens += step.Usage.InputTokens
		run.Totals.OutputTokens += step.Usage.OutputTokens
		run.Totals.TotalTokens += step.Usage.TotalTokens

		// EvalResults at run level: copy any seen in any step.
		run.EvalResults = append(run.EvalResults, step.Evals...)

		run.Steps = append(run.Steps, step)
	}

	run.Name = chooseRunName(run.Steps)
	if earliestStart != ^uint64(0) {
		run.StartTime = time.Unix(0, int64(earliestStart))
	}
	run.EndTime = time.Unix(0, int64(latestEnd))

	// Compute a stable prompt hash from the first system+user messages we see.
	run.PromptHash = promptHashFromSteps(run.Steps)

	return &Result{Run: run, Events: events, FeedbackEvents: feedback}
}

type spanWithResource struct {
	span     *tracepb.Span
	resource AttrMap
}

func collectSpans(rss []*tracepb.ResourceSpans) []spanWithResource {
	var out []spanWithResource
	for _, rs := range rss {
		var rAttrs AttrMap
		if rs.Resource != nil {
			rAttrs = attrMapFromKVList(rs.Resource.Attributes)
		}
		for _, ss := range rs.ScopeSpans {
			for _, sp := range ss.Spans {
				out = append(out, spanWithResource{span: sp, resource: rAttrs})
			}
		}
	}
	return out
}

func buildStep(idx int, sp *tracepb.Span, attrs AttrMap) model.AgentStep {
	st := model.AgentStep{
		Index:        idx,
		SpanID:       hex.EncodeToString(sp.SpanId),
		ParentSpanID: hex.EncodeToString(sp.ParentSpanId),
		StartTime:    time.Unix(0, int64(sp.StartTimeUnixNano)),
		Duration:     time.Duration(sp.EndTimeUnixNano-sp.StartTimeUnixNano) * time.Nanosecond,
		Model:        attrs.FirstString("gen_ai.response.model", "gen_ai.request.model", "llm.model_name"),
		Operation:    attrs.FirstString("gen_ai.operation.name"),
		AgentName:    attrs.FirstString("gen_ai.agent.name"),
		Usage: model.Usage{
			InputTokens:  attrs.FirstInt64("gen_ai.usage.input_tokens", "llm.token_count.prompt"),
			OutputTokens: attrs.FirstInt64("gen_ai.usage.output_tokens", "llm.token_count.completion"),
			TotalTokens:  attrs.FirstInt64("gen_ai.usage.total_tokens", "llm.token_count.total"),
		},
	}
	if st.Usage.TotalTokens == 0 {
		st.Usage.TotalTokens = st.Usage.InputTokens + st.Usage.OutputTokens
	}

	toolName := attrs.FirstString("gen_ai.tool.name", "tool.name")
	switch {
	case attrs.FirstString("openinference.span.kind") == "TOOL" || toolName != "" && strings.Contains(sp.Name, "tool"):
		st.Kind = model.StepKindToolCall
		st.Tool = &model.ToolInvocation{
			Name:      toolName,
			Arguments: attrs.FirstString("gen_ai.tool.call.arguments", "tool.input", "input.value"),
			Result:    attrs.FirstString("gen_ai.tool.call.result", "tool.output", "output.value"),
		}
	case attrs.FirstString("openinference.span.kind") == "AGENT" ||
		strings.HasPrefix(sp.Name, "invoke_agent") || st.AgentName != "":
		st.Kind = model.StepKindAgentInvoke
	case attrs.FirstString("openinference.span.kind") == "LLM" ||
		strings.HasPrefix(sp.Name, "chat") || strings.HasPrefix(sp.Name, "completion") ||
		st.Operation == "chat" || st.Operation == "text_completion" || st.Operation == "generate_content":
		st.Kind = model.StepKindPlannerLLM
		// OpenInference often stuffs the prompt as input.value, output as output.value.
		if input := attrs.String("input.value"); input != "" && len(st.Messages) == 0 {
			st.Messages = parseInputValue(input)
		}
		if out := attrs.String("output.value"); out != "" && st.Output == "" {
			st.Output = out
		}
	default:
		st.Kind = model.StepKindOther
	}
	return st
}

// parseInputValue tolerates the two common shapes OpenInference instruments
// emit: a JSON array of {role, content} or a raw text prompt.
func parseInputValue(s string) []model.Message {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if s[0] == '[' {
		var msgs []model.Message
		if err := json.Unmarshal([]byte(s), &msgs); err == nil {
			return msgs
		}
	}
	if s[0] == '{' {
		var obj map[string]any
		if err := json.Unmarshal([]byte(s), &obj); err == nil {
			if msgs, ok := obj["messages"].([]any); ok {
				out := make([]model.Message, 0, len(msgs))
				for _, m := range msgs {
					if mm, ok := m.(map[string]any); ok {
						out = append(out, model.Message{
							Role:    asString(mm["role"]),
							Content: asString(mm["content"]),
						})
					}
				}
				return out
			}
		}
	}
	return []model.Message{{Role: "user", Content: s}}
}

func asString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func chooseRunName(steps []model.AgentStep) string {
	// Prefer the outermost agent invoke; fall back to first planner LLM.
	for _, s := range steps {
		if s.Kind == model.StepKindAgentInvoke && s.AgentName != "" {
			return "agent:" + s.AgentName
		}
	}
	for _, s := range steps {
		if s.Kind == model.StepKindPlannerLLM {
			if s.Model != "" {
				return "llm:" + s.Model
			}
			return "llm"
		}
	}
	if len(steps) > 0 {
		return string(steps[0].Kind)
	}
	return "trace"
}

func promptHashFromSteps(steps []model.AgentStep) string {
	for _, s := range steps {
		if s.Kind != model.StepKindPlannerLLM {
			continue
		}
		var parts []string
		for _, m := range s.Messages {
			if m.Role == "system" || m.Role == "user" {
				parts = append(parts, m.Role+":"+m.Content)
			}
		}
		if len(parts) == 0 {
			continue
		}
		h := sha256.Sum256([]byte(strings.Join(parts, "\n---\n")))
		return hex.EncodeToString(h[:])
	}
	return ""
}
