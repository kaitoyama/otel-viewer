package api

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

// The build pipeline writes the Vite output into internal/api/dist. When the
// directory is absent at compile time the embed package still emits the
// symbol — we just check for empty FS at runtime.
//
//go:embed all:dist
var spaFS embed.FS

// SPAHandler serves the embedded React SPA, falling back to /index.html for
// any path that's not a real asset. If the embedded FS is empty (developer
// build without `npm run build` first) we return nil so the caller can
// install a dev proxy instead.
func SPAHandler() http.Handler {
	sub, err := fs.Sub(spaFS, "dist")
	if err != nil {
		return nil
	}
	entries, err := fs.ReadDir(sub, ".")
	if err != nil || len(entries) == 0 {
		return nil
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			fileServer.ServeHTTP(w, r)
			return
		}
		if _, err := fs.Stat(sub, path); err == nil {
			fileServer.ServeHTTP(w, r)
			return
		}
		// SPA fallback.
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	})
}
