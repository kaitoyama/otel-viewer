package tempo_test

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kaitoyama/otel-viewer/internal/ingest/tempo"
)

func TestClient_SearchAndGetTrace(t *testing.T) {
	traceID := hex.EncodeToString([]byte("0123456789abcdef"))
	var sawAuth, sawTenant string
	mux := http.NewServeMux()
	mux.HandleFunc("/api/search", func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		sawTenant = r.Header.Get("X-Scope-OrgID")
		assert.Equal(t, "{ .x = 1 }", r.URL.Query().Get("q"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"traces": []map[string]any{{"traceID": traceID}},
		})
	})
	mux.HandleFunc("/api/traces/", func(w http.ResponseWriter, r *http.Request) {
		assert.True(t, strings.HasSuffix(r.URL.Path, traceID))
		// Tempo returns {"batches":[{ResourceSpans...}]}
		payload := `{"batches":[{"resource":{"attributes":[]},"scopeSpans":[{"spans":[
			{"traceId":"` + traceID + `","spanId":"00112233","name":"chat","startTimeUnixNano":"1","endTimeUnixNano":"2","kind":1}
		]}]}]}`
		_, _ = w.Write([]byte(payload))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := tempo.New(srv.URL, tempo.Auth{Mode: "bearer", Token: "secret", TenantHeader: "tenant-1"})
	res, err := c.Search(context.Background(), "{ .x = 1 }", time.Unix(1000, 0), time.Unix(2000, 0), 50)
	require.NoError(t, err)
	require.Len(t, res.Traces, 1)
	assert.Equal(t, "Bearer secret", sawAuth)
	assert.Equal(t, "tenant-1", sawTenant)

	td, err := c.GetTrace(context.Background(), res.Traces[0].TraceID)
	require.NoError(t, err)
	require.NotNil(t, td)
	require.Len(t, td.ResourceSpans, 1)
	require.Len(t, td.ResourceSpans[0].ScopeSpans, 1)
	require.Len(t, td.ResourceSpans[0].ScopeSpans[0].Spans, 1)
}

func TestClient_GetTrace_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := tempo.New(srv.URL, tempo.Auth{Mode: "none"})
	_, err := c.GetTrace(context.Background(), "abc")
	assert.ErrorIs(t, err, tempo.ErrNotFound)
}
