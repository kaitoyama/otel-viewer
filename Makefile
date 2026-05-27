SHELL := /bin/bash
.PHONY: help dev build test test-go test-web fmt vet lint clean

help:
	@echo "Targets:"
	@echo "  dev         — Go server + Vite dev server (proxied)"
	@echo "  build       — build SPA, then embed in Go binary -> bin/otel-viewer"
	@echo "  test        — Go and frontend tests"
	@echo "  test-go     — Go tests only"
	@echo "  test-web    — Vitest only"
	@echo "  fmt vet     — gofmt / go vet"
	@echo "  clean       — bin/, dist/, node_modules/"

dev:
	@echo "Starting backend on :8080 (dev-proxy -> :5173)"
	@echo "Starting Vite on :5173"
	@trap 'kill 0' EXIT; \
	  go run ./cmd/server -addr :8080 -dev-proxy http://localhost:5173 & \
	  (cd web && npm run dev) & \
	  wait

build:
	cd web && npm ci --silent && npm run build
	go build -o bin/otel-viewer ./cmd/server

test: test-go test-web

test-go:
	go test ./... -race

test-web:
	cd web && npm test --silent

fmt:
	gofmt -w .

vet:
	go vet ./...

clean:
	rm -rf bin/ internal/api/dist/assets internal/api/dist/index.html web/node_modules web/dist
