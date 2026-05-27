package tempo

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/kaitoyama/otel-viewer/internal/ingest"
	"github.com/kaitoyama/otel-viewer/internal/store"
)

// BackfillRequest is what the API hands to the job runner.
type BackfillRequest struct {
	BaseURL string
	Auth    Auth
	TraceQL string
	Start   time.Time
	End     time.Time
	Limit   int
}

// Runner serially executes backfill jobs. v1 is single-worker; concurrent
// backfills are out of scope.
type Runner struct {
	Store    *store.Store
	Pipeline *ingest.Pipeline
	Logger   *slog.Logger
}

// Run kicks off a backfill, persisting status into tempo_backfill_jobs. The
// returned job id can be used to poll progress via store.ListBackfillJobs.
//
// We intentionally run synchronously inside the caller's context: small
// time-windows complete quickly, larger ones are explicitly chunked by users.
func (r *Runner) Run(ctx context.Context, req BackfillRequest) (string, error) {
	if req.BaseURL == "" {
		return "", errors.New("base url required")
	}
	if req.Limit <= 0 {
		req.Limit = 200
	}
	job := store.BackfillJob{
		BaseURL: req.BaseURL,
		TraceQL: req.TraceQL,
		StartNs: req.Start.UnixNano(),
		EndNs:   req.End.UnixNano(),
		Status:  "pending",
	}
	if err := r.Store.CreateBackfillJob(ctx, &job); err != nil {
		return "", err
	}

	client := New(req.BaseURL, req.Auth)
	if err := r.Store.UpdateBackfill(ctx, job.ID, 0, 0, "running", ""); err != nil {
		return job.ID, err
	}

	res, err := client.Search(ctx, req.TraceQL, req.Start, req.End, req.Limit)
	if err != nil {
		_ = r.Store.UpdateBackfill(ctx, job.ID, 0, 0, "failed", err.Error())
		return job.ID, err
	}
	total := len(res.Traces)
	fetched := 0
	for _, t := range res.Traces {
		td, err := client.GetTrace(ctx, t.TraceID)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				continue
			}
			r.Logger.Warn("backfill get-trace failed", "trace_id", t.TraceID, "err", err)
			continue
		}
		if _, err := r.Pipeline.Ingest(ctx, td, "tempo"); err != nil {
			r.Logger.Warn("backfill ingest failed", "trace_id", t.TraceID, "err", err)
			continue
		}
		fetched++
		if fetched%10 == 0 {
			_ = r.Store.UpdateBackfill(ctx, job.ID, fetched, total, "running", "")
		}
	}
	status := "done"
	errMsg := ""
	if fetched < total {
		errMsg = fmt.Sprintf("ingested %d of %d", fetched, total)
	}
	if err := r.Store.UpdateBackfill(ctx, job.ID, fetched, total, status, errMsg); err != nil {
		return job.ID, err
	}
	return job.ID, nil
}
