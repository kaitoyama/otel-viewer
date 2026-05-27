// Package task resolves the canonical task id of a run, with this priority:
//
//  1. gen_ai.task.id attribute (explicit)
//  2. gen_ai.task.name attribute
//  3. gen_ai.conversation.id attribute
//  4. prompt SHA-256 hash, prefixed with "ph:"
//  5. manual UI grouping (handled by store.AddManualTaskMember)
//
// The resolver only handles paths 1–4; manual membership is recorded outside
// the normalize pipeline and overrides any other source.
package task

import "github.com/kaitoyama/otel-viewer/internal/model"

func Resolve(r *model.Run) (string, model.TaskSource) {
	if v := r.Tags["gen_ai.task.id"]; v != "" {
		return v, model.TaskSourceExplicit
	}
	if v := r.Tags["gen_ai.task.name"]; v != "" {
		return "name:" + v, model.TaskSourceTaskName
	}
	if v := r.Tags["gen_ai.conversation.id"]; v != "" {
		return "conv:" + v, model.TaskSourceConversationID
	}
	if r.PromptHash != "" {
		return "ph:" + r.PromptHash[:16], model.TaskSourcePromptHash
	}
	return "", model.TaskSourceUnknown
}

// Apply mutates the run with the resolved task id, unless it was already set
// (e.g., by a previous manual assignment that we want to preserve).
func Apply(r *model.Run) {
	if r.TaskSource == model.TaskSourceManual {
		return
	}
	id, src := Resolve(r)
	r.TaskID = id
	r.TaskSource = src
}
