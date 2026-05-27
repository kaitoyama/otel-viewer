// Package api wires the HTTP surface: REST endpoints under /api, the OTLP
// receiver under /v1/traces and the embedded React SPA at /.
package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/kaitoyama/otel-viewer/internal/ingest"
	otlpr "github.com/kaitoyama/otel-viewer/internal/ingest/otlp"
	tempopkg "github.com/kaitoyama/otel-viewer/internal/ingest/tempo"
	"github.com/kaitoyama/otel-viewer/internal/store"
)

// Server wraps every collaborator the HTTP layer needs.
type Server struct {
	Store        *store.Store
	Pipeline     *ingest.Pipeline
	BackfillRun  *tempopkg.Runner
	Logger       *slog.Logger
	BearerToken  string // optional; empty disables auth
	SPA          http.Handler
}

// Handler builds the top-level mux.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(5 * time.Minute))
	r.Use(s.logRequests)

	r.Get("/healthz", okHandler)

	r.Route("/api", func(api chi.Router) {
		api.Use(s.requireBearer)
		api.Get("/healthz", okHandler)
		api.Get("/datasources", s.datasourcesGet)
		api.Post("/datasources/tempo/backfill", s.tempoBackfillPost)
		api.Get("/datasources/tempo/backfill", s.tempoBackfillList)

		api.Get("/traces", s.tracesList)
		api.Get("/traces/{id}", s.tracesGet)
		api.Post("/traces/upload", s.tracesUpload)

		api.Post("/compare", s.comparePost)

		api.Get("/tasks", s.tasksList)
		api.Get("/tasks/{id}", s.taskGet)
		api.Post("/tasks/{id}/members", s.taskAddMember)
		api.Delete("/tasks/{id}/members/{traceId}", s.taskRemoveMember)

		api.Get("/replay-groups/{id}", s.replayGroupGet)

		api.Get("/analytics/flow", s.analyticsFlow)
		api.Get("/analytics/frequency", s.analyticsFrequency)

		api.Post("/feedback", s.feedbackPost)
		api.Get("/feedback", s.feedbackList)
	})

	r.Route("/v1", func(o chi.Router) {
		// OTLP receivers don't require the same UI bearer; if a separate token
		// is desired in future, gate here.
		o.Method(http.MethodPost, "/traces", otlpr.Handler(s.Pipeline))
	})

	// SPA at the root. The fallback returns index.html for anything that
	// doesn't match a static asset, so client-side routing works.
	if s.SPA != nil {
		r.Mount("/", s.SPA)
	}

	return r
}

func okHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func (s *Server) requireBearer(next http.Handler) http.Handler {
	if s.BearerToken == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := r.Header.Get("Authorization")
		if got != "Bearer "+s.BearerToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)
		s.Logger.Info("http",
			"method", r.Method, "path", r.URL.Path,
			"status", ww.Status(),
			"bytes", ww.BytesWritten(),
			"elapsed_ms", time.Since(start).Milliseconds(),
		)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}
