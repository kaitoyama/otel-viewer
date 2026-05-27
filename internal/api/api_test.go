package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kaitoyama/otel-viewer/internal/api"
	"github.com/kaitoyama/otel-viewer/internal/ingest"
	tempopkg "github.com/kaitoyama/otel-viewer/internal/ingest/tempo"
	"github.com/kaitoyama/otel-viewer/internal/store"
)

func newServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), t.TempDir()+"/api.duckdb")
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pipe := &ingest.Pipeline{Store: st, Logger: logger}
	srv := &api.Server{
		Store:       st,
		Pipeline:    pipe,
		BackfillRun: &tempopkg.Runner{Store: st, Pipeline: pipe, Logger: logger},
		Logger:      logger,
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, st
}

// sampleOTLPJSON returns an OTLP-JSON payload with a single GenAI chat trace.
func sampleOTLPJSON(traceID string) []byte {
	return []byte(`{"resourceSpans":[{
	  "resource":{"attributes":[{"key":"service.name","value":{"stringValue":"agent"}}]},
	  "scopeSpans":[{"scope":{"name":"openai"},"spans":[
	    {
	      "traceId":"` + traceID + `",
	      "spanId":"0123456789abcdef",
	      "name":"chat claude-opus-4-7",
	      "startTimeUnixNano":"1000000000",
	      "endTimeUnixNano":"2000000000",
	      "kind":1,
	      "attributes":[
	        {"key":"gen_ai.system","value":{"stringValue":"anthropic"}},
	        {"key":"gen_ai.request.model","value":{"stringValue":"claude-opus-4-7"}},
	        {"key":"gen_ai.response.model","value":{"stringValue":"claude-opus-4-7"}},
	        {"key":"gen_ai.operation.name","value":{"stringValue":"chat"}},
	        {"key":"gen_ai.usage.input_tokens","value":{"intValue":"100"}},
	        {"key":"gen_ai.usage.output_tokens","value":{"intValue":"30"}},
	        {"key":"gen_ai.task.id","value":{"stringValue":"task-X"}}
	      ],
	      "events":[
	        {"name":"gen_ai.user.message","timeUnixNano":"1100000000",
	         "attributes":[{"key":"content","value":{"stringValue":"hello"}}]},
	        {"name":"gen_ai.choice","timeUnixNano":"1900000000",
	         "attributes":[{"key":"content","value":{"stringValue":"hi"}}]}
	      ]
	    }
	  ]}]
	}]}`)
}

func TestEndToEnd_OTLPIngest_ListGetCompareFeedback(t *testing.T) {
	ts, _ := newServer(t)

	// Push two traces via OTLP/HTTP.
	for _, tid := range []string{
		"00112233445566778899aabbccddeeff",
		"112233445566778899aabbccddeeff00",
	} {
		resp, err := http.Post(ts.URL+"/v1/traces", "application/json",
			bytes.NewReader(sampleOTLPJSON(tid)))
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode, mustReadBody(t, resp))
		resp.Body.Close()
	}

	// /api/traces
	resp, err := http.Get(ts.URL + "/api/traces")
	require.NoError(t, err)
	var listRes struct {
		Runs []map[string]any `json:"runs"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&listRes))
	resp.Body.Close()
	assert.Len(t, listRes.Runs, 2)

	// /api/traces/:id
	resp, err = http.Get(ts.URL + "/api/traces/" + listRes.Runs[0]["traceId"].(string))
	require.NoError(t, err)
	var run map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&run))
	resp.Body.Close()
	assert.Equal(t, "claude-opus-4-7", run["model"])
	assert.NotEmpty(t, run["steps"])

	// /api/feedback (POST then GET)
	fbPayload := `{"traceId":"` + listRes.Runs[0]["traceId"].(string) + `","source":"user","score":0.1,"label":"bad"}`
	resp, err = http.Post(ts.URL+"/api/feedback", "application/json", strings.NewReader(fbPayload))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, mustReadBody(t, resp))
	resp.Body.Close()

	resp, err = http.Get(ts.URL + "/api/feedback?maxScore=0.5")
	require.NoError(t, err)
	var fbList struct {
		Feedback []map[string]any `json:"feedback"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&fbList))
	resp.Body.Close()
	assert.Len(t, fbList.Feedback, 1)

	// /api/compare
	compareBody := `{"traceIds":["` + listRes.Runs[0]["traceId"].(string) + `","` + listRes.Runs[1]["traceId"].(string) + `"]}`
	resp, err = http.Post(ts.URL+"/api/compare", "application/json", strings.NewReader(compareBody))
	require.NoError(t, err)
	cmpBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, string(cmpBody))
	var cmpResp map[string]any
	require.NoError(t, json.Unmarshal(cmpBody, &cmpResp), string(cmpBody))
	assert.NotEmpty(t, cmpResp["alignment"])
	assert.NotNil(t, cmpResp["summary"])

	// /api/tasks
	resp, err = http.Get(ts.URL + "/api/tasks")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	var tasksRes struct {
		Tasks []map[string]any `json:"tasks"`
	}
	require.NoError(t, json.Unmarshal(body, &tasksRes), string(body))
	require.NotEmpty(t, tasksRes.Tasks)
	assert.Equal(t, "task-X", tasksRes.Tasks[0]["id"])

	// /api/analytics/frequency
	resp, err = http.Get(ts.URL + "/api/analytics/frequency")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, mustReadBody(t, resp))
	resp.Body.Close()

	// /api/analytics/flow
	resp, err = http.Get(ts.URL + "/api/analytics/flow")
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, mustReadBody(t, resp))
	resp.Body.Close()
}

func TestUnauthorized_WhenTokenSet(t *testing.T) {
	st, err := store.Open(context.Background(), t.TempDir()+"/api.duckdb")
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	pipe := &ingest.Pipeline{Store: st, Logger: logger}
	srv := &api.Server{
		Store:       st,
		Pipeline:    pipe,
		BackfillRun: &tempopkg.Runner{Store: st, Pipeline: pipe, Logger: logger},
		Logger:      logger,
		BearerToken: "secret",
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/api/traces")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/traces", nil)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err = http.DefaultClient.Do(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func mustReadBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
