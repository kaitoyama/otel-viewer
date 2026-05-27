// Package store wraps DuckDB as the single source of truth for runs, steps,
// events, feedback, task membership, replay links and Tempo backfill jobs.
package store

import (
	"context"
	"database/sql"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "github.com/marcboeker/go-duckdb"

	"github.com/kaitoyama/otel-viewer/internal/model"
)

//go:embed schema.sql
var schemaSQL string

type Store struct {
	db *sql.DB
}

func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("duckdb", dsn)
	if err != nil {
		return nil, fmt.Errorf("open duckdb: %w", err)
	}
	if _, err := db.ExecContext(ctx, schemaSQL); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) DB() *sql.DB { return s.db }

// UpsertRun inserts or replaces a run together with its steps and events.
// The operation is performed in a single transaction.
func (s *Store) UpsertRun(ctx context.Context, r *model.Run, events []EventRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	tagsJSON, _ := json.Marshal(r.Tags)
	totalsJSON, _ := json.Marshal(r.Totals)

	if _, err := tx.ExecContext(ctx, `DELETE FROM steps WHERE trace_id = ?`, r.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM events WHERE trace_id = ?`, r.ID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM runs WHERE trace_id = ?`, r.ID); err != nil {
		return err
	}

	var replayOf, replayGroup any
	if r.ReplayOf != nil {
		replayOf = *r.ReplayOf
	}
	if r.ReplayGroup != nil {
		replayGroup = *r.ReplayGroup
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO runs
		  (trace_id, name, start_ns, end_ns, model, vendor, task_id, task_source,
		   replay_of, replay_group, prompt_hash, tags, totals, ingest_source)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	`,
		r.ID, r.Name, r.StartTime.UnixNano(), r.EndTime.UnixNano(),
		r.Model, r.Vendor, r.TaskID, string(r.TaskSource),
		replayOf, replayGroup, r.PromptHash,
		string(tagsJSON), string(totalsJSON), r.IngestSource,
	); err != nil {
		return fmt.Errorf("insert run: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO steps
		  (trace_id, span_id, parent_span_id, step_index, kind,
		   start_ns, duration_ns, agent_name, tool_name, operation, model,
		   status_error, payload)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
	`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, st := range r.Steps {
		toolName := ""
		if st.Tool != nil {
			toolName = st.Tool.Name
		}
		payload := stepPayload{
			Messages:   st.Messages,
			Output:     st.Output,
			Tool:       st.Tool,
			Usage:      st.Usage,
			Evals:      st.Evals,
		}
		p, _ := json.Marshal(payload)
		if _, err := stmt.ExecContext(ctx,
			r.ID, st.SpanID, st.ParentSpanID, st.Index, string(st.Kind),
			st.StartTime.UnixNano(), int64(st.Duration),
			st.AgentName, toolName, st.Operation, st.Model,
			st.StatusError, string(p),
		); err != nil {
			return fmt.Errorf("insert step: %w", err)
		}
	}

	if len(events) > 0 {
		eStmt, err := tx.PrepareContext(ctx, `
			INSERT INTO events (trace_id, span_id, time_ns, name, payload)
			VALUES (?,?,?,?,?)
		`)
		if err != nil {
			return err
		}
		defer eStmt.Close()
		for _, e := range events {
			pl, _ := json.Marshal(e.Attributes)
			if _, err := eStmt.ExecContext(ctx,
				r.ID, e.SpanID, e.TimeNs, e.Name, string(pl),
			); err != nil {
				return fmt.Errorf("insert event: %w", err)
			}
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM task_membership WHERE trace_id = ?`, r.ID); err != nil {
		return err
	}
	if r.TaskID != "" {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO task_membership (task_id, trace_id, method) VALUES (?,?,?)`,
			r.TaskID, r.ID, string(r.TaskSource)); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM replay_links WHERE trace_id = ?`, r.ID); err != nil {
		return err
	}
	if r.ReplayGroup != nil && *r.ReplayGroup != "" {
		role := "shadow"
		if r.ReplayOf == nil {
			role = "primary"
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO replay_links (group_id, trace_id, role) VALUES (?,?,?)`,
			*r.ReplayGroup, r.ID, role); err != nil {
			return err
		}
	}

	return tx.Commit()
}

type stepPayload struct {
	Messages []model.Message       `json:"messages,omitempty"`
	Output   string                `json:"output,omitempty"`
	Tool     *model.ToolInvocation `json:"tool,omitempty"`
	Usage    model.Usage           `json:"usage"`
	Evals    []model.EvalResult    `json:"evals,omitempty"`
}

