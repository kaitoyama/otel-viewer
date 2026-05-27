// Package tempo is a small client for Grafana Tempo's GA HTTP APIs:
//
//   GET /api/search?q=<TraceQL>&start=<unix-s>&end=<unix-s>&limit=<n>
//   GET /api/traces/{traceID}
//
// We deliberately do not use TraceQL Metrics, metrics-generator, compare(), or
// any other preview/experimental endpoint. Tempo's role in this project is
// strictly to ship historical traces into our DuckDB store.
package tempo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// Auth is how we authenticate to Tempo.
type Auth struct {
	Mode         string // "none" | "bearer" | "basic"
	Token        string // when Mode == bearer
	Username     string // when Mode == basic
	Password     string // when Mode == basic
	TenantHeader string // X-Scope-OrgID, optional
}

// Client talks to a single Tempo endpoint.
type Client struct {
	BaseURL string
	Auth    Auth
	HTTP    *http.Client
}

// New returns a Client with sensible HTTP defaults.
func New(baseURL string, auth Auth) *Client {
	return &Client{
		BaseURL: baseURL,
		Auth:    auth,
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) applyAuth(req *http.Request) {
	switch c.Auth.Mode {
	case "bearer":
		req.Header.Set("Authorization", "Bearer "+c.Auth.Token)
	case "basic":
		req.SetBasicAuth(c.Auth.Username, c.Auth.Password)
	}
	if c.Auth.TenantHeader != "" {
		req.Header.Set("X-Scope-OrgID", c.Auth.TenantHeader)
	}
}

// SearchResult is a subset of Tempo's /api/search response. Tempo's full
// schema is broader, but only TraceID is required for the next step (pulling
// each trace individually).
type SearchResult struct {
	Traces []struct {
		TraceID           string `json:"traceID"`
		RootServiceName   string `json:"rootServiceName,omitempty"`
		RootTraceName     string `json:"rootTraceName,omitempty"`
		StartTimeUnixNano string `json:"startTimeUnixNano,omitempty"`
		DurationMs        int    `json:"durationMs,omitempty"`
	} `json:"traces"`
	Metrics map[string]any `json:"metrics,omitempty"`
}

// Search runs a TraceQL query over a time window. start/end are inclusive
// unix seconds. Returns up to limit trace ids.
func (c *Client) Search(ctx context.Context, traceql string, start, end time.Time, limit int) (*SearchResult, error) {
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, err
	}
	u.Path = singleSlash(u.Path) + "api/search"
	q := u.Query()
	if traceql != "" {
		q.Set("q", traceql)
	}
	if !start.IsZero() {
		q.Set("start", strconv.FormatInt(start.Unix(), 10))
	}
	if !end.IsZero() {
		q.Set("end", strconv.FormatInt(end.Unix(), 10))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	c.applyAuth(req)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("tempo search %d: %s", resp.StatusCode, string(b))
	}
	var out SearchResult
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode search: %w", err)
	}
	return &out, nil
}

// GetTrace fetches a single trace by id and decodes it into the OTLP proto
// model. Tempo returns OTLP-JSON wrapping {"batches":[ResourceSpans...]}.
func (c *Client) GetTrace(ctx context.Context, traceID string) (*tracepb.TracesData, error) {
	if traceID == "" {
		return nil, errors.New("empty traceID")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, err
	}
	u.Path = singleSlash(u.Path) + "api/traces/" + traceID

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	c.applyAuth(req)
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errTraceNotFound
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("tempo get-trace %d: %s", resp.StatusCode, string(b))
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	// Tempo returns {"batches": [...]} where batches are ResourceSpans. We
	// rewrap into a TracesData since both fields are essentially equivalent.
	var aux struct {
		Batches       json.RawMessage `json:"batches"`
		ResourceSpans json.RawMessage `json:"resourceSpans"`
	}
	if err := json.Unmarshal(body, &aux); err != nil {
		return nil, fmt.Errorf("decode batches: %w", err)
	}
	rs := aux.ResourceSpans
	if len(rs) == 0 {
		rs = aux.Batches
	}
	if len(rs) == 0 {
		return &tracepb.TracesData{}, nil
	}
	wrapped := []byte(`{"resourceSpans":` + string(rs) + `}`)
	td := &tracepb.TracesData{}
	if err := protojson.Unmarshal(wrapped, td); err != nil {
		return nil, fmt.Errorf("protojson: %w", err)
	}
	return td, nil
}

var errTraceNotFound = errors.New("trace not found")

// ErrNotFound exposes the sentinel for callers.
var ErrNotFound = errTraceNotFound

func singleSlash(p string) string {
	if p == "" {
		return "/"
	}
	if p[len(p)-1] != '/' {
		return p + "/"
	}
	return p
}
