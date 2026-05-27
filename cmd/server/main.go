// Command otel-viewer is a single-binary HTTP server that ingests OTel traces
// for LLM-agent runs and serves the embedded React UI.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kaitoyama/otel-viewer/internal/api"
	"github.com/kaitoyama/otel-viewer/internal/ingest"
	tempopkg "github.com/kaitoyama/otel-viewer/internal/ingest/tempo"
	"github.com/kaitoyama/otel-viewer/internal/store"
)

func main() {
	var (
		addr      = flag.String("addr", ":8080", "HTTP listen address")
		dbPath    = flag.String("db", "otel-viewer.duckdb", "DuckDB file path (use ':memory:' for ephemeral)")
		token     = flag.String("token", os.Getenv("OTEL_VIEWER_TOKEN"), "API bearer token; empty disables auth")
		devProxy  = flag.String("dev-proxy", "", "If set, proxy non-API requests to this URL (e.g., http://localhost:5173)")
	)
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	st, err := store.Open(ctx, *dbPath)
	if err != nil {
		logger.Error("open store", "err", err)
		os.Exit(1)
	}
	defer st.Close()

	pipe := &ingest.Pipeline{Store: st, Logger: logger}
	runner := &tempopkg.Runner{Store: st, Pipeline: pipe, Logger: logger}

	var spa http.Handler
	if *devProxy != "" {
		u, err := url.Parse(*devProxy)
		if err != nil {
			logger.Error("parse dev-proxy", "err", err)
			os.Exit(1)
		}
		spa = httputil.NewSingleHostReverseProxy(u)
		logger.Info("dev proxy enabled", "target", *devProxy)
	} else {
		spa = api.SPAHandler()
	}

	srv := &api.Server{
		Store:       st,
		Pipeline:    pipe,
		BackfillRun: runner,
		Logger:      logger,
		BearerToken: *token,
		SPA:         spa,
	}

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("listening", "addr", *addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	logger.Info("shutting down")
	shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutCancel()
	_ = httpSrv.Shutdown(shutCtx)
}
