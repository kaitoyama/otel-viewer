package analytics

import (
	"context"
	"fmt"

	"github.com/kaitoyama/otel-viewer/internal/store"
)

// FlowNode is one node in the aggregated flow graph.
type FlowNode struct {
	ID        string `json:"id"`        // canonical key
	Kind      string `json:"kind"`      // PlannerLLM / ToolCall / AgentInvoke / Other
	AgentName string `json:"agentName,omitempty"`
	ToolName  string `json:"toolName,omitempty"`
	Operation string `json:"operation,omitempty"`
	Count     int    `json:"count"`
}

// FlowEdge is a directed transition with its observed count.
type FlowEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Count int    `json:"count"`
}

type FlowGraph struct {
	Nodes []FlowNode `json:"nodes"`
	Edges []FlowEdge `json:"edges"`
}

func nodeKey(kind, agent, tool, op string) string {
	return kind + "|" + agent + "|" + tool + "|" + op
}

// Flow returns the aggregated node/edge counts for runs that match the filter.
// Edges are computed from consecutive step indices within each trace.
func Flow(ctx context.Context, s *store.Store, f Filter) (*FlowGraph, error) {
	where, args, err := BuildRunsWhere(f)
	if err != nil {
		return nil, err
	}
	// Node counts.
	nq := fmt.Sprintf(`
		WITH filtered AS (
		    SELECT r.trace_id FROM runs r %s
		)
		SELECT st.kind,
		       COALESCE(st.agent_name, '') AS agent,
		       COALESCE(st.tool_name, '') AS tool,
		       COALESCE(st.operation, '') AS op,
		       COUNT(*) AS c
		FROM steps st
		WHERE st.trace_id IN (SELECT trace_id FROM filtered)
		GROUP BY 1,2,3,4
	`, where)
	rows, err := s.DB().QueryContext(ctx, nq, args...)
	if err != nil {
		return nil, fmt.Errorf("flow nodes: %w", err)
	}
	graph := &FlowGraph{}
	for rows.Next() {
		var n FlowNode
		if err := rows.Scan(&n.Kind, &n.AgentName, &n.ToolName, &n.Operation, &n.Count); err != nil {
			rows.Close()
			return nil, err
		}
		n.ID = nodeKey(n.Kind, n.AgentName, n.ToolName, n.Operation)
		graph.Nodes = append(graph.Nodes, n)
	}
	rows.Close()

	// Edge counts via self-join on consecutive step_index.
	eq := fmt.Sprintf(`
		WITH filtered AS (
		    SELECT r.trace_id FROM runs r %s
		)
		SELECT a.kind, COALESCE(a.agent_name, ''), COALESCE(a.tool_name, ''), COALESCE(a.operation, ''),
		       b.kind, COALESCE(b.agent_name, ''), COALESCE(b.tool_name, ''), COALESCE(b.operation, ''),
		       COUNT(*) AS c
		FROM steps a
		JOIN steps b
		  ON a.trace_id = b.trace_id AND b.step_index = a.step_index + 1
		WHERE a.trace_id IN (SELECT trace_id FROM filtered)
		GROUP BY 1,2,3,4,5,6,7,8
	`, where)
	rows, err = s.DB().QueryContext(ctx, eq, args...)
	if err != nil {
		return nil, fmt.Errorf("flow edges: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			fk, fa, ft, fo string
			tk, ta, tt, to string
			c              int
		)
		if err := rows.Scan(&fk, &fa, &ft, &fo, &tk, &ta, &tt, &to, &c); err != nil {
			return nil, err
		}
		graph.Edges = append(graph.Edges, FlowEdge{
			From:  nodeKey(fk, fa, ft, fo),
			To:    nodeKey(tk, ta, tt, to),
			Count: c,
		})
	}
	return graph, nil
}