type EventRecord struct {
	SpanID     string
	TimeNs     int64
	Name       string
	Attributes map[string]any
}

// GetRun returns a full Run, including steps and feedback. Returns
// sql.ErrNoRows if absent.
func (s *Store) GetRun(ctx context.Context, traceID string) (*model.Run, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT trace_id, name, start_ns, end_ns, model, vendor, task_id, task_source,
		       replay_of, replay_group, prompt_hash, tags, totals, ingest_source
		FROM runs WHERE trace_id = ?
	`, traceID)
	var (
		r                                          model.Run
		startNS, endNS                             int64
		taskSource                                 string
		replayOf, replayGroup                      sql.NullString
		tagsJSON, totalsJSON                       string
	)
	if err := row.Scan(&r.ID, &r.Name, &startNS, &endNS, &r.Model, &r.Vendor,
		&r.TaskID, &taskSource, &replayOf, &replayGroup, &r.PromptHash,
		&tagsJSON, &totalsJSON, &r.IngestSource); err != nil {
		return nil, err
	}
	r.StartTime = time.Unix(0, startNS)
	r.EndTime = time.Unix(0, endNS)
	r.TaskSource = model.TaskSource(taskSource)
	if replayOf.Valid {
		v := replayOf.String
		r.ReplayOf = &v
	}
	if replayGroup.Valid {
		v := replayGroup.String
		r.ReplayGroup = &v
	}
	_ = json.Unmarshal([]byte(tagsJSON), &r.Tags)
	_ = json.Unmarshal([]byte(totalsJSON), &r.Totals)

	rows, err := s.db.QueryContext(ctx, `
		SELECT span_id, parent_span_id, step_index, kind,
		       start_ns, duration_ns, agent_name, tool_name, operation, model,
		       status_error, payload
		FROM steps WHERE trace_id = ? ORDER BY step_index
	`, traceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			st       model.AgentStep
			startNs  int64
			durNs    int64
			kind     string
			toolName string
			payload  string
		)
		if err := rows.Scan(&st.SpanID, &st.ParentSpanID, &st.Index, &kind,
			&startNs, &durNs, &st.AgentName, &toolName, &st.Operation, &st.Model,
			&st.StatusError, &payload); err != nil {
			return nil, err
		}
		st.Kind = model.StepKind(kind)
		st.StartTime = time.Unix(0, startNs)
		st.Duration = time.Duration(durNs)
		var p stepPayload
		_ = json.Unmarshal([]byte(payload), &p)
		st.Messages = p.Messages
		st.Output = p.Output
		st.Tool = p.Tool
		st.Usage = p.Usage
		st.Evals = p.Evals
		if st.Tool == nil && toolName != "" {
			st.Tool = &model.ToolInvocation{Name: toolName}
		}
		r.Steps = append(r.Steps, st)
	}

	fbRows, err := s.db.QueryContext(ctx, `
		SELECT id, COALESCE(span_id, ''), source, score, label, comment, created_at
		FROM feedback WHERE trace_id = ? ORDER BY created_at
	`, traceID)
	if err == nil {
		defer fbRows.Close()
		for fbRows.Next() {
			var fb model.FeedbackEntry
			fb.TraceID = traceID
			if err := fbRows.Scan(&fb.ID, &fb.SpanID, &fb.Source, &fb.Score, &fb.Label, &fb.Comment, &fb.CreatedAt); err != nil {
				return nil, err
			}
			r.Feedback = append(r.Feedback, fb)
		}
	}
	return &r, nil
}

// RunListItem is a row in the trace list. It does not include steps.
type RunListItem struct {
	TraceID      string            `json:"traceId"`
	Name         string            `json:"name"`
	StartTime    time.Time         `json:"startTime"`
	EndTime      time.Time         `json:"endTime"`
	Model        string            `json:"model"`
	Vendor       string            `json:"vendor"`
	TaskID       string            `json:"taskId"`
	ReplayGroup  *string           `json:"replayGroup,omitempty"`
	ReplayOf     *string           `json:"replayOf,omitempty"`
	IngestSource string            `json:"ingestSource"`
	Tags         map[string]string `json:"tags"`
	Totals       model.Usage       `json:"totals"`
	StepCount    int               `json:"stepCount"`
	HasFeedback  bool              `json:"hasFeedback"`
	MinFeedback  *float64          `json:"minFeedback,omitempty"`
}

// RunFilter is a simple, programmatic filter for run lists.
// Empty fields are ignored.
type RunFilter struct {
	Models          []string
	TaskIDs         []string
	Vendors         []string
	IngestSources   []string
	ReplayGroup     string
	StartAfterNs    int64
	StartBeforeNs   int64
	FeedbackMaxScore *float64
	ToolNameAny     []string
	ToolOutputLike  string
	Limit           int
	Offset          int
}

func (s *Store) ListRuns(ctx context.Context, f RunFilter) ([]RunListItem, error) {
	var (
		conds []string
		args  []any
	)
	if len(f.Models) > 0 {
		conds = append(conds, "r.model IN ("+placeholders(len(f.Models))+")")
		for _, v := range f.Models {
			args = append(args, v)
		}
	}
	if len(f.TaskIDs) > 0 {
		conds = append(conds, "r.task_id IN ("+placeholders(len(f.TaskIDs))+")")
		for _, v := range f.TaskIDs {
			args = append(args, v)
		}
	}
	if len(f.Vendors) > 0 {
		conds = append(conds, "r.vendor IN ("+placeholders(len(f.Vendors))+")")
		for _, v := range f.Vendors {
			args = append(args, v)
		}
	}
	if len(f.IngestSources) > 0 {
		conds = append(conds, "r.ingest_source IN ("+placeholders(len(f.IngestSources))+")")
		for _, v := range f.IngestSources {
			args = append(args, v)
		}
	}
	if f.ReplayGroup != "" {
		conds = append(conds, "r.replay_group = ?")
		args = append(args, f.ReplayGroup)
	}
	if f.StartAfterNs > 0 {
		conds = append(conds, "r.start_ns >= ?")
		args = append(args, f.StartAfterNs)
	}
	if f.StartBeforeNs > 0 {
		conds = append(conds, "r.start_ns <= ?")
		args = append(args, f.StartBeforeNs)
	}
	if f.FeedbackMaxScore != nil {
		conds = append(conds, "EXISTS (SELECT 1 FROM feedback fb WHERE fb.trace_id = r.trace_id AND fb.score <= ?)")
		args = append(args, *f.FeedbackMaxScore)
	}
	if len(f.ToolNameAny) > 0 {
		conds = append(conds, "EXISTS (SELECT 1 FROM steps st WHERE st.trace_id = r.trace_id AND st.tool_name IN ("+placeholders(len(f.ToolNameAny))+"))")
		for _, v := range f.ToolNameAny {
			args = append(args, v)
		}
	}
	if f.ToolOutputLike != "" {
		conds = append(conds, "EXISTS (SELECT 1 FROM steps st WHERE st.trace_id = r.trace_id AND st.kind = 'ToolCall' AND CAST(st.payload AS VARCHAR) ILIKE ?)")
		args = append(args, "%"+f.ToolOutputLike+"%")
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	q := fmt.Sprintf(`
		SELECT r.trace_id, r.name, r.start_ns, r.end_ns, r.model, r.vendor, r.task_id,
		       r.replay_of, r.replay_group, r.ingest_source, r.tags, r.totals,
		       (SELECT COUNT(*) FROM steps st WHERE st.trace_id = r.trace_id),
		       (SELECT COUNT(*) FROM feedback fb WHERE fb.trace_id = r.trace_id),
		       (SELECT MIN(score) FROM feedback fb WHERE fb.trace_id = r.trace_id)
		FROM runs r
		%s
		ORDER BY r.start_ns DESC
		LIMIT %d OFFSET %d
	`, where, limit, f.Offset)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunListItem
	for rows.Next() {
		var (
			it                      RunListItem
			startNs, endNs          int64
			replayOf, replayGroup   sql.NullString
			tagsJSON, totalsJSON    string
			stepCount, fbCount      int
			minScore                sql.NullFloat64
		)
		if err := rows.Scan(&it.TraceID, &it.Name, &startNs, &endNs, &it.Model, &it.Vendor, &it.TaskID,
			&replayOf, &replayGroup, &it.IngestSource, &tagsJSON, &totalsJSON,
			&stepCount, &fbCount, &minScore); err != nil {
			return nil, err
		}
		it.StartTime = time.Unix(0, startNs)
		it.EndTime = time.Unix(0, endNs)
		if replayOf.Valid {
			v := replayOf.String
			it.ReplayOf = &v
		}
		if replayGroup.Valid {
			v := replayGroup.String
			it.ReplayGroup = &v
		}
		_ = json.Unmarshal([]byte(tagsJSON), &it.Tags)
		_ = json.Unmarshal([]byte(totalsJSON), &it.Totals)
		it.StepCount = stepCount
		it.HasFeedback = fbCount > 0
		if minScore.Valid {
			v := minScore.Float64
			it.MinFeedback = &v
		}
		out = append(out, it)
	}
	return out, nil
}

// InsertFeedback persists a feedback entry. If id is empty a uuid is generated.
func (s *Store) InsertFeedback(ctx context.Context, fb *model.FeedbackEntry) error {
	if fb.ID == "" {
		fb.ID = uuid.NewString()
	}
	if fb.CreatedAt.IsZero() {
		fb.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO feedback (id, trace_id, span_id, source, score, label, comment, created_at)
		VALUES (?,?,?,?,?,?,?,?)
	`, fb.ID, fb.TraceID, nullString(fb.SpanID), fb.Source, fb.Score, fb.Label, fb.Comment, fb.CreatedAt)
	return err
}

