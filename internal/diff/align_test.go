package diff_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kaitoyama/otel-viewer/internal/diff"
	"github.com/kaitoyama/otel-viewer/internal/model"
)

func step(kind model.StepKind, tool, agent string) model.AgentStep {
	st := model.AgentStep{Kind: kind, AgentName: agent}
	if tool != "" {
		st.Tool = &model.ToolInvocation{Name: tool}
	}
	return st
}

func TestSignature(t *testing.T) {
	assert.Equal(t, "P", diff.Signature(step(model.StepKindPlannerLLM, "", "")))
	assert.Equal(t, "T:search", diff.Signature(step(model.StepKindToolCall, "search", "")))
	assert.Equal(t, "A:planner", diff.Signature(step(model.StepKindAgentInvoke, "", "planner")))
}

func TestAlign_Identical(t *testing.T) {
	left := []model.AgentStep{
		step(model.StepKindPlannerLLM, "", ""),
		step(model.StepKindToolCall, "search", ""),
		step(model.StepKindPlannerLLM, "", ""),
	}
	right := left
	pairs := diff.Align(left, right)
	if assert.Len(t, pairs, 3) {
		for i, p := range pairs {
			assert.Equal(t, i, p.Left)
			assert.Equal(t, i, p.Right)
		}
	}
}

func TestAlign_RetryInsertedOnRight(t *testing.T) {
	left := []model.AgentStep{
		step(model.StepKindPlannerLLM, "", ""),
		step(model.StepKindToolCall, "search", ""),
		step(model.StepKindPlannerLLM, "", ""),
	}
	right := []model.AgentStep{
		step(model.StepKindPlannerLLM, "", ""),
		step(model.StepKindToolCall, "search", ""),
		step(model.StepKindToolCall, "search", ""), // retry inserted
		step(model.StepKindPlannerLLM, "", ""),
	}
	pairs := diff.Align(left, right)
	// We expect one gap on the left side for the inserted retry.
	gaps := 0
	for _, p := range pairs {
		if p.Left == -1 {
			gaps++
		}
	}
	assert.Equal(t, 1, gaps, "expected exactly one gap on left for the inserted retry")
	assert.Len(t, pairs, 4)
}

func TestAlign_ToolOrderReversed(t *testing.T) {
	left := []model.AgentStep{
		step(model.StepKindToolCall, "search", ""),
		step(model.StepKindToolCall, "calc", ""),
	}
	right := []model.AgentStep{
		step(model.StepKindToolCall, "calc", ""),
		step(model.StepKindToolCall, "search", ""),
	}
	pairs := diff.Align(left, right)
	// At least one mismatch (either by matching tool name to tool name with
	// negative score, or by introducing gaps). Either way the resulting set
	// must contain four total slots (two left + two right) when interpreted
	// as a coverage of both inputs.
	leftCover := map[int]bool{}
	rightCover := map[int]bool{}
	for _, p := range pairs {
		if p.Left >= 0 {
			leftCover[p.Left] = true
		}
		if p.Right >= 0 {
			rightCover[p.Right] = true
		}
	}
	assert.Equal(t, 2, len(leftCover))
	assert.Equal(t, 2, len(rightCover))
}

func TestAlign_TotallyDifferent(t *testing.T) {
	left := []model.AgentStep{
		step(model.StepKindToolCall, "a", ""),
		step(model.StepKindToolCall, "b", ""),
	}
	right := []model.AgentStep{
		step(model.StepKindToolCall, "c", ""),
		step(model.StepKindToolCall, "d", ""),
	}
	pairs := diff.Align(left, right)
	leftCover := map[int]bool{}
	rightCover := map[int]bool{}
	for _, p := range pairs {
		if p.Left >= 0 {
			leftCover[p.Left] = true
		}
		if p.Right >= 0 {
			rightCover[p.Right] = true
		}
	}
	assert.Equal(t, 2, len(leftCover))
	assert.Equal(t, 2, len(rightCover))
}

func TestAlignMany_ThreeSequences(t *testing.T) {
	a := []model.AgentStep{
		step(model.StepKindPlannerLLM, "", ""),
		step(model.StepKindToolCall, "search", ""),
		step(model.StepKindPlannerLLM, "", ""),
	}
	b := []model.AgentStep{
		step(model.StepKindPlannerLLM, "", ""),
		step(model.StepKindToolCall, "search", ""),
		step(model.StepKindPlannerLLM, "", ""),
	}
	c := []model.AgentStep{
		step(model.StepKindPlannerLLM, "", ""),
		step(model.StepKindToolCall, "search", ""),
		step(model.StepKindToolCall, "calc", ""),
		step(model.StepKindPlannerLLM, "", ""),
	}
	rows := diff.AlignMany([][]model.AgentStep{a, b, c})
	// Each row must have 3 indices (one per sequence).
	for _, r := range rows {
		assert.Len(t, r.Indices, 3)
	}
	// At least one row should be a gap on a & b (showing c's extra calc step).
	gaps := 0
	for _, r := range rows {
		if r.Indices[0] == -1 && r.Indices[1] == -1 && r.Indices[2] >= 0 {
			gaps++
		}
	}
	assert.GreaterOrEqual(t, gaps, 1)
}
