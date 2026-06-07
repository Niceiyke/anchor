package control

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/oyomworld/anchor/internal/store"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

// ---- Auth ----

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	settings, _ := s.store.Settings()
	if body.Username != settings.AdminUser || !checkPass(body.Password, settings.AdminPass) {
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}
	// Transparently upgrade a legacy sha256 hash to bcrypt on successful login.
	if isLegacyHash(settings.AdminPass) {
		settings.AdminPass = hashPass(body.Password)
		_ = s.store.SaveSettings(settings)
	}
	tok := s.auth.issue()
	http.SetCookie(w, &http.Cookie{
		Name: "anchor_session", Value: tok, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Expires: time.Now().Add(24 * time.Hour),
	})
	writeJSON(w, http.StatusOK, map[string]string{"token": tok})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.auth.revoke(bearer(r))
	http.SetCookie(w, &http.Cookie{Name: "anchor_session", Value: "", Path: "/", MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	settings, _ := s.store.Settings()
	writeJSON(w, http.StatusOK, map[string]string{"username": settings.AdminUser})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := readJSON(r, &body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if len(body.NewPassword) < 8 {
		http.Error(w, "new password must be at least 8 characters", http.StatusBadRequest)
		return
	}
	settings, _ := s.store.Settings()
	if !checkPass(body.CurrentPassword, settings.AdminPass) {
		http.Error(w, "current password is incorrect", http.StatusUnauthorized)
		return
	}
	settings.AdminPass = hashPass(body.NewPassword)
	if err := s.store.SaveSettings(settings); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Invalidate all other sessions; keep the caller logged in by re-issuing.
	_ = s.store.DeleteExpiredSessions(time.Now())
	w.WriteHeader(http.StatusNoContent)
}

// ---- Servers ----

func (s *Server) handleListServers(w http.ResponseWriter, r *http.Request) {
	servers, _ := s.store.ListServers()
	// reflect live connection state from the hub
	for i := range servers {
		servers[i].Online = s.hub.Online(servers[i].ID)
		servers[i].AgentToken = "" // never leak tokens in list
	}
	writeJSON(w, http.StatusOK, servers)
}

func (s *Server) handleCreateServer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &body); err != nil || strings.TrimSpace(body.Name) == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	srv := store.Server{
		ID:         "srv_" + randToken()[:12],
		Name:       body.Name,
		AgentToken: randToken(),
		CreatedAt:  time.Now(),
	}
	if err := s.store.CreateServer(srv); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Return the token ONCE so the user can configure the agent.
	writeJSON(w, http.StatusCreated, srv)
}

func (s *Server) handleDeleteServer(w http.ResponseWriter, r *http.Request) {
	_ = s.store.DeleteServer(r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

// ---- Apps ----

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	apps, _ := s.store.ListApps()
	writeJSON(w, http.StatusOK, apps)
}

func (s *Server) handleCreateApp(w http.ResponseWriter, r *http.Request) {
	var a store.App
	if err := readJSON(r, &a); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if a.Name == "" || a.ServerID == "" || a.RepoURL == "" {
		http.Error(w, "name, server_id, repo_url required", http.StatusBadRequest)
		return
	}
	if a.Branch == "" {
		a.Branch = "main"
	}
	if a.ContainerPort == 0 {
		a.ContainerPort = 3000
	}
	if a.EnvVars == nil {
		a.EnvVars = map[string]string{}
	}
	a.ID = "app_" + randToken()[:12]
	a.CreatedAt = time.Now()
	if err := s.store.CreateApp(a); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) handleGetApp(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetApp(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	_ = s.store.DeleteApp(r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeployApp(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetApp(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	dep, err := s.triggerDeploy(a, "")
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "deployment_id": dep.ID})
		return
	}
	writeJSON(w, http.StatusAccepted, dep)
}

// ---- Deployments ----

func (s *Server) handleListDeployments(w http.ResponseWriter, r *http.Request) {
	deps, _ := s.store.ListDeployments(r.PathValue("id"))
	for i := range deps {
		deps[i].Logs = nil // omit logs from list view
	}
	writeJSON(w, http.StatusOK, deps)
}

func (s *Server) handleGetDeployment(w http.ResponseWriter, r *http.Request) {
	dep, err := s.store.GetDeployment(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, dep)
}

// ---- Settings ----

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, _ := s.store.Settings()
	writeJSON(w, http.StatusOK, map[string]any{
		"admin_user":            settings.AdminUser,
		"github_token_set":      settings.GitHubToken != "",
		"webhook_secret_set":    settings.WebhookSecret != "",
		"github_app_configured": settings.GitHubAppConfigured(),
		"github_app_installed":  settings.GitHubInstallationID != 0,
		"github_app_slug":       settings.GitHubAppSlug,
	})
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		GitHubToken   *string `json:"github_token"`
		WebhookSecret *string `json:"webhook_secret"`
	}
	if err := readJSON(r, &body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	settings, _ := s.store.Settings()
	if body.GitHubToken != nil {
		settings.GitHubToken = *body.GitHubToken
	}
	if body.WebhookSecret != nil {
		settings.WebhookSecret = *body.WebhookSecret
	}
	if err := s.store.SaveSettings(settings); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
