package analytics

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/kaitoyama/otel-viewer/internal/store"
)

// FrequencyRow is one row of the tool/agent invocation frequency table.
type FrequencyRow struct {
	Model       string  `json:"model"`
	AgentName   string  `json:"agentName"`
	ToolName    string  `json:"toolName"`
	Operation   string  `json:"operation"`
	Calls       int     `json:"calls"`
	Errors      int     `json:"errors"`
	ErrorRate   float64 `json:"errorRate"`
	AvgDuration float64 `json:"avgDurationMs"`
	P95Duration float64 `json:"p95DurationMs"`
	AvgInTokens float64 `json:"avgInputTokens"`
	AvgOutTokens float64 `json:"avgOutputTokens"`
}

// Frequency builds the table grouped by (model, agent_name, tool_name, operation).
// Rows where all four group keys are empty are dropped.
func Frequency(ctx context.Context, s *store.Store, f Filter) ([]FrequencyRow, error) {
	where, args, err := BuildRunsWhere(f)
	if err != nil {
		return nil, err
	}
	q := fmt.Sprintf(`
		WITH filtered AS (
		    SELECT r.trace_id FROM runs r %s
		)
		SELECT * FROM (
		    SELECT COALESCE(st.model, '') AS model,
		           COALESCE(st.agent_name, '') AS agent_name,
		           COALESCE(st.tool_name, '') AS tool_name,
		           COALESCE(st.operation, '') AS operation,
		           COUNT(*) AS calls,
		           SUM(CASE WHEN COALESCE(st.status_error, '') <> '' THEN 1 ELSE 0 END) AS errors,
		           AVG(st.duration_ns / 1.0e6) AS avg_ms,
		           quantile_cont(st.duration_ns / 1.0e6, 0.95) AS p95_ms,
		           AVG(CAST(json_extract(st.payload, '$.usage.inputTokens') AS DOUBLE)) AS avg_in,
		           AVG(CAST(json_extract(st.payload, '$.usage.outputTokens') AS DOUBLE)) AS avg_out
		    FROM steps st
		    WHERE st.trace_id IN (SELECT trace_id FROM filtered)
		    GROUP BY 1,2,3,4
		) t
		WHERE NOT (model = '' AND agent_name = '' AND tool_name = '' AND operation = '')
		ORDER BY calls DESC
	`, where)
	rows, err := s.DB().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("frequency query: %w", err)
	}
	defer rows.Close()
	var out []FrequencyRow
	for rows.Next() {
		var (
			r            FrequencyRow
			avgIn, avgOut sql.NullFloat64
		)
		if err := rows.Scan(&r.Model, &r.AgentName, &r.ToolName, &r.Operation,
			&r.Calls, &r.Errors, &r.AvgDuration, &r.P95Duration, &avgIn, &avgOut); err != nil {
			return nil, err
		}
		if r.Calls > 0 {
			r.ErrorRate = float64(r.Errors) / float64(r.Calls)
		}
		if avgIn.Valid {
			r.AvgInTokens = avgIn.Float64
		}
		if avgOut.Valid {
			r.AvgOutTokens = avgOut.Float64
		}
		out = append(out, r)
	}
	return out, nil
}
