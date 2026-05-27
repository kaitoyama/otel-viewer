package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/kaitoyama/otel-viewer/internal/analytics"
	"github.com/kaitoyama/otel-viewer/internal/diff"
	jsonlpkg "github.com/kaitoyama/otel-viewer/internal/ingest/jsonl"
	tempopkg "github.com/kaitoyama/otel-viewer/internal/ingest/tempo"
	"github.com/kaitoyama/otel-viewer/internal/model"
	"github.com/kaitoyama/otel-viewer/internal/store"
)

// ---- /api/datasources -------------------------------------------------------

func (s *Server) datasourcesGet(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"otlpReceiver": "/v1/traces",
		"tempoEnabled": true, // we always allow on-demand backfills
	})
}

type backfillReq struct {
	BaseURL      string `json:"baseUrl"`
	TraceQL      string `json:"traceql"`
	Start        int64  `json:"startNs"`
	End          int64  `json:"endNs"`
	Limit        int    `json:"limit"`
	AuthMode     string `json:"authMode"`
	Token        string `json:"token"`
	Username     string `json:"username"`
	Password     string `json:"password"`
	TenantHeader string `json:"tenantHeader"`
}

func (s *Server) tempoBackfillPost(w http.ResponseWriter, r *http.Request) {
	var req backfillReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "decode: "+err.Error())
		return
	}
	if req.BaseURL == "" {
		writeErr(w, http.StatusBadRequest, "baseUrl required")
		return
	}
	start, end := time.Unix(0, req.Start), time.Unix(0, req.End)
	id, err := s.BackfillRun.Run(r.Context(), tempopkg.BackfillRequest{
		BaseURL: req.BaseURL,
		Auth: tempopkg.Auth{
			Mode:         req.AuthMode,
			Token:        req.Token,
			Username:     req.Username,
			Password:     req.Password,
			TenantHeader: req.TenantHeader,
		},
		TraceQL: req.TraceQL,
		Start:   start,
		End:     end,
		Limit:   req.Limit,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"jobId": id})
}

func (s *Server) tempoBackfillList(w http.ResponseWriter, r *http.Request) {
	jobs, err := s.Store.ListBackfillJobs(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, jobs)
}

// ---- /api/traces ------------------------------------------------------------

func (s *Server) tracesList(w http.ResponseWriter, r *http.Request) {
	filter, err := analytics.ParseFilter([]byte(r.URL.Query().Get("filter")))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rf := store.RunFilter{
		Limit:  intParam(r, "limit", 100),
		Offset: intParam(r, "offset", 0),
	}
	if filter.Model != nil {
		rf.Models = strOpValues(filter.Model)
	}
	if filter.TaskID != nil {
		rf.TaskIDs = strOpValues(filter.TaskID)
	}
	if filter.Vendor != nil {
		rf.Vendors = strOpValues(filter.Vendor)
	}
	if filter.IngestSource != nil {
		rf.IngestSources = strOpValues(filter.IngestSource)
	}
	rf.ReplayGroup = filter.ReplayGroup
	if filter.Time != nil {
		rf.StartAfterNs = filter.Time.Gte
		rf.StartBeforeNs = filter.Time.Lte
	}
	if filter.FeedbackScore != nil && filter.FeedbackScore.Lte != nil {
		rf.FeedbackMaxScore = filter.FeedbackScore.Lte
	} else if filter.FeedbackScore != nil && filter.FeedbackScore.Lt != nil {
		// approximate < as <=; the UI only ships one of the two.
		rf.FeedbackMaxScore = filter.FeedbackScore.Lt
	}
	if filter.ToolName != nil {
		rf.ToolNameAny = strOpValues(filter.ToolName)
	}
	rf.ToolOutputLike = filter.ToolOutputContains

	runs, err := s.Store.ListRuns(r.Context(), rf)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) tracesGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	run, err := s.Store.GetRun(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "trace not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) tracesUpload(w http.ResponseWriter, r *http.Request) {
	// Accept multipart "file" or raw ndjson body.
	var reader io.Reader = r.Body
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			writeErr(w, http.StatusBadRequest, "missing file field")
			return
		}
		defer f.Close()
		reader = f
	}
	n, err := jsonlpkg.ParseAndIngest(r.Context(), s.Pipeline, reader)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"ingested": n})
}

// ---- /api/compare -----------------------------------------------------------

type compareReq struct {
	TraceIDs []string `json:"traceIds"`
}

type compareResp struct {
	Runs      []*model.Run `json:"runs"`
	Alignment []diff.Row   `json:"alignment"`
	Summary   compareSum   `json:"summary"`
}

type compareSum struct {
	LatencyMs   []int64   `json:"latencyMs"`
	TotalTokens []int64   `json:"totalTokens"`
	StepCount   []int     `json:"stepCount"`
	AvgScore    []float64 `json:"avgFeedbackScore"`
}

