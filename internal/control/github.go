package control

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// handleGitHubRepos lists repos the configured token can access. MVP uses a
// personal access token (stored in settings). Swap for a GitHub App later.
func (s *Server) handleGitHubRepos(w http.ResponseWriter, r *http.Request) {
	settings, _ := s.store.Settings()
	if settings.GitHubToken == "" {
		http.Error(w, "no github token configured", http.StatusPreconditionRequired)
		return
	}
	req, _ := http.NewRequest("GET", "https://api.github.com/user/repos?per_page=100&sort=updated", nil)
	req.Header.Set("Authorization", "Bearer "+settings.GitHubToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		http.Error(w, "github: "+string(b), http.StatusBadGateway)
		return
	}
	var repos []struct {
		FullName      string `json:"full_name"`
		CloneURL      string `json:"clone_url"`
		DefaultBranch string `json:"default_branch"`
		Private       bool   `json:"private"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, repos)
}

// handleGitHubWebhook receives push events and triggers auto-deploys for any
// app bound to that repo with auto_deploy enabled.
func (s *Server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 5<<20))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}
	settings, _ := s.store.Settings()
	if settings.WebhookSecret != "" {
		if !validSignature(settings.WebhookSecret, r.Header.Get("X-Hub-Signature-256"), body) {
			http.Error(w, "invalid signature", http.StatusUnauthorized)
			return
		}
	}
	if r.Header.Get("X-GitHub-Event") != "push" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var payload struct {
		Ref   string `json:"ref"` // refs/heads/<branch>
		After string `json:"after"`
		Repo  struct {
			FullName string `json:"full_name"`
		} `json:"repository"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "bad payload", http.StatusBadRequest)
		return
	}
	branch := strings.TrimPrefix(payload.Ref, "refs/heads/")

	apps, _ := s.store.AppsByRepo(payload.Repo.FullName)
	triggered := 0
	for _, a := range apps {
		if a.AutoDeploy && a.Branch == branch {
			if _, err := s.triggerDeploy(a, payload.After); err == nil {
				triggered++
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"triggered": triggered})
}

func validSignature(secret, header string, body []byte) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(header, prefix) {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(strings.TrimPrefix(header, prefix)))
}
