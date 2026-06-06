package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO)
)

// sqliteStore is a durable Store backed by SQLite. It implements the same
// interface as the JSON store so the control plane is agnostic to which is used.
type sqliteStore struct {
	db *sql.DB
}

// OpenSQLite opens (and migrates) a SQLite database at path.
func OpenSQLite(path string) (Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // SQLite: serialize writes to avoid "database is locked"
	s := &sqliteStore{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *sqliteStore) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS settings (
	id   INTEGER PRIMARY KEY CHECK (id = 1),
	data TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS servers (
	id          TEXT PRIMARY KEY,
	name        TEXT NOT NULL,
	agent_token TEXT NOT NULL UNIQUE,
	online      INTEGER NOT NULL DEFAULT 0,
	last_seen   TEXT,
	stats       TEXT,
	created_at  TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS apps (
	id             TEXT PRIMARY KEY,
	name           TEXT NOT NULL,
	server_id      TEXT NOT NULL,
	repo_full_name TEXT,
	repo_url       TEXT NOT NULL,
	branch         TEXT NOT NULL,
	domain         TEXT,
	container_port INTEGER NOT NULL,
	auto_deploy    INTEGER NOT NULL DEFAULT 0,
	env_vars       TEXT NOT NULL DEFAULT '{}',
	created_at     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_apps_repo ON apps(repo_full_name);
CREATE TABLE IF NOT EXISTS deployments (
	id         TEXT PRIMARY KEY,
	app_id     TEXT NOT NULL,
	commit_sha TEXT,
	branch     TEXT,
	phase      TEXT,
	stack_type TEXT,
	message    TEXT,
	created_at TEXT NOT NULL,
	updated_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_deps_app ON deployments(app_id);
CREATE TABLE IF NOT EXISTS deployment_logs (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	deployment_id TEXT NOT NULL,
	stream        TEXT,
	line          TEXT,
	at            TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_logs_dep ON deployment_logs(deployment_id, id);
`)
	return err
}

// ---- helpers ----

func tsParse(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}
func tsFmt(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// ---- Settings ----

func (s *sqliteStore) Settings() (Settings, error) {
	var raw string
	err := s.db.QueryRow(`SELECT data FROM settings WHERE id = 1`).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return Settings{}, nil
	}
	if err != nil {
		return Settings{}, err
	}
	var v Settings
	return v, json.Unmarshal([]byte(raw), &v)
}

func (s *sqliteStore) SaveSettings(v Settings) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO settings (id, data) VALUES (1, ?)
		ON CONFLICT(id) DO UPDATE SET data = excluded.data`, string(b))
	return err
}

// ---- Servers ----

func scanStats(raw sql.NullString) *Stats {
	if !raw.Valid || raw.String == "" {
		return nil
	}
	var st Stats
	if json.Unmarshal([]byte(raw.String), &st) != nil {
		return nil
	}
	return &st
}

func (s *sqliteStore) ListServers() ([]Server, error) {
	rows, err := s.db.Query(`SELECT id, name, agent_token, online, last_seen, stats, created_at FROM servers ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Server
	for rows.Next() {
		v, err := scanServer(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanServer(r scanner) (Server, error) {
	var v Server
	var lastSeen, createdAt string
	var stats sql.NullString
	var online int
	if err := r.Scan(&v.ID, &v.Name, &v.AgentToken, &online, &lastSeen, &stats, &createdAt); err != nil {
		return v, err
	}
	v.Online = online == 1
	v.LastSeen = tsParse(lastSeen)
	v.CreatedAt = tsParse(createdAt)
	v.Stats = scanStats(stats)
	return v, nil
}

func (s *sqliteStore) GetServer(id string) (Server, error) {
	row := s.db.QueryRow(`SELECT id, name, agent_token, online, last_seen, stats, created_at FROM servers WHERE id = ?`, id)
	v, err := scanServer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, ErrNotFound
	}
	return v, err
}

func (s *sqliteStore) GetServerByToken(token string) (Server, error) {
	row := s.db.QueryRow(`SELECT id, name, agent_token, online, last_seen, stats, created_at FROM servers WHERE agent_token = ?`, token)
	v, err := scanServer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Server{}, ErrNotFound
	}
	return v, err
}

func (s *sqliteStore) CreateServer(v Server) error {
	_, err := s.db.Exec(`INSERT INTO servers (id, name, agent_token, online, last_seen, stats, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		v.ID, v.Name, v.AgentToken, b2i(v.Online), tsFmt(v.LastSeen), statsJSON(v.Stats), tsFmt(v.CreatedAt))
	return err
}

func (s *sqliteStore) UpdateServer(v Server) error {
	res, err := s.db.Exec(`UPDATE servers SET name=?, agent_token=?, online=?, last_seen=?, stats=? WHERE id=?`,
		v.Name, v.AgentToken, b2i(v.Online), tsFmt(v.LastSeen), statsJSON(v.Stats), v.ID)
	return affected(res, err)
}

func (s *sqliteStore) DeleteServer(id string) error {
	_, err := s.db.Exec(`DELETE FROM servers WHERE id = ?`, id)
	return err
}

// ---- Apps ----

func scanApp(r scanner) (App, error) {
	var v App
	var envRaw, createdAt string
	var auto int
	if err := r.Scan(&v.ID, &v.Name, &v.ServerID, &v.RepoFullName, &v.RepoURL, &v.Branch,
		&v.Domain, &v.ContainerPort, &auto, &envRaw, &createdAt); err != nil {
		return v, err
	}
	v.AutoDeploy = auto == 1
	v.CreatedAt = tsParse(createdAt)
	v.EnvVars = map[string]string{}
	_ = json.Unmarshal([]byte(envRaw), &v.EnvVars)
	return v, nil
}

const appCols = `id, name, server_id, repo_full_name, repo_url, branch, domain, container_port, auto_deploy, env_vars, created_at`

func (s *sqliteStore) ListApps() ([]App, error) {
	rows, err := s.db.Query(`SELECT ` + appCols + ` FROM apps ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []App
	for rows.Next() {
		v, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *sqliteStore) GetApp(id string) (App, error) {
	row := s.db.QueryRow(`SELECT `+appCols+` FROM apps WHERE id = ?`, id)
	v, err := scanApp(row)
	if errors.Is(err, sql.ErrNoRows) {
		return App{}, ErrNotFound
	}
	return v, err
}

func (s *sqliteStore) AppsByRepo(fullName string) ([]App, error) {
	rows, err := s.db.Query(`SELECT `+appCols+` FROM apps WHERE repo_full_name = ?`, fullName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []App
	for rows.Next() {
		v, err := scanApp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *sqliteStore) CreateApp(v App) error {
	_, err := s.db.Exec(`INSERT INTO apps (`+appCols+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		v.ID, v.Name, v.ServerID, v.RepoFullName, v.RepoURL, v.Branch, v.Domain,
		v.ContainerPort, b2i(v.AutoDeploy), envJSON(v.EnvVars), tsFmt(v.CreatedAt))
	return err
}

func (s *sqliteStore) UpdateApp(v App) error {
	res, err := s.db.Exec(`UPDATE apps SET name=?, server_id=?, repo_full_name=?, repo_url=?, branch=?, domain=?, container_port=?, auto_deploy=?, env_vars=? WHERE id=?`,
		v.Name, v.ServerID, v.RepoFullName, v.RepoURL, v.Branch, v.Domain, v.ContainerPort, b2i(v.AutoDeploy), envJSON(v.EnvVars), v.ID)
	return affected(res, err)
}

func (s *sqliteStore) DeleteApp(id string) error {
	_, err := s.db.Exec(`DELETE FROM apps WHERE id = ?`, id)
	return err
}

// ---- Deployments ----

func scanDeployment(r scanner) (Deployment, error) {
	var v Deployment
	var created, updated string
	if err := r.Scan(&v.ID, &v.AppID, &v.CommitSHA, &v.Branch, &v.Phase, &v.StackType, &v.Message, &created, &updated); err != nil {
		return v, err
	}
	v.CreatedAt = tsParse(created)
	v.UpdatedAt = tsParse(updated)
	return v, nil
}

const depCols = `id, app_id, commit_sha, branch, phase, stack_type, message, created_at, updated_at`

func (s *sqliteStore) ListDeployments(appID string) ([]Deployment, error) {
	var rows *sql.Rows
	var err error
	if appID == "" {
		rows, err = s.db.Query(`SELECT ` + depCols + ` FROM deployments ORDER BY created_at DESC`)
	} else {
		rows, err = s.db.Query(`SELECT `+depCols+` FROM deployments WHERE app_id = ? ORDER BY created_at DESC`, appID)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Deployment
	for rows.Next() {
		v, err := scanDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *sqliteStore) GetDeployment(id string) (Deployment, error) {
	row := s.db.QueryRow(`SELECT `+depCols+` FROM deployments WHERE id = ?`, id)
	v, err := scanDeployment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Deployment{}, ErrNotFound
	}
	if err != nil {
		return v, err
	}
	// load logs
	lrows, err := s.db.Query(`SELECT stream, line, at FROM deployment_logs WHERE deployment_id = ? ORDER BY id`, id)
	if err != nil {
		return v, err
	}
	defer lrows.Close()
	for lrows.Next() {
		var ll LogLine
		var at string
		if err := lrows.Scan(&ll.Stream, &ll.Line, &at); err != nil {
			return v, err
		}
		ll.At = tsParse(at)
		v.Logs = append(v.Logs, ll)
	}
	return v, lrows.Err()
}

func (s *sqliteStore) CreateDeployment(v Deployment) error {
	_, err := s.db.Exec(`INSERT INTO deployments (`+depCols+`) VALUES (?,?,?,?,?,?,?,?,?)`,
		v.ID, v.AppID, v.CommitSHA, v.Branch, v.Phase, v.StackType, v.Message, tsFmt(v.CreatedAt), tsFmt(v.UpdatedAt))
	return err
}

// UpdateDeployment persists phase/message/stack changes and appends any NEW log
// lines (those beyond what's already stored) so callers can keep using the same
// append-then-save pattern as the JSON store.
func (s *sqliteStore) UpdateDeployment(v Deployment) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`UPDATE deployments SET commit_sha=?, branch=?, phase=?, stack_type=?, message=?, updated_at=? WHERE id=?`,
		v.CommitSHA, v.Branch, v.Phase, v.StackType, v.Message, tsFmt(v.UpdatedAt), v.ID)
	if err := affected(res, err); err != nil {
		return err
	}

	if len(v.Logs) > 0 {
		var have int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM deployment_logs WHERE deployment_id = ?`, v.ID).Scan(&have); err != nil {
			return err
		}
		if have < len(v.Logs) {
			stmt, err := tx.Prepare(`INSERT INTO deployment_logs (deployment_id, stream, line, at) VALUES (?,?,?,?)`)
			if err != nil {
				return err
			}
			defer stmt.Close()
			for _, ll := range v.Logs[have:] {
				if _, err := stmt.Exec(v.ID, ll.Stream, ll.Line, tsFmt(ll.At)); err != nil {
					return err
				}
			}
		}
	}
	return tx.Commit()
}

// ---- small helpers ----

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func statsJSON(st *Stats) any {
	if st == nil {
		return nil
	}
	b, _ := json.Marshal(st)
	return string(b)
}

func envJSON(m map[string]string) string {
	if m == nil {
		m = map[string]string{}
	}
	b, _ := json.Marshal(m)
	return string(b)
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
