package task_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kaitoyama/otel-viewer/internal/model"
	"github.com/kaitoyama/otel-viewer/internal/task"
)

func TestResolve_PriorityExplicit(t *testing.T) {
	r := &model.Run{Tags: map[string]string{
		"gen_ai.task.id":   "task-123",
		"gen_ai.task.name": "summarize",
	}, PromptHash: "abc"}
	id, src := task.Resolve(r)
	assert.Equal(t, "task-123", id)
	assert.Equal(t, model.TaskSourceExplicit, src)
}

func TestResolve_FallbackToName(t *testing.T) {
	r := &model.Run{Tags: map[string]string{"gen_ai.task.name": "summarize"}}
	id, src := task.Resolve(r)
	assert.Equal(t, "name:summarize", id)
	assert.Equal(t, model.TaskSourceTaskName, src)
}

func TestResolve_FallbackToConversation(t *testing.T) {
	r := &model.Run{Tags: map[string]string{"gen_ai.conversation.id": "conv-9"}}
	id, src := task.Resolve(r)
	assert.Equal(t, "conv:conv-9", id)
	assert.Equal(t, model.TaskSourceConversationID, src)
}

func TestResolve_FallbackToPromptHash(t *testing.T) {
	r := &model.Run{
		Tags:       map[string]string{},
		PromptHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
	}
	id, src := task.Resolve(r)
	assert.Equal(t, "ph:0123456789abcdef", id)
	assert.Equal(t, model.TaskSourcePromptHash, src)
}

func TestResolve_Unknown(t *testing.T) {
	r := &model.Run{Tags: map[string]string{}}
	_, src := task.Resolve(r)
	assert.Equal(t, model.TaskSourceUnknown, src)
}

func TestApply_PreservesManual(t *testing.T) {
	r := &model.Run{
		TaskID:     "custom",
		TaskSource: model.TaskSourceManual,
		Tags:       map[string]string{"gen_ai.task.id": "would-override"},
	}
	task.Apply(r)
	assert.Equal(t, "custom", r.TaskID)
	assert.Equal(t, model.TaskSourceManual, r.TaskSource)
}