func (s *Server) comparePost(w http.ResponseWriter, r *http.Request) {
	var req compareReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(req.TraceIDs) < 2 || len(req.TraceIDs) > 4 {
		writeErr(w, http.StatusBadRequest, "compare requires 2..4 trace ids")
		return
	}
	runs := make([]*model.Run, 0, len(req.TraceIDs))
	for _, id := range req.TraceIDs {
		run, err := s.Store.GetRun(r.Context(), id)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "trace "+id+": "+err.Error())
			return
		}
		runs = append(runs, run)
	}
	seqs := make([][]model.AgentStep, len(runs))
	for i, run := range runs {
		seqs[i] = run.Steps
	}
	alignment := diff.AlignMany(seqs)
	sum := compareSum{
		LatencyMs:   make([]int64, len(runs)),
		TotalTokens: make([]int64, len(runs)),
		StepCount:   make([]int, len(runs)),
		AvgScore:    make([]float64, len(runs)),
	}
	for i, run := range runs {
		sum.LatencyMs[i] = run.EndTime.Sub(run.StartTime).Milliseconds()
		sum.TotalTokens[i] = run.Totals.TotalTokens
		sum.StepCount[i] = len(run.Steps)
		if len(run.Feedback) > 0 {
			total := 0.0
			for _, fb := range run.Feedback {
				total += fb.Score
			}
			sum.AvgScore[i] = total / float64(len(run.Feedback))
		}
	}
	writeJSON(w, http.StatusOK, compareResp{Runs: runs, Alignment: alignment, Summary: sum})
}

// ---- /api/tasks -------------------------------------------------------------

func (s *Server) tasksList(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.Store.ListTasks(r.Context(), intParam(r, "limit", 200))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (s *Server) taskGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, runs, err := s.Store.GetTask(r.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "task not found")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": t, "runs": runs})
}

type taskMemberReq struct {
	TraceID string `json:"traceId"`
}

func (s *Server) taskAddMember(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req taskMemberReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.AddManualTaskMember(r.Context(), id, req.TraceID); err != nil {
		if errors.Is(err, store.ErrUnknownTrace) {
			writeErr(w, http.StatusBadRequest, "unknown trace id")
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) taskRemoveMember(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	traceID := chi.URLParam(r, "traceId")
	if err := s.Store.RemoveManualTaskMember(r.Context(), id, traceID); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- /api/replay-groups -----------------------------------------------------

func (s *Server) replayGroupGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	runs, err := s.Store.GetReplayGroup(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"groupId": id, "runs": runs})
}

// ---- /api/analytics ---------------------------------------------------------

func (s *Server) analyticsFlow(w http.ResponseWriter, r *http.Request) {
	filter, err := analytics.ParseFilter([]byte(r.URL.Query().Get("filter")))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	g, err := analytics.Flow(r.Context(), s.Store, filter)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) analyticsFrequency(w http.ResponseWriter, r *http.Request) {
	filter, err := analytics.ParseFilter([]byte(r.URL.Query().Get("filter")))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	rows, err := analytics.Frequency(r.Context(), s.Store, filter)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows})
}

// ---- /api/feedback ----------------------------------------------------------

type feedbackReq struct {
	TraceID string  `json:"traceId"`
	SpanID  string  `json:"spanId,omitempty"`
	Source  string  `json:"source"`
	Score   float64 `json:"score"`
	Label   string  `json:"label,omitempty"`
	Comment string  `json:"comment,omitempty"`
}

func (s *Server) feedbackPost(w http.ResponseWriter, r *http.Request) {
	var req feedbackReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.TraceID == "" {
		writeErr(w, http.StatusBadRequest, "traceId required")
		return
	}
	fb := model.FeedbackEntry{
		TraceID:   req.TraceID,
		SpanID:    req.SpanID,
		Source:    req.Source,
		Score:     req.Score,
		Label:     req.Label,
		Comment:   req.Comment,
		CreatedAt: time.Now().UTC(),
	}
	if fb.Source == "" {
		fb.Source = "user"
	}
	if err := s.Store.InsertFeedback(r.Context(), &fb); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, fb)
}

func (s *Server) feedbackList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	traceID := q.Get("traceId")
	var maxScore *float64
	if v := q.Get("maxScore"); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err == nil {
			maxScore = &f
		}
	}
	limit := intParam(r, "limit", 200)
	list, err := s.Store.ListFeedback(r.Context(), traceID, maxScore, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"feedback": list})
}

// ---- helpers ----------------------------------------------------------------

func intParam(r *http.Request, name string, def int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func strOpValues(op *analytics.StringOp) []string {
	if op == nil {
		return nil
	}
	if op.Eq != "" {
		return []string{op.Eq}
	}
	return op.In
}
