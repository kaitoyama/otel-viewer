// Package jsonl ingests OTLP-JSON line-delimited files, the format produced
// by the OTel Collector file exporter. Each non-empty line must be a
// TracesData object (or ExportTraceServiceRequest with resourceSpans).
package jsonl

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"google.golang.org/protobuf/encoding/protojson"

	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/kaitoyama/otel-viewer/internal/ingest"
)

// ParseAndIngest reads JSONL from r and runs each line through the pipeline.
// Lines that fail to decode are logged but do not abort the import.
func ParseAndIngest(ctx context.Context, p *ingest.Pipeline, r io.Reader) (int, error) {
	scanner := bufio.NewScanner(r)
	// Larger buffer; agent traces can have huge attribute payloads.
	scanner.Buffer(make([]byte, 0, 1<<20), 64<<20)
	written := 0
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		td, err := decodeLine(line)
		if err != nil {
			p.Logger.Warn("jsonl decode failed", "err", err)
			continue
		}
		n, err := p.Ingest(ctx, td, "jsonl")
		if err != nil {
			return written, fmt.Errorf("ingest: %w", err)
		}
		written += n
	}
	if err := scanner.Err(); err != nil {
		return written, err
	}
	return written, nil
}

func decodeLine(line []byte) (*tracepb.TracesData, error) {
	td := &tracepb.TracesData{}
	if err := protojson.Unmarshal(line, td); err == nil && len(td.ResourceSpans) > 0 {
		return td, nil
	}
	// Allow the ExportTraceServiceRequest shape transparently.
	var aux struct {
		ResourceSpans json.RawMessage `json:"resourceSpans"`
	}
	if err := json.Unmarshal(line, &aux); err != nil {
		return nil, err
	}
	if len(aux.ResourceSpans) == 0 {
		return nil, errors.New("no resourceSpans field")
	}
	wrapped := []byte(`{"resourceSpans":` + string(aux.ResourceSpans) + `}`)
	if err := protojson.Unmarshal(wrapped, td); err != nil {
		return nil, err
	}
	return td, nil
}