func (s *Store) ListFeedback(ctx context.Context, traceID string, maxScore *float64, limit int) ([]model.FeedbackEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	var (
		conds []string
		args  []any
	)
	if traceID != "" {
		conds = append(conds, "trace_id = ?")
		args = append(args, traceID)
	}
	if maxScore != nil {
		conds = append(conds, "score <= ?")
		args = append(args, *maxScore)
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	q := fmt.Sprintf(`
		SELECT id, trace_id, COALESCE(span_id, ''), source, score, label, comment, created_at
		FROM feedback %s ORDER BY created_at DESC LIMIT %d
	`, where, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.FeedbackEntry
	for rows.Next() {
		var fb model.FeedbackEntry
		if err := rows.Scan(&fb.ID, &fb.TraceID, &fb.SpanID, &fb.Source, &fb.Score, &fb.Label, &fb.Comment, &fb.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, fb)
	}
	return out, nil
}

// AddManualTaskMember associates an existing run with a task id via the manual
// route. It returns ErrUnknownTrace if the trace does not exist.
var ErrUnknownTrace = errors.New("unknown trace")

func (s *Store) AddManualTaskMember(ctx context.Context, taskID, traceID string) error {
	var exists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM runs WHERE trace_id = ?`, traceID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrUnknownTrace
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE runs SET task_id = ?, task_source = 'manual' WHERE trace_id = ?
	`, taskID, traceID); err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM task_membership WHERE task_id = ? AND trace_id = ?`,
		taskID, traceID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO task_membership (task_id, trace_id, method) VALUES (?,?,?)`,
		taskID, traceID, "manual")
	return err
}

