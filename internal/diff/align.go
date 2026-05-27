// Package diff aligns two or more sequences of AgentStep using a
// Needleman–Wunsch global alignment.
//
// Signatures are model-agnostic, since we explicitly want to compare runs of
// the same task across different models. The exposed Score function makes the
// scoring contract testable in isolation.
package diff

import (
	"github.com/kaitoyama/otel-viewer/internal/model"
)

// Signature returns the alignment key for a step.
func Signature(s model.AgentStep) string {
	switch s.Kind {
	case model.StepKindPlannerLLM:
		return "P"
	case model.StepKindToolCall:
		name := ""
		if s.Tool != nil {
			name = s.Tool.Name
		}
		return "T:" + name
	case model.StepKindAgentInvoke:
		return "A:" + s.AgentName
	case model.StepKindObservation:
		return "O"
	}
	return "X"
}

// Score returns the cell score between two signatures. Higher is better.
func Score(a, b string) int {
	if a == b {
		if a == "P" {
			return 2
		}
		return 3
	}
	// Both tool calls but different name.
	if len(a) > 2 && len(b) > 2 && a[:2] == "T:" && b[:2] == "T:" {
		return -2
	}
	if len(a) > 2 && len(b) > 2 && a[:2] == "A:" && b[:2] == "A:" {
		return -1
	}
	if a == "P" || b == "P" {
		return -1
	}
	return -3
}

const gapPenalty = -1

// Pair represents one row of the aligned table. Left or Right may be -1 to
// mean "gap on that side".
type Pair struct {
	Left  int `json:"left"`
	Right int `json:"right"`
}

// Align returns the alignment of two step sequences using Needleman–Wunsch.
func Align(left, right []model.AgentStep) []Pair {
	n, m := len(left), len(right)
	if n == 0 && m == 0 {
		return nil
	}
	// dp[i][j] = best score aligning left[:i] with right[:j]
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := 1; i <= n; i++ {
		dp[i][0] = dp[i-1][0] + gapPenalty
	}
	for j := 1; j <= m; j++ {
		dp[0][j] = dp[0][j-1] + gapPenalty
	}
	lSig := make([]string, n)
	for i := range left {
		lSig[i] = Signature(left[i])
	}
	rSig := make([]string, m)
	for j := range right {
		rSig[j] = Signature(right[j])
	}
	for i := 1; i <= n; i++ {
		for j := 1; j <= m; j++ {
			diag := dp[i-1][j-1] + Score(lSig[i-1], rSig[j-1])
			up := dp[i-1][j] + gapPenalty
			lt := dp[i][j-1] + gapPenalty
			dp[i][j] = max3(diag, up, lt)
		}
	}
	// Backtrace.
	var out []Pair
	i, j := n, m
	for i > 0 || j > 0 {
		switch {
		case i > 0 && j > 0 && dp[i][j] == dp[i-1][j-1]+Score(lSig[i-1], rSig[j-1]):
			out = append(out, Pair{Left: i - 1, Right: j - 1})
			i--
			j--
		case i > 0 && dp[i][j] == dp[i-1][j]+gapPenalty:
			out = append(out, Pair{Left: i - 1, Right: -1})
			i--
		default:
			out = append(out, Pair{Left: -1, Right: j - 1})
			j--
		}
	}
	// Reverse.
	for k, l := 0, len(out)-1; k < l; k, l = k+1, l-1 {
		out[k], out[l] = out[l], out[k]
	}
	return out
}

// AlignMany aligns N (2..4) sequences against the leftmost one. The result is
// a matrix where row.Indices[i] is the index in sequence i (or -1 for gap).
type Row struct {
	Indices []int `json:"indices"`
}

func AlignMany(sequences [][]model.AgentStep) []Row {
	if len(sequences) == 0 {
		return nil
	}
	if len(sequences) == 1 {
		out := make([]Row, len(sequences[0]))
		for i := range sequences[0] {
			out[i] = Row{Indices: []int{i}}
		}
		return out
	}
	base := sequences[0]
	pairs := make([][]Pair, len(sequences))
	for i := 1; i < len(sequences); i++ {
		pairs[i] = Align(base, sequences[i])
	}
	// Build a unified row list driven by the base sequence index. For each
	// row in each Align result that consumes a left index, we anchor it at
	// that left index; gap-on-left rows are interleaved in order.
	var rows []aux
	rows = makeBaseRows(len(base))
	for i := 1; i < len(sequences); i++ {
		// Splice gap-on-base entries from this alignment into rows in order.
		rows = splice(rows, pairs[i], i)
	}
	// Now collapse rows into Row{Indices: []}.
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		row := Row{Indices: make([]int, len(sequences))}
		for k := range row.Indices {
			row.Indices[k] = -1
		}
		if r.baseIdx >= 0 {
			row.Indices[0] = r.baseIdx
		}
		if r.seq > 0 {
			row.Indices[r.seq] = r.right
		}
		out = append(out, row)
	}
	// Merge: for any base-anchored row, look up the corresponding right index
	// in each non-base sequence via its pairs slice.
	if len(sequences) > 2 {
		// Build base->right maps.
		maps := make([]map[int]int, len(sequences))
		for i := 1; i < len(sequences); i++ {
			m := make(map[int]int)
			for _, p := range pairs[i] {
				if p.Left >= 0 && p.Right >= 0 {
					m[p.Left] = p.Right
				}
			}
			maps[i] = m
		}
		for ri, r := range out {
			if r.Indices[0] < 0 {
				continue
			}
			for i := 1; i < len(sequences); i++ {
				if r.Indices[i] >= 0 {
					continue
				}
				if v, ok := maps[i][r.Indices[0]]; ok {
					out[ri].Indices[i] = v
				}
			}
		}
	}
	return out
}

type aux struct {
	baseIdx int // -1 if gap on base side
	right   int // index in sequences[seq]
	seq     int // which sequence the row came from
}

func makeBaseRows(n int) []aux {
	out := make([]aux, n)
	for i := 0; i < n; i++ {
		out[i] = aux{baseIdx: i, right: -1, seq: 0}
	}
	return out
}

func splice(rows []aux, pairs []Pair, seq int) []aux {
	// Walk through pairs in order; when we see Left==-1, insert a new row at
	// the position immediately after the previous Left index in rows.
	insertAfter := -1 // -1 means "before everything"
	for _, p := range pairs {
		if p.Left >= 0 {
			insertAfter = p.Left
			continue
		}
		pos := 0
		if insertAfter >= 0 {
			for i, r := range rows {
				if r.baseIdx == insertAfter {
					pos = i + 1
					break
				}
			}
		}
		newRow := aux{baseIdx: -1, right: p.Right, seq: seq}
		rows = append(rows[:pos], append([]aux{newRow}, rows[pos:]...)...)
	}
	return rows
}

func max3(a, b, c int) int {
	m := a
	if b > m {
		m = b
	}
	if c > m {
		m = c
	}
	return m
}
