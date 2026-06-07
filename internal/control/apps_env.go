package control

import (
	"net/http"
	"strings"

	"github.com/oyomworld/anchor/pkg/protocol"
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
	if app.EnvSecret == nil {
		app.EnvSecret = map[string]bool{}
	}
	app.EnvSecret[varName] = true // a DB connection string is always a secret
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
		Key    string `json:"key"`
		Value  string `json:"value"`
		Secret *bool  `json:"secret"` // nil = secret (safe default)
	}
	if err := readJSON(r, &body); err != nil || strings.TrimSpace(body.Key) == "" {
		http.Error(w, "key required", http.StatusBadRequest)
		return
	}
	key := strings.TrimSpace(body.Key)
	if app.EnvVars == nil {
		app.EnvVars = map[string]string{}
	}
	if app.EnvSecret == nil {
		app.EnvSecret = map[string]bool{}
	}
	app.EnvVars[key] = body.Value
	app.EnvSecret[key] = body.Secret == nil || *body.Secret
	if err := s.store.UpdateApp(app); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	app.ContainerName = protocol.Sanitize(app.Name)
	writeJSON(w, http.StatusOK, app)
}

// parseDotenv parses a .env-style blob into key/value pairs. It tolerates
// comments (# ...), blank lines, section banners, optional `export `, and
// surrounding single/double quotes; values may contain '=' (only the first '='
// splits). Keys must be valid env identifiers; invalid lines are skipped.
func parseDotenv(content string) map[string]string {
	out := map[string]string{}
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if !validEnvKey(key) {
			continue
		}
		// strip a matching pair of surrounding quotes
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		out[key] = val
	}
	return out
}

func validEnvKey(k string) bool {
	if k == "" {
		return false
	}
	for i, r := range k {
		isLetter := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_'
		isDigit := r >= '0' && r <= '9'
		if i == 0 && !isLetter {
			return false
		}
		if !isLetter && !isDigit {
			return false
		}
	}
	return true
}

// handleImportEnv bulk-imports env vars from a pasted .env blob, merging into
// the app's existing vars. Imported vars default to secret (masked).
func (s *Server) handleImportEnv(w http.ResponseWriter, r *http.Request) {
	app, err := s.store.GetApp(r.PathValue("id"))
	if err != nil {
		http.Error(w, "app not found", http.StatusNotFound)
		return
	}
	var body struct {
		Content string `json:"content"`
		Secret  *bool  `json:"secret"` // nil = secret
	}
	if err := readJSON(r, &body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	parsed := parseDotenv(body.Content)
	if len(parsed) == 0 {
		http.Error(w, "no valid KEY=VALUE pairs found", http.StatusBadRequest)
		return
	}
	if app.EnvVars == nil {
		app.EnvVars = map[string]string{}
	}
	if app.EnvSecret == nil {
		app.EnvSecret = map[string]bool{}
	}
	secret := body.Secret == nil || *body.Secret
	for k, v := range parsed {
		app.EnvVars[k] = v
		app.EnvSecret[k] = secret
	}
	if err := s.store.UpdateApp(app); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	app.ContainerName = protocol.Sanitize(app.Name)
	writeJSON(w, http.StatusOK, map[string]any{"app": app, "imported": len(parsed)})
}

// handleDeleteEnv removes a single env var from an app.
func (s *Server) handleDeleteEnv(w http.ResponseWriter, r *http.Request) {
	app, err := s.store.GetApp(r.PathValue("id"))
	if err != nil {
		http.Error(w, "app not found", http.StatusNotFound)
		return
	}
	key := r.PathValue("key")
	delete(app.EnvVars, key)
	delete(app.EnvSecret, key)
	if err := s.store.UpdateApp(app); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	app.ContainerName = protocol.Sanitize(app.Name)
	writeJSON(w, http.StatusOK, app)
}
