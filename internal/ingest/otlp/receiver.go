// Package otlp implements the OTLP/HTTP traces receiver
// (POST /v1/traces with protobuf or JSON body).
package otlp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/kaitoyama/otel-viewer/internal/ingest"
)

const maxBodyBytes = 64 << 20 // 64 MiB

// Handler returns the http.Handler that accepts OTLP traces.
func Handler(p *ingest.Pipeline) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
		if err != nil {
			http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
			return
		}
		td, err := decode(body, r.Header.Get("Content-Type"))
		if err != nil {
			http.Error(w, "decode: "+err.Error(), http.StatusBadRequest)
			return
		}
		n, err := p.Ingest(r.Context(), td, "otlp")
		if err != nil {
			http.Error(w, "ingest: "+err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ingested": n,
		})
	})
}

func decode(body []byte, contentType string) (*tracepb.TracesData, error) {
	ct := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	td := &tracepb.TracesData{}
	switch ct {
	case "application/x-protobuf", "application/protobuf", "":
		// Try protobuf first; if that fails, fall through to ExportTraceServiceRequest.
		if err := proto.Unmarshal(body, td); err == nil {
			return td, nil
		}
		// Some clients send the OTLP collector service request envelope. We
		// don't import the collector proto here to keep deps lean — peek at
		// the first byte to detect JSON ergonomically when content-type is wrong.
		if len(body) > 0 && (body[0] == '{' || body[0] == '[') {
			return decodeJSON(body)
		}
		return nil, errors.New("invalid protobuf")
	case "application/json":
		return decodeJSON(body)
	}
	return nil, errors.New("unsupported content-type: " + contentType)
}

func decodeJSON(body []byte) (*tracepb.TracesData, error) {
	td := &tracepb.TracesData{}
	if err := protojson.Unmarshal(body, td); err == nil {
		return td, nil
	}
	// Some exporters emit {"resourceSpans": [...]} only (matches TracesData
	// shape exactly), some emit the ExportTraceServiceRequest shape which is
	// structurally compatible because resourceSpans is the only field there
	// too. So a second attempt with permissive unmarshal:
	var aux struct {
		ResourceSpans json.RawMessage `json:"resourceSpans"`
	}
	if err := json.Unmarshal(body, &aux); err != nil {
		return nil, err
	}
	if len(aux.ResourceSpans) == 0 {
		return td, nil
	}
	wrapped := []byte(`{"resourceSpans":` + string(aux.ResourceSpans) + `}`)
	if err := protojson.Unmarshal(wrapped, td); err != nil {
		return nil, err
	}
	return td, nil
}
