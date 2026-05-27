// Package ingest groups span batches by trace and routes them through
// normalize → task.Apply → store.UpsertRun.
package ingest

import (
	"context"
	"encoding/hex"
	"fmt"
	"log/slog"

	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/kaitoyama/otel-viewer/internal/normalize"
	"github.com/kaitoyama/otel-viewer/internal/store"
	"github.com/kaitoyama/otel-viewer/internal/task"
)

// Pipeline is the shared post-normalize sink used by every ingest source.
type Pipeline struct {
	Store  *store.Store
	Logger *slog.Logger
}

// Ingest normalizes a TracesData payload, splits it per trace_id and writes
// each as a Run. Source identifies the ingest channel ("otlp" | "jsonl" |
// "tempo") and is recorded on the run for later filtering.
func (p *Pipeline) Ingest(ctx context.Context, td *tracepb.TracesData, source string) (int, error) {
	if td == nil {
		return 0, nil
	}
	byTrace := map[string][]*tracepb.ResourceSpans{}
	for _, rs := range td.ResourceSpans {
		if rs == nil {
			continue
		}
		// A single ResourceSpans may contain spans from multiple traces; we
		// split per-trace by re-grouping inside ScopeSpans.
		groups := splitResourceSpansByTrace(rs)
		for tid, g := range groups {
			byTrace[tid] = append(byTrace[tid], g)
		}
	}
	written := 0
	for tid, rss := range byTrace {
		res := normalize.Normalize(rss, source)
		if res == nil {
			continue
		}
		task.Apply(&res.Run)
		if err := p.Store.UpsertRun(ctx, &res.Run, res.Events); err != nil {
			return written, fmt.Errorf("upsert run %s: %w", tid, err)
		}
		for i := range res.FeedbackEvents {
			fb := res.FeedbackEvents[i]
			fb.TraceID = res.Run.ID
			if err := p.Store.InsertFeedback(ctx, &fb); err != nil {
				p.Logger.Warn("insert feedback failed", "trace_id", tid, "err", err)
			}
		}
		written++
	}
	return written, nil
}

func splitResourceSpansByTrace(rs *tracepb.ResourceSpans) map[string]*tracepb.ResourceSpans {
	out := map[string]*tracepb.ResourceSpans{}
	for _, ss := range rs.ScopeSpans {
		bySpan := map[string][]*tracepb.Span{}
		for _, sp := range ss.Spans {
			tid := hex.EncodeToString(sp.TraceId)
			bySpan[tid] = append(bySpan[tid], sp)
		}
		for tid, spans := range bySpan {
			target, ok := out[tid]
			if !ok {
				target = &tracepb.ResourceSpans{
					Resource:  rs.Resource,
					SchemaUrl: rs.SchemaUrl,
				}
				out[tid] = target
			}
			target.ScopeSpans = append(target.ScopeSpans, &tracepb.ScopeSpans{
				Scope:     ss.Scope,
				Spans:     spans,
				SchemaUrl: ss.SchemaUrl,
			})
		}
	}
	return out
}
