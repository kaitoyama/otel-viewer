// Package analytics implements the filter DSL → DuckDB SQL translator and
// the higher-level analytical queries (frequency table, flow graph).
package analytics

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Filter is a small JSON DSL the UI builds. Fields are AND-combined; lists of
// values inside a field are OR-combined.
//
//	{
//	  "model": {"in": ["claude-opus-4-7", "claude-haiku-4-5"]},
//	  "task_id": {"eq": "ph:abc..."},
//	  "feedback_score": {"lt": 0.3},
//	  "tool_name": {"in": ["search_web"]},
//	  "tool_output_contains": "ERROR",
//	  "time": {"gte": 1716700000000000000, "lte": 1716800000000000000},
//	  "ingest_source": {"in": ["otlp"]}
//	}
type Filter struct {
	Model              *StringOp `json:"model,omitempty"`
	TaskID             *StringOp `json:"task_id,omitempty"`
	Vendor             *StringOp `json:"vendor,omitempty"`
	IngestSource       *StringOp `json:"ingest_source,omitempty"`
	ReplayGroup        string    `json:"replay_group,omitempty"`
	FeedbackScore      *NumOp    `json:"feedback_score,omitempty"`
	ToolName           *StringOp `json:"tool_name,omitempty"`
	ToolOutputContains string    `json:"tool_output_contains,omitempty"`
	Time               *TimeOp   `json:"time,omitempty"`
}

type StringOp struct {
	Eq string   `json:"eq,omitempty"`
	In []string `json:"in,omitempty"`
}
type NumOp struct {
	Eq *float64 `json:"eq,omitempty"`
	Lt *float64 `json:"lt,omitempty"`
	Lte *float64 `json:"lte,omitempty"`
	Gt *float64 `json:"gt,omitempty"`
	Gte *float64 `json:"gte,omitempty"`
}
type TimeOp struct {
	Gte int64 `json:"gte,omitempty"` // ns
	Lte int64 `json:"lte,omitempty"` // ns
}

// ParseFilter decodes a JSON-encoded filter. Empty bytes yields a zero Filter.
func ParseFilter(b []byte) (Filter, error) {
	var f Filter
	if len(b) == 0 {
		return f, nil
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, fmt.Errorf("parse filter: %w", err)
	}
	return f, nil
}

// BuildRunsWhere returns a WHERE clause fragment and args for runs (aliased
// as `r`). Joins for tool name / output / feedback are emitted as EXISTS
// subqueries to keep the planner happy across small and large corpora alike.
func BuildRunsWhere(f Filter) (string, []any, error) {
	var conds []string
	var args []any

	addStr := func(col string, op *StringOp) {
		if op == nil {
			return
		}
		if op.Eq != "" {
			conds = append(conds, col+" = ?")
			args = append(args, op.Eq)
		}
		if len(op.In) > 0 {
			conds = append(conds, col+" IN ("+placeholders(len(op.In))+")")
			for _, v := range op.In {
				args = append(args, v)
			}
		}
	}

	addStr("r.model", f.Model)
	addStr("r.task_id", f.TaskID)
	addStr("r.vendor", f.Vendor)
	addStr("r.ingest_source", f.IngestSource)

	if f.ReplayGroup != "" {
		conds = append(conds, "r.replay_group = ?")
		args = append(args, f.ReplayGroup)
	}

	if f.Time != nil {
		if f.Time.Gte > 0 {
			conds = append(conds, "r.start_ns >= ?")
			args = append(args, f.Time.Gte)
		}
		if f.Time.Lte > 0 {
			conds = append(conds, "r.start_ns <= ?")
			args = append(args, f.Time.Lte)
		}
	}

	if f.FeedbackScore != nil {
		// We treat the score op as "exists a feedback row matching".
		clause, scoreArgs, err := numOpClause("fb.score", f.FeedbackScore)
		if err != nil {
			return "", nil, err
		}
		conds = append(conds, "EXISTS (SELECT 1 FROM feedback fb WHERE fb.trace_id = r.trace_id AND "+clause+")")
		args = append(args, scoreArgs...)
	}

	if f.ToolName != nil && (f.ToolName.Eq != "" || len(f.ToolName.In) > 0) {
		var sub []string
		if f.ToolName.Eq != "" {
			sub = append(sub, "st.tool_name = ?")
			args = append(args, f.ToolName.Eq)
		}
		if len(f.ToolName.In) > 0 {
			sub = append(sub, "st.tool_name IN ("+placeholders(len(f.ToolName.In))+")")
			for _, v := range f.ToolName.In {
				args = append(args, v)
			}
		}
		conds = append(conds, "EXISTS (SELECT 1 FROM steps st WHERE st.trace_id = r.trace_id AND ("+strings.Join(sub, " OR ")+"))")
	}

	if f.ToolOutputContains != "" {
		conds = append(conds, "EXISTS (SELECT 1 FROM steps st WHERE st.trace_id = r.trace_id AND st.kind = 'ToolCall' AND CAST(st.payload AS VARCHAR) ILIKE ?)")
		args = append(args, "%"+f.ToolOutputContains+"%")
	}

	if len(conds) == 0 {
		return "", nil, nil
	}
	return "WHERE " + strings.Join(conds, " AND "), args, nil
}

func numOpClause(col string, op *NumOp) (string, []any, error) {
	if op == nil {
		return "", nil, nil
	}
	var parts []string
	var args []any
	if op.Eq != nil {
		parts = append(parts, col+" = ?")
		args = append(args, *op.Eq)
	}
	if op.Lt != nil {
		parts = append(parts, col+" < ?")
		args = append(args, *op.Lt)
	}
	if op.Lte != nil {
		parts = append(parts, col+" <= ?")
		args = append(args, *op.Lte)
	}
	if op.Gt != nil {
		parts = append(parts, col+" > ?")
		args = append(args, *op.Gt)
	}
	if op.Gte != nil {
		parts = append(parts, col+" >= ?")
		args = append(args, *op.Gte)
	}
	if len(parts) == 0 {
		return "", nil, errors.New("empty number op")
	}
	return strings.Join(parts, " AND "), args, nil
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

// SinceWindow is a helper for the UI to ship a relative time window.
func SinceWindow(d time.Duration) *TimeOp {
	if d <= 0 {
		return nil
	}
	return &TimeOp{Gte: time.Now().Add(-d).UnixNano()}
}
