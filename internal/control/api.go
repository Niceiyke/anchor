package control

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/oyomworld/anchor/internal/store"
	"github.com/oyomworld/anchor/pkg/protocol"
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
	if !s.limiter.allow(r) {
		http.Error(w, "too many attempts", http.StatusTooManyRequests)
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	// Try users table first, then fall back to bootstrap admin in settings.
	var authenticated bool
	var role string
	if u, err := s.store.GetUserByUsername(body.Username); err == nil {
		if checkPass(body.Password, u.PasswordHash) {
			authenticated = true
			role = u.Role
		}
	} else {
		settings, _ := s.store.Settings()
		if body.Username == settings.AdminUser && checkPass(body.Password, settings.AdminPass) {
			authenticated = true
			role = "admin"
			if isLegacyHash(settings.AdminPass) {
				settings.AdminPass = hashPass(body.Password)
				_ = s.store.SaveSettings(settings)
			}
		}
	}

	if !authenticated {
		s.limiter.record(r)
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	tok := s.auth.issueWithRole(body.Username, role)
	http.SetCookie(w, &http.Cookie{
		Name: "anchor_session", Value: tok, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil,
		Expires: time.Now().Add(7 * 24 * time.Hour),
	})
	csrf := s.auth.csrfToken()
	http.SetCookie(w, &http.Cookie{
		Name: "anchor_csrf", Value: csrf, Path: "/",
		SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil,
		Expires: time.Now().Add(7 * 24 * time.Hour),
	})
	writeJSON(w, http.StatusOK, map[string]string{"token": tok, "csrf_token": csrf, "role": role})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.auth.revoke(bearer(r))
	http.SetCookie(w, &http.Cookie{Name: "anchor_session", Value: "", Path: "/", MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	tok := bearer(r)
	parts := strings.SplitN(tok, ":", 3)
	username := ""
	role := ""
	if len(parts) == 3 {
		username = parts[0]
		role = parts[1]
	} else {
		settings, _ := s.store.Settings()
		username = settings.AdminUser
		role = "admin"
	}
	// Self-heal sessions established before CSRF was introduced: if the session
	// is valid but the double-submit cookie is missing, issue one so mutating
	// requests work without forcing a re-login.
	if _, err := r.Cookie("anchor_csrf"); err != nil {
		http.SetCookie(w, &http.Cookie{
			Name: "anchor_csrf", Value: s.auth.csrfToken(), Path: "/",
			SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil,
			Expires: time.Now().Add(7 * 24 * time.Hour),
		})
	}
	writeJSON(w, http.StatusOK, map[string]string{"username": username, "role": role})
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
	// Revoke the admin's other sessions (log out other devices); keep the
	// caller logged in via their current token.
	_ = s.store.DeleteSessionsForUser(settings.AdminUser, bearer(r))
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
	id := r.PathValue("id")
	// Cascade: remove databases and apps belonging to this server.
	for _, db := range s.mustListDatabases() {
		if db.ServerID == id {
			_ = s.store.DeleteDatabase(db.ID)
		}
	}
	for _, app := range s.mustListApps() {
		if app.ServerID == id {
			_ = s.store.DeleteApp(app.ID)
		}
	}
	_ = s.store.DeleteServer(id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) mustListDatabases() []store.Database {
	dbs, _ := s.store.ListDatabases()
	return dbs
}

func (s *Server) mustListApps() []store.App {
	apps, _ := s.store.ListApps()
	return apps
}

// ---- Apps ----

func (s *Server) handleListApps(w http.ResponseWriter, r *http.Request) {
	apps, _ := s.store.ListApps()
	for i := range apps {
		apps[i].ContainerName = protocol.Sanitize(apps[i].Name)
	}
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
	a.ComposeFile = strings.TrimSpace(a.ComposeFile)
	if !validComposePath(a.ComposeFile) {
		http.Error(w, "invalid compose file path", http.StatusBadRequest)
		return
	}
	// Auto-assign a subdomain under the base domain when none was provided.
	a.Domain = strings.TrimSpace(a.Domain)
	if a.Domain == "" {
		a.Domain = s.assignDomain(a.Name)
	}
	a.ID = "app_" + randToken()[:12]
	a.CreatedAt = time.Now()
	if err := s.store.CreateApp(a); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	go s.cfEnsureDomain(a.Domain) // best-effort Cloudflare A record
	a.ContainerName = protocol.Sanitize(a.Name)
	writeJSON(w, http.StatusCreated, a)
}

func (s *Server) handleGetApp(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetApp(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	a.ContainerName = protocol.Sanitize(a.Name)
	writeJSON(w, http.StatusOK, a)
}

// handleUpdateApp patches an app's deploy configuration. Only the fields
// present in the body are changed (all pointers). Identity fields (server,
// repo) are intentionally immutable — changing them would orphan the running
// container; recreate the app instead. Changes apply on the next deploy.
func (s *Server) handleUpdateApp(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetApp(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	var body struct {
		Branch        *string `json:"branch"`
		Domain        *string `json:"domain"`
		ContainerPort *int    `json:"container_port"`
		AutoDeploy    *bool   `json:"auto_deploy"`
		ComposeFile   *string `json:"compose_file"`
	}
	if err := readJSON(r, &body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if body.Branch != nil {
		if strings.TrimSpace(*body.Branch) == "" {
			http.Error(w, "branch cannot be empty", http.StatusBadRequest)
			return
		}
		a.Branch = strings.TrimSpace(*body.Branch)
	}
	oldDomain := a.Domain
	if body.Domain != nil {
		a.Domain = strings.TrimSpace(*body.Domain)
	}
	if body.ContainerPort != nil {
		if *body.ContainerPort <= 0 || *body.ContainerPort > 65535 {
			http.Error(w, "container_port out of range", http.StatusBadRequest)
			return
		}
		a.ContainerPort = *body.ContainerPort
	}
	if body.AutoDeploy != nil {
		a.AutoDeploy = *body.AutoDeploy
	}
	if body.ComposeFile != nil {
		cf := strings.TrimSpace(*body.ComposeFile)
		if !validComposePath(cf) {
			http.Error(w, "invalid compose file path", http.StatusBadRequest)
			return
		}
		a.ComposeFile = cf
	}
	if err := s.store.UpdateApp(a); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if a.Domain != oldDomain {
		if oldDomain != "" {
			go s.cfDeleteDomain(oldDomain)
		}
		go s.cfEnsureDomain(a.Domain)
	}
	a.ContainerName = protocol.Sanitize(a.Name)
	writeJSON(w, http.StatusOK, a)
}

func (s *Server) handleDeleteApp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Best-effort: stop/remove the running container(s) on the agent so we don't
	// orphan them, then delete the record.
	if a, err := s.store.GetApp(id); err == nil {
		payload, _ := json.Marshal(protocol.StopAppRequest{AppName: a.Name})
		cmd := protocol.Command{ID: randToken()[:12], Type: protocol.CmdStopApp, Data: payload}
		s.hub.Send(a.ServerID, cmd) // best-effort; agent may be offline
		go s.cfDeleteDomain(a.Domain)
	}
	_ = s.store.DeleteApp(id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeployApp(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetApp(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// Optional commit_sha lets the UI redeploy a specific past commit; empty
	// means deploy the latest of the app's branch.
	var body struct {
		CommitSHA string `json:"commit_sha"`
	}
	_ = readJSON(r, &body)
	dep, err := s.triggerDeploy(a, strings.TrimSpace(body.CommitSHA))
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error(), "deployment_id": dep.ID})
		return
	}
	writeJSON(w, http.StatusAccepted, dep)
}

func (s *Server) handleStopApp(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetApp(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	payload, _ := json.Marshal(protocol.StopAppRequest{AppName: a.Name})
	cmd := protocol.Command{ID: randToken()[:12], Type: protocol.CmdStopApp, Data: payload}
	if !s.hub.Send(a.ServerID, cmd) {
		http.Error(w, "agent offline", http.StatusConflict)
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleRollbackApp(w http.ResponseWriter, r *http.Request) {
	a, err := s.store.GetApp(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if a.LastGoodSHA == "" {
		http.Error(w, "no previous successful deployment to roll back to", http.StatusPreconditionFailed)
		return
	}
	dep, err := s.triggerDeploy(a, a.LastGoodSHA)
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
		"admin_user":               settings.AdminUser,
		"github_token_set":         settings.GitHubToken != "",
		"webhook_secret_set":       settings.WebhookSecret != "",
		"github_app_configured":    settings.GitHubAppConfigured(),
		"github_app_installed":     settings.GitHubInstallationID != 0,
		"github_app_slug":          settings.GitHubAppSlug,
		"notification_webhook_set": settings.NotificationWebhook != "",
		"base_domain":              settings.BaseDomain,
		"cloudflare_configured":    settings.CloudflareAPIToken != "",
		"public_ip":                settings.PublicIP,
	})
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		GitHubToken         *string `json:"github_token"`
		WebhookSecret       *string `json:"webhook_secret"`
		NotificationWebhook *string `json:"notification_webhook"`
		BaseDomain          *string `json:"base_domain"`
		CloudflareAPIToken  *string `json:"cloudflare_api_token"`
		PublicIP            *string `json:"public_ip"`
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
	if body.NotificationWebhook != nil {
		settings.NotificationWebhook = *body.NotificationWebhook
	}
	if body.BaseDomain != nil {
		settings.BaseDomain = normalizeBaseDomain(*body.BaseDomain)
	}
	if body.CloudflareAPIToken != nil {
		settings.CloudflareAPIToken = strings.TrimSpace(*body.CloudflareAPIToken)
	}
	if body.PublicIP != nil {
		settings.PublicIP = strings.TrimSpace(*body.PublicIP)
	}
	if err := s.store.SaveSettings(settings); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- Server stats ----

func (s *Server) handleServerStats(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("id")
	since := time.Now().Add(-24 * time.Hour)
	if q := r.URL.Query().Get("since"); q != "" {
		if t, err := time.Parse(time.RFC3339, q); err == nil {
			since = t
		}
	}
	limit := 200
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 5000 {
			limit = n
		}
	}
	stats, _ := s.store.ServerStats(serverID, since, limit)
	writeJSON(w, http.StatusOK, stats)
}

// ---- Users ----

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users, _ := s.store.ListUsers()
	for i := range users {
		users[i].PasswordHash = "" // never leak password hashes
	}
	if users == nil {
		users = []store.User{}
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := readJSON(r, &body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if body.Username == "" || body.Password == "" {
		http.Error(w, "username and password required", http.StatusBadRequest)
		return
	}
	if body.Role != "admin" && body.Role != "viewer" {
		body.Role = "viewer"
	}
	if _, err := s.store.GetUserByUsername(body.Username); err == nil {
		http.Error(w, "username already exists", http.StatusConflict)
		return
	}
	u := store.User{
		ID:           "usr_" + randToken()[:12],
		Username:     body.Username,
		PasswordHash: hashPass(body.Password),
		Role:         body.Role,
		CreatedAt:    time.Now(),
	}
	if err := s.store.CreateUser(u); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	u.PasswordHash = ""
	writeJSON(w, http.StatusCreated, u)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	settings, _ := s.store.Settings()
	if u, err := s.store.GetUser(id); err == nil && u.Username == settings.AdminUser {
		http.Error(w, "cannot delete the bootstrap admin user", http.StatusBadRequest)
		return
	}
	_ = s.store.DeleteUser(id)
	w.WriteHeader(http.StatusNoContent)
}