func (s *Store) RemoveManualTaskMember(ctx context.Context, taskID, traceID string) error {
	if _, err := s.db.ExecContext(ctx, `
		DELETE FROM task_membership WHERE task_id = ? AND trace_id = ? AND method = 'manual'
	`, taskID, traceID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE runs SET task_id = '', task_source = 'unknown'
		WHERE trace_id = ? AND task_id = ? AND task_source = 'manual'
	`, traceID, taskID)
	return err
}

// ListTasks aggregates by task_id with per-task model breakdown.
func (s *Store) ListTasks(ctx context.Context, limit int) ([]model.Task, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT task_id,
		       MAX(task_source),
		       COUNT(*) AS run_count,
		       MAX(end_ns) AS last_seen,
		       LIST(DISTINCT model) AS models,
		       AVG((SELECT AVG(score) FROM feedback fb WHERE fb.trace_id = r.trace_id)) AS avg_score
		FROM runs r
		WHERE task_id IS NOT NULL AND task_id <> ''
		GROUP BY task_id
		ORDER BY last_seen DESC
		LIMIT %d
	`, limit))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Task
	for rows.Next() {
		var (
			t         model.Task
			source    string
			lastNs    int64
			modelsRaw any
			avgScore  sql.NullFloat64
		)
		if err := rows.Scan(&t.ID, &source, &t.RunCount, &lastNs, &modelsRaw, &avgScore); err != nil {
			return nil, err
		}
		t.Source = model.TaskSource(source)
		t.LastSeen = time.Unix(0, lastNs)
		t.Models = stringList(modelsRaw)
		if avgScore.Valid {
			v := avgScore.Float64
			t.AvgScore = &v
		}
		out = append(out, t)
	}
	return out, nil
}

