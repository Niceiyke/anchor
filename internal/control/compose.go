package control

import (
	"encoding/json"
	"net/http"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// validComposePath reports whether p is a safe, repo-relative compose file path.
// Empty means "auto-detect". Absolute paths and parent-directory traversal are
// rejected so a deploy can't reference files outside the cloned repo.
func validComposePath(p string) bool {
	if p == "" {
		return true
	}
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(p))
	return clean != ".." && !strings.HasPrefix(clean, "../")
}

// isComposeFile reports whether a repo path looks like a Compose file:
// docker-compose*.{yml,yaml} or compose*.{yml,yaml}, in any directory.
func isComposeFile(p string) bool {
	base := strings.ToLower(path.Base(p))
	if !strings.HasSuffix(base, ".yml") && !strings.HasSuffix(base, ".yaml") {
		return false
	}
	return strings.HasPrefix(base, "docker-compose") || strings.HasPrefix(base, "compose")
}

// handleListComposeFiles discovers Compose files in a repo via the GitHub git
// trees API so the UI can offer a picker. Requires a configured GitHub App or
// token; returns 412 otherwise (the UI then falls back to a free-text field).
func (s *Server) handleListComposeFiles(w http.ResponseWriter, r *http.Request) {
	repo := strings.TrimSpace(r.URL.Query().Get("repo")) // owner/name
	ref := strings.TrimSpace(r.URL.Query().Get("branch"))
	if repo == "" || !strings.Contains(repo, "/") {
		http.Error(w, "repo (owner/name) required", http.StatusBadRequest)
		return
	}
	if ref == "" {
		ref = "HEAD"
	}

	tok := s.githubCloneToken()
	if tok == "" {
		http.Error(w, "no github app or token configured", http.StatusPreconditionRequired)
		return
	}

	url := "https://api.github.com/repos/" + repo + "/git/trees/" + ref + "?recursive=1"
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "github: unable to read repo tree", http.StatusBadGateway)
		return
	}

	var body struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	files := []string{}
	for _, e := range body.Tree {
		if e.Type == "blob" && isComposeFile(e.Path) {
			files = append(files, e.Path)
		}
	}
	// Surface root-level files first, then shallower paths, then alphabetical.
	sort.Slice(files, func(i, j int) bool {
		di, dj := strings.Count(files[i], "/"), strings.Count(files[j], "/")
		if di != dj {
			return di < dj
		}
		return files[i] < files[j]
	})
	writeJSON(w, http.StatusOK, files)
}
