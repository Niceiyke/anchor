package control

import (
	"net/http"
	"strings"
)

// defaultDBEnvVar is the conventional env var name for each engine's URL.
var defaultDBEnvVar = map[string]string{
	"postgres": "DATABASE_URL",
	"redis":    "REDIS_URL",
}

// handleAttachDB injects a database's connection string into an app's env vars.
// The database should live on the same server as the app (they reach each other
// over anchor_net); we warn but don't hard-block otherwise.
func (s *Server) handleAttachDB(w http.ResponseWriter, r *http.Request) {
	app, err := s.store.GetApp(r.PathValue("id"))
	if err != nil {
		http.Error(w, "app not found", http.StatusNotFound)
		return
	}
	var body struct {
		DatabaseID string `json:"database_id"`
		VarName    string `json:"var_name"`
	}
	if err := readJSON(r, &body); err != nil || body.DatabaseID == "" {
		http.Error(w, "database_id required", http.StatusBadRequest)
		return
	}
	db, err := s.store.GetDatabase(body.DatabaseID)
	if err != nil {
		http.Error(w, "database not found", http.StatusBadRequest)
		return
	}

	varName := strings.TrimSpace(body.VarName)
	if varName == "" {
		varName = defaultDBEnvVar[db.Engine]
		if varName == "" {
			varName = "DATABASE_URL"
		}
	}

	if app.EnvVars == nil {
		app.EnvVars = map[string]string{}
	}
	app.EnvVars[varName] = db.ConnURI
	if err := s.store.UpdateApp(app); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"app":            app,
		"attached_var":   varName,
		"same_server":    db.ServerID == app.ServerID,
		"needs_redeploy": true,
	})
}

// handleSetEnv sets (or overwrites) a single env var on an app.
func (s *Server) handleSetEnv(w http.ResponseWriter, r *http.Request) {
	app, err := s.store.GetApp(r.PathValue("id"))
	if err != nil {
		http.Error(w, "app not found", http.StatusNotFound)
		return
	}
	var body struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := readJSON(r, &body); err != nil || strings.TrimSpace(body.Key) == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}
	if app.EnvVars == nil {
		app.EnvVars = map[string]string{}
	}
	app.EnvVars[body.Key] = body.Value
	if err := s.store.UpdateApp(app); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, app)
}

// handleDeleteEnv removes a single env var from an app.
func (s *Server) handleDeleteEnv(w http.ResponseWriter, r *http.Request) {
	app, err := s.store.GetApp(r.PathValue("id"))
	if err != nil {
		http.Error(w, "app not found", http.StatusNotFound)
		return
	}
	delete(app.EnvVars, r.PathValue("key"))
	if err := s.store.UpdateApp(app); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, app)
}