func (s *Store) GetTask(ctx context.Context, taskID string) (*model.Task, []RunListItem, error) {
	t := model.Task{ID: taskID}
	row := s.db.QueryRowContext(ctx, `
		SELECT MAX(task_source), COUNT(*), MAX(end_ns),
		       LIST(DISTINCT model), LIST(DISTINCT prompt_hash)
		FROM runs WHERE task_id = ?
	`, taskID)
	var (
		source    sql.NullString
		count     int
		lastNs    sql.NullInt64
		modelsRaw any
		hashRaw   any
	)
	if err := row.Scan(&source, &count, &lastNs, &modelsRaw, &hashRaw); err != nil {
		return nil, nil, err
	}
	if count == 0 {
		return nil, nil, sql.ErrNoRows
	}
	if source.Valid {
		t.Source = model.TaskSource(source.String)
	}
	t.RunCount = count
	if lastNs.Valid {
		t.LastSeen = time.Unix(0, lastNs.Int64)
	}
	t.Models = stringList(modelsRaw)
	t.PromptHashes = stringList(hashRaw)
	runs, err := s.ListRuns(ctx, RunFilter{TaskIDs: []string{taskID}, Limit: 500})
	if err != nil {
		return nil, nil, err
	}
	return &t, runs, nil
}

func (s *Store) GetReplayGroup(ctx context.Context, groupID string) ([]RunListItem, error) {
	return s.ListRuns(ctx, RunFilter{ReplayGroup: groupID, Limit: 50})
}

// PromptHashIndex returns trace_ids that share a prompt hash with the given
// trace. Used by the task resolver to bucket retroactively.
func (s *Store) PromptHashIndex(ctx context.Context, hash string) ([]string, error) {
	if hash == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT trace_id FROM runs WHERE prompt_hash = ?`, hash)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// CreateBackfillJob persists a new backfill record. The caller is responsible
// for running the actual job loop and calling UpdateBackfill as it progresses.
func (s *Store) CreateBackfillJob(ctx context.Context, j *BackfillJob) error {
	if j.ID == "" {
		j.ID = uuid.NewString()
	}
	j.Status = "pending"
	j.StartedAt = time.Now().UTC()
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO tempo_backfill_jobs
		  (id, base_url, traceql, start_ns, end_ns, status, fetched, total, started_at)
		VALUES (?,?,?,?,?,?,?,?,?)
	`, j.ID, j.BaseURL, j.TraceQL, j.StartNs, j.EndNs, j.Status, j.Fetched, j.Total, j.StartedAt)
	return err
}

func (s *Store) UpdateBackfill(ctx context.Context, id string, fetched, total int, status, errStr string) error {
	finished := sql.NullTime{}
	if status == "done" || status == "failed" {
		finished = sql.NullTime{Valid: true, Time: time.Now().UTC()}
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE tempo_backfill_jobs
		SET fetched = ?, total = ?, status = ?, error = ?, finished_at = ?
		WHERE id = ?
	`, fetched, total, status, errStr, finished, id)
	return err
}

func (s *Store) ListBackfillJobs(ctx context.Context) ([]BackfillJob, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, base_url, traceql, start_ns, end_ns, status, fetched, total,
		       started_at, finished_at, COALESCE(error, '')
		FROM tempo_backfill_jobs
		ORDER BY started_at DESC
		LIMIT 100
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BackfillJob
	for rows.Next() {
		var (
			j        BackfillJob
			finished sql.NullTime
		)
		if err := rows.Scan(&j.ID, &j.BaseURL, &j.TraceQL, &j.StartNs, &j.EndNs,
			&j.Status, &j.Fetched, &j.Total, &j.StartedAt, &finished, &j.Error); err != nil {
			return nil, err
		}
		if finished.Valid {
			j.FinishedAt = &finished.Time
		}
		out = append(out, j)
	}
	return out, nil
}

type BackfillJob struct {
	ID         string     `json:"id"`
	BaseURL    string     `json:"baseUrl"`
	TraceQL    string     `json:"traceql"`
	StartNs    int64      `json:"startNs"`
	EndNs      int64      `json:"endNs"`
	Status     string     `json:"status"`
	Fetched    int        `json:"fetched"`
	Total      int        `json:"total"`
	StartedAt  time.Time  `json:"startedAt"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	Error      string     `json:"error,omitempty"`
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat("?,", n-1) + "?"
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// stringList parses DuckDB LIST(VARCHAR) values returned via go-duckdb.
// The driver currently returns []any for nested list types.
func stringList(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		if x == "" {
			return nil
		}
		// fallback: comma separated
		return strings.Split(x, ",")
	}
	return nil
}
