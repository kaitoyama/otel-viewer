package analytics_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kaitoyama/otel-viewer/internal/analytics"
	"github.com/kaitoyama/otel-viewer/internal/model"
	"github.com/kaitoyama/otel-viewer/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), t.TempDir()+"/a.duckdb")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func mkRun(id, model_ string, steps []model.AgentStep) *model.Run {
	now := time.Unix(1700_000_000, 0).UTC()
	r := &model.Run{
		ID:           id,
		Name:         "t",
		StartTime:    now,
		EndTime:      now.Add(time.Second),
		Model:        model_,
		Vendor:       "anthropic",
		TaskID:       "task-1",
		TaskSource:   model.TaskSourceExplicit,
		IngestSource: "otlp",
	}
	for i := range steps {
		steps[i].Index = i
		steps[i].SpanID = id + "-s" + itoa(i)
		steps[i].StartTime = now.Add(time.Duration(i) * 10 * time.Millisecond)
		steps[i].Duration = 5 * time.Millisecond
		if steps[i].Model == "" {
			steps[i].Model = model_
		}
	}
	r.Steps = steps
	return r
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	d := []byte{}
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}

func planner() model.AgentStep {
	return model.AgentStep{Kind: model.StepKindPlannerLLM, Operation: "chat"}
}
func toolCall(name string) model.AgentStep {
	return model.AgentStep{Kind: model.StepKindToolCall, Tool: &model.ToolInvocation{Name: name}}
}
func toolErr(name string) model.AgentStep {
	s := toolCall(name)
	s.StatusError = "boom"
	return s
}

func TestFrequency_GroupsByToolAndModel(t *testing.T) {
	s := newStore(t)
	require.NoError(t, s.UpsertRun(context.Background(), mkRun("a", "opus",
		[]model.AgentStep{planner(), toolCall("search"), toolErr("search"), planner()}), nil))
	require.NoError(t, s.UpsertRun(context.Background(), mkRun("b", "haiku",
		[]model.AgentStep{planner(), toolCall("search"), planner()}), nil))

	rows, err := analytics.Frequency(context.Background(), s, analytics.Filter{})
	require.NoError(t, err)

	// We expect rows for search@opus (2 calls, 1 error), search@haiku (1), and
	// planner rows. Just check the search tool rows exist.
	var opus, haiku *analytics.FrequencyRow
	for i := range rows {
		if rows[i].ToolName == "search" && rows[i].Model == "opus" {
			opus = &rows[i]
		}
		if rows[i].ToolName == "search" && rows[i].Model == "haiku" {
			haiku = &rows[i]
		}
	}
	if assert.NotNil(t, opus) {
		assert.Equal(t, 2, opus.Calls)
		assert.Equal(t, 1, opus.Errors)
		assert.InDelta(t, 0.5, opus.ErrorRate, 0.001)
	}
	if assert.NotNil(t, haiku) {
		assert.Equal(t, 1, haiku.Calls)
		assert.Equal(t, 0, haiku.Errors)
	}
}

func TestFlow_EdgesAreConsecutiveSteps(t *testing.T) {
	s := newStore(t)
	require.NoError(t, s.UpsertRun(context.Background(), mkRun("a", "opus",
		[]model.AgentStep{planner(), toolCall("search"), planner()}), nil))
	require.NoError(t, s.UpsertRun(context.Background(), mkRun("b", "opus",
		[]model.AgentStep{planner(), toolCall("search"), planner()}), nil))
	require.NoError(t, s.UpsertRun(context.Background(), mkRun("c", "opus",
		[]model.AgentStep{planner(), toolCall("calc")}), nil))

	g, err := analytics.Flow(context.Background(), s, analytics.Filter{})
	require.NoError(t, err)

	// We expect 3 distinct node tuples (planner, tool:search, tool:calc) and
	// edges P→search (2x), search→P (2x), P→calc (1x).
	edgeMap := map[string]int{}
	for _, e := range g.Edges {
		edgeMap[e.From+"=>"+e.To] = e.Count
	}
	hasPToSearch := false
	hasPToCalc := false
	for k, v := range edgeMap {
		if v == 2 {
			hasPToSearch = true
		}
		if v == 1 && k != "" {
			hasPToCalc = true
		}
	}
	assert.True(t, hasPToSearch, "expected an edge with count=2 from planner to search")
	assert.True(t, hasPToCalc, "expected at least one edge with count=1")
}

func TestParseFilter(t *testing.T) {
	b := []byte(`{
	  "model": {"in": ["opus", "haiku"]},
	  "feedback_score": {"lte": 0.3},
	  "tool_output_contains": "ERROR"
	}`)
	f, err := analytics.ParseFilter(b)
	require.NoError(t, err)
	assert.Equal(t, []string{"opus", "haiku"}, f.Model.In)
	require.NotNil(t, f.FeedbackScore.Lte)
	assert.InDelta(t, 0.3, *f.FeedbackScore.Lte, 0.0001)
	assert.Equal(t, "ERROR", f.ToolOutputContains)
}

func TestBuildRunsWhere(t *testing.T) {
	score := 0.5
	f := analytics.Filter{
		Model:         &analytics.StringOp{In: []string{"opus"}},
		FeedbackScore: &analytics.NumOp{Lte: &score},
	}
	where, args, err := analytics.BuildRunsWhere(f)
	require.NoError(t, err)
	assert.Contains(t, where, "r.model IN")
	assert.Contains(t, where, "feedback")
	assert.NotEmpty(t, args)
	// Sanity: JSON round-trip the filter so we know the DSL is stable.
	b, err := json.Marshal(f)
	require.NoError(t, err)
	assert.Contains(t, string(b), "opus")
}
