package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kaitoyama/otel-viewer/internal/model"
	"github.com/kaitoyama/otel-viewer/internal/store"
)

func newStore(t *testing.T) *store.Store {
	t.Helper()
	// Each test gets its own DuckDB file. ":memory:" or empty DSN can be
	// shared across test goroutines depending on driver version, which would
	// leak state.
	dsn := t.TempDir() + "/store.duckdb"
	s, err := store.Open(context.Background(), dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func makeRun(id, model_, taskID string, stepCount int) *model.Run {
	now := time.Unix(1700_000_000, 0).UTC()
	r := &model.Run{
		ID:           id,
		Name:         "test",
		StartTime:    now,
		EndTime:      now.Add(time.Second),
		Model:        model_,
		Vendor:       "anthropic",
		TaskID:       taskID,
		TaskSource:   model.TaskSourceExplicit,
		Tags:         map[string]string{"env": "dev"},
		IngestSource: "otlp",
		Totals:       model.Usage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
	}
	for i := 0; i < stepCount; i++ {
		kind := model.StepKindPlannerLLM
		var tool *model.ToolInvocation
		if i%2 == 1 {
			kind = model.StepKindToolCall
			tool = &model.ToolInvocation{Name: "search"}
		}
		r.Steps = append(r.Steps, model.AgentStep{
			Index:     i,
			SpanID:    "span-" + id + "-" + itoa(i),
			Kind:      kind,
			StartTime: now.Add(time.Duration(i) * time.Millisecond),
			Duration:  10 * time.Millisecond,
			Model:     model_,
			Tool:      tool,
			Usage:     model.Usage{InputTokens: 50, OutputTokens: 25, TotalTokens: 75},
		})
	}
	return r
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

func TestStore_UpsertAndGet(t *testing.T) {
	s := newStore(t)
	r := makeRun("trace-1", "claude-opus-4-7", "task-1", 4)
	require.NoError(t, s.UpsertRun(context.Background(), r, nil))

	back, err := s.GetRun(context.Background(), "trace-1")
	require.NoError(t, err)
	assert.Equal(t, "trace-1", back.ID)
	assert.Equal(t, "claude-opus-4-7", back.Model)
	assert.Len(t, back.Steps, 4)
	assert.Equal(t, model.StepKindToolCall, back.Steps[1].Kind)
	assert.Equal(t, "search", back.Steps[1].Tool.Name)
}

func TestStore_ListAndFilterByModel(t *testing.T) {
	s := newStore(t)
	require.NoError(t, s.UpsertRun(context.Background(), makeRun("a", "opus", "t1", 2), nil))
	require.NoError(t, s.UpsertRun(context.Background(), makeRun("b", "haiku", "t1", 2), nil))
	require.NoError(t, s.UpsertRun(context.Background(), makeRun("c", "opus", "t2", 2), nil))

	runs, err := s.ListRuns(context.Background(), store.RunFilter{Models: []string{"opus"}})
	require.NoError(t, err)
	assert.Len(t, runs, 2)
}

func TestStore_Feedback(t *testing.T) {
	s := newStore(t)
	require.NoError(t, s.UpsertRun(context.Background(), makeRun("a", "opus", "t1", 2), nil))

	fb := &model.FeedbackEntry{TraceID: "a", Source: "user", Score: 0.1, Label: "bad"}
	require.NoError(t, s.InsertFeedback(context.Background(), fb))
	require.NotEmpty(t, fb.ID)

	score := 0.5
	list, err := s.ListFeedback(context.Background(), "", &score, 100)
	require.NoError(t, err)
	assert.Len(t, list, 1)

	// Filter trace list by feedback max score.
	max := 0.5
	runs, err := s.ListRuns(context.Background(), store.RunFilter{FeedbackMaxScore: &max})
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.True(t, runs[0].HasFeedback)
}

func TestStore_ManualTaskMembership(t *testing.T) {
	s := newStore(t)
	require.NoError(t, s.UpsertRun(context.Background(), makeRun("a", "opus", "", 1), nil))

	require.NoError(t, s.AddManualTaskMember(context.Background(), "manual-1", "a"))
	tasks, err := s.ListTasks(context.Background(), 10)
	require.NoError(t, err)
	found := false
	for _, t0 := range tasks {
		if t0.ID == "manual-1" {
			found = true
		}
	}
	assert.True(t, found, "manual task should appear in task list")

	// Unknown trace returns sentinel.
	err = s.AddManualTaskMember(context.Background(), "x", "no-such-trace")
	assert.ErrorIs(t, err, store.ErrUnknownTrace)
}

func TestStore_BackfillJobLifecycle(t *testing.T) {
	s := newStore(t)
	j := store.BackfillJob{BaseURL: "http://tempo", TraceQL: "{ }", StartNs: 1, EndNs: 2}
	require.NoError(t, s.CreateBackfillJob(context.Background(), &j))
	require.NotEmpty(t, j.ID)
	require.NoError(t, s.UpdateBackfill(context.Background(), j.ID, 5, 10, "running", ""))
	require.NoError(t, s.UpdateBackfill(context.Background(), j.ID, 10, 10, "done", ""))
	jobs, err := s.ListBackfillJobs(context.Background())
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	assert.Equal(t, "done", jobs[0].Status)
	assert.Equal(t, 10, jobs[0].Fetched)
}
