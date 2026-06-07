package control

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"encoding/json"
	"github.com/oyomworld/anchor/internal/store"
	"github.com/oyomworld/anchor/pkg/protocol"
)

// default image tags when the client doesn't specify one
var defaultDBVersion = map[string]string{
	"postgres": "16-alpine",
	"redis":    "7-alpine",
}

var defaultDBPort = map[string]int{
	"postgres": 5432,
	"redis":    6379,
}

// handleListDatabases returns all managed databases (passwords included so the
// single admin can copy connection strings).
func (s *Server) handleListDatabases(w http.ResponseWriter, r *http.Request) {
	dbs, _ := s.store.ListDatabases()
	for i := range dbs {
		dbs[i].Status = s.dbLiveStatus(dbs[i])
	}
	writeJSON(w, http.StatusOK, dbs)
}

func (s *Server) handleGetDatabase(w http.ResponseWriter, r *http.Request) {
	db, err := s.store.GetDatabase(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	db.Status = s.dbLiveStatus(db)
	writeJSON(w, http.StatusOK, db)
}

// dbLiveStatus downgrades a "running" db to "unreachable" if its server's agent
// is offline (we can't confirm the container, so surface the uncertainty).
func (s *Server) dbLiveStatus(db store.Database) string {
	if db.Status == "running" && !s.hub.Online(db.ServerID) {
		return "unreachable"
	}
	return db.Status
}

// handleCreateDatabase provisions a new managed database.
func (s *Server) handleCreateDatabase(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		ServerID string `json:"server_id"`
		Engine   string `json:"engine"`
		Version  string `json:"version"`
		HostPort int    `json:"host_port"`
	}
	if err := readJSON(r, &body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || body.ServerID == "" {
		http.Error(w, "name and server_id required", http.StatusBadRequest)
		return
	}
	port, ok := defaultDBPort[body.Engine]
	if !ok {
		http.Error(w, "engine must be postgres or redis", http.StatusBadRequest)
		return
	}
	if body.Version == "" {
		body.Version = defaultDBVersion[body.Engine]
	}
	if _, err := s.store.GetServer(body.ServerID); err != nil {
		http.Error(w, "server not found", http.StatusBadRequest)
		return
	}

	slug := dbSanitize(body.Name)
	container := "anchor-db-" + slug
	db := store.Database{
		ID:        "db_" + randToken()[:12],
		Name:      body.Name,
		ServerID:  body.ServerID,
		Engine:    body.Engine,
		Version:   body.Version,
		Status:    "provisioning",
		Container: container,
		Volume:    "anchor_db_" + slug,
		Host:      container,
		Port:      port,
		HostPort:  body.HostPort,
		Username:  "anchor",
		Password:  randToken()[:24],
		DBName:    slug,
		CreatedAt: time.Now(),
	}
	db.ConnURI = connURI(db)

	if err := s.store.CreateDatabase(db); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	payload, _ := json.Marshal(protocol.ProvisionDBRequest{
		DatabaseID: db.ID, Container: db.Container, Volume: db.Volume,
		Engine: db.Engine, Version: db.Version,
		Username: db.Username, Password: db.Password, DBName: db.DBName, HostPort: db.HostPort,
	})
	cmd := protocol.Command{ID: randToken()[:12], Type: protocol.CmdProvisionDB, Data: payload}
	if !s.hub.Send(db.ServerID, cmd) {
		db.Status = "failed"
		db.Message = "target server agent is offline"
		_ = s.store.UpdateDatabase(db)
	}
	writeJSON(w, http.StatusCreated, db)
}

// handleDeleteDatabase removes a managed database (and its volume by default).
func (s *Server) handleDeleteDatabase(w http.ResponseWriter, r *http.Request) {
	db, err := s.store.GetDatabase(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	deleteVolume := r.URL.Query().Get("keep_volume") != "true"
	payload, _ := json.Marshal(protocol.RemoveDBRequest{
		DatabaseID: db.ID, Container: db.Container, Volume: db.Volume, DeleteVolume: deleteVolume,
	})
	cmd := protocol.Command{ID: randToken()[:12], Type: protocol.CmdRemoveDB, Data: payload}
	s.hub.Send(db.ServerID, cmd) // best-effort
	_ = s.store.DeleteDatabase(db.ID)
	w.WriteHeader(http.StatusNoContent)
}

// connURI builds the in-network connection string apps use (host = container
// name on anchor_net).
func connURI(db store.Database) string {
	switch db.Engine {
	case "postgres":
		return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
			db.Username, db.Password, db.Host, db.Port, db.DBName)
	case "redis":
		return fmt.Sprintf("redis://:%s@%s:%d", db.Password, db.Host, db.Port)
	}
	return ""
}

// dbSanitize lowercases and strips a name to a docker/identifier-safe slug.
func dbSanitize(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = "db"
	}
	return out
}

// handleBackupDatabase triggers a backup (pg_dump / redis SAVE) and returns the
// result synchronously via the request/reply channel.
func (s *Server) handleBackupDatabase(w http.ResponseWriter, r *http.Request) {
	db, err := s.store.GetDatabase(r.PathValue("id"))
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if !s.hub.Online(db.ServerID) {
		http.Error(w, "server agent offline", http.StatusConflict)
		return
	}

	reqID := "bkup_" + randToken()[:12]
	ch, cancel := s.awaitReply(reqID)
	defer cancel()

	payload, _ := json.Marshal(protocol.BackupDBRequest{
		RequestID:  reqID,
		DatabaseID: db.ID,
		Engine:     db.Engine,
		Container:  db.Container,
		Username:   db.Username,
		DBName:     db.DBName,
	})
	cmd := protocol.Command{ID: randToken()[:12], Type: protocol.CmdBackupDB, Data: payload}
	if !s.hub.Send(db.ServerID, cmd) {
		http.Error(w, "agent offline", http.StatusConflict)
		return
	}

	select {
	case evt := <-ch:
		var result protocol.BackupResult
		_ = json.Unmarshal(evt.Data, &result)
		if result.Error != "" {
			http.Error(w, result.Error, http.StatusInternalServerError)
			return
		}
		// Stream the backup as a downloadable file.
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="backup-%s-%s.sql"`, db.Name, time.Now().UTC().Format("20060102-150405")))
		w.Write([]byte(result.Data))
	case <-time.After(60 * time.Second):
		http.Error(w, "backup timed out", http.StatusGatewayTimeout)
	case <-r.Context().Done():
	}
}
