package control

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// ServeSPA mounts the built web UI at "/" with single-page-app fallback.
// More specific routes (/api, /agent, /webhooks, /healthz) already take
// precedence in the ServeMux, so this only handles UI paths.
//
// Call after New(); it's a no-op if dir is empty or missing.
func (s *Server) ServeSPA(dir string) {
	if dir == "" {
		return
	}
	if _, err := os.Stat(dir); err != nil {
		return
	}
	fs := http.FileServer(http.Dir(dir))
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Serve the file if it exists, otherwise fall back to index.html so
		// client-side routes (/apps/...) resolve.
		clean := filepath.Clean(r.URL.Path)
		if clean != "/" && !strings.Contains(filepath.Base(clean), ".") {
			if _, err := os.Stat(filepath.Join(dir, clean)); err != nil {
				r.URL.Path = "/"
			}
		}
		fs.ServeHTTP(w, r)
	})
}
