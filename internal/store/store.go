// Package store is the persistence layer for the control plane.
//
// The MVP uses a single JSON file guarded by a mutex. Everything is accessed
// through the Store interface so it can later be swapped for SQLite/Postgres
// without changing the HTTP handlers.
package store

import (
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrNotFound = errors.New("not found")

// ---- Domain models ---------------------------------------------------------

// Server is a VPS running an agent.
type Server struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	AgentToken string    `json:"agent_token"` // bearer token the agent presents
	Online     bool      `json:"online"`
	LastSeen   time.Time `json:"last_seen"`
	Stats      *Stats    `json:"stats,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// Stats is the last reported system snapshot for a server.
type Stats struct {
	CPUPercent float64   `json:"cpu_percent"`
	MemUsed    uint64    `json:"mem_used"`
	MemTotal   uint64    `json:"mem_total"`
	DiskUsed   uint64    `json:"disk_used"`
	DiskTotal  uint64    `json:"disk_total"`
	Containers int       `json:"containers"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ServerStat is a single time-series data point for server metrics.
type ServerStat struct {
	ServerID   string    `json:"server_id"`
	CPUPercent float64   `json:"cpu_percent"`
	MemUsed    uint64    `json:"mem_used"`
	MemTotal   uint64    `json:"mem_total"`
	DiskUsed   uint64    `json:"disk_used"`
	DiskTotal  uint64    `json:"disk_total"`
	Containers int       `json:"containers"`
	Load1      float64   `json:"load_1"`
	At         time.Time `json:"at"`
}

// User represents an admin user with a role.
type User struct {
	ID           string    `json:"id"`
	Username     string    `json:"username"`
	PasswordHash string    `json:"password_hash"`
	Role         string    `json:"role"` // admin | viewer
	CreatedAt    time.Time `json:"created_at"`
}

// App is a deployable project bound to a GitHub repo and a target server.
type App struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	ServerID      string            `json:"server_id"`
	RepoFullName  string            `json:"repo_full_name"` // owner/repo
	RepoURL       string            `json:"repo_url"`
	Branch        string            `json:"branch"`
	Domain        string            `json:"domain"`
	ContainerPort int               `json:"container_port"`
	AutoDeploy    bool              `json:"auto_deploy"`
	EnvVars       map[string]string `json:"env_vars"`
	EnvSecret     map[string]bool   `json:"env_secret,omitempty"`    // key -> secret? absent/true = masked, false = plain
	ComposeFile   string            `json:"compose_file,omitempty"`  // explicit compose file path; "" = auto-detect
	LastGoodSHA   string            `json:"last_good_sha,omitempty"` // for rollbacks
	CreatedAt     time.Time         `json:"created_at"`

	// ContainerName is the docker container / compose-project name derived from
	// Name. Computed on read (not persisted) so the UI can match the running
	// container without re-implementing the sanitizer.
	ContainerName string `json:"container_name,omitempty"`
}

// Deployment is a single build+run attempt for an App.
type Deployment struct {
	ID        string    `json:"id"`
	AppID     string    `json:"app_id"`
	CommitSHA string    `json:"commit_sha"`
	Branch    string    `json:"branch"`
	Phase     string    `json:"phase"`
	StackType string    `json:"stack_type"`
	Message   string    `json:"message"`
	Logs      []LogLine `json:"logs"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type LogLine struct {
	Stream string    `json:"stream"`
	Line   string    `json:"line"`
	At     time.Time `json:"at"`
}

// Database is a managed datastore (Postgres/Redis) running as a container on a
// target server, reachable by apps on the anchor_net network by Container name.
type Database struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	ServerID  string    `json:"server_id"`
	Engine    string    `json:"engine"`  // postgres | redis
	Version   string    `json:"version"` // image tag
	Status    string    `json:"status"`  // provisioning | running | failed | stopped
	Message   string    `json:"message"`
	Container string    `json:"container"` // docker name + in-network hostname
	Volume    string    `json:"volume"`
	Host      string    `json:"host"` // = Container (in-network)
	Port      int       `json:"port"`
	HostPort  int       `json:"host_port"` // 0 = internal only
	Username  string    `json:"username"`
	Password  string    `json:"password"`
	DBName    string    `json:"db_name"`
	ConnURI   string    `json:"conn_uri"`
	CreatedAt time.Time `json:"created_at"`
}

// Settings holds singleton config (admin creds, GitHub config, etc).
type Settings struct {
	AdminUser     string `json:"admin_user"`
	AdminPass     string `json:"admin_pass_hash"` // bcrypt-like; MVP uses sha256
	GitHubToken   string `json:"github_token"`    // optional PAT fallback
	WebhookSecret string `json:"webhook_secret"`  // used when no GitHub App configured

	// GitHub App (preferred). Populated via the manifest flow.
	GitHubAppID            int64  `json:"github_app_id"`
	GitHubAppSlug          string `json:"github_app_slug"`
	GitHubAppPrivateKey    string `json:"github_app_private_key"` // PEM
	GitHubAppWebhookSecret string `json:"github_app_webhook_secret"`
	GitHubClientID         string `json:"github_client_id"`
	GitHubClientSecret     string `json:"github_client_secret"`
	GitHubInstallationID   int64  `json:"github_installation_id"`

	// Notification webhook (Slack/Discord).
	NotificationWebhook string `json:"notification_webhook"`

	// BaseDomain auto-assigns apps a subdomain (<slug>.<base_domain>) when no
	// custom domain is set. Requires a wildcard DNS record (*.<base_domain>).
	BaseDomain string `json:"base_domain"`
}

// GitHubAppConfigured reports whether a GitHub App is fully set up.
func (s Settings) GitHubAppConfigured() bool {
	return s.GitHubAppID != 0 && s.GitHubAppPrivateKey != ""
}

// ---- Store interface -------------------------------------------------------

type Store interface {
	Settings() (Settings, error)
	SaveSettings(Settings) error

	ListServers() ([]Server, error)
	GetServer(id string) (Server, error)
	GetServerByToken(token string) (Server, error)
	CreateServer(Server) error
	UpdateServer(Server) error
	DeleteServer(id string) error

	ListApps() ([]App, error)
	GetApp(id string) (App, error)
	AppsByRepo(fullName string) ([]App, error)
	CreateApp(App) error
	UpdateApp(App) error
	DeleteApp(id string) error

	ListDeployments(appID string) ([]Deployment, error)
	GetDeployment(id string) (Deployment, error)
	CreateDeployment(Deployment) error
	UpdateDeployment(Deployment) error
	AppendDeploymentLog(deploymentID string, line LogLine) error

	ListDatabases() ([]Database, error)
	GetDatabase(id string) (Database, error)
	CreateDatabase(Database) error
	UpdateDatabase(Database) error
	DeleteDatabase(id string) error

	// Sessions (persistent admin login sessions).
	CreateSession(token string, expiresAt time.Time) error
	GetSession(token string) (time.Time, error)
	DeleteSession(token string) error
	DeleteExpiredSessions(now time.Time) error
	// DeleteSessionsForUser revokes every session belonging to username except
	// exceptToken (the caller's own). Used to log out other devices on a
	// password change. Session tokens are formatted "username:role:random".
	DeleteSessionsForUser(username, exceptToken string) error

	// Server stats (time series).
	InsertServerStat(ServerStat) error
	ServerStats(serverID string, since time.Time, limit int) ([]ServerStat, error)

	// Users (multi-user).
	ListUsers() ([]User, error)
	GetUser(id string) (User, error)
	GetUserByUsername(username string) (User, error)
	CreateUser(User) error
	DeleteUser(id string) error
}

// ---- JSON file implementation ---------------------------------------------

type data struct {
	Settings    Settings                `json:"settings"`
	Servers     map[string]Server       `json:"servers"`
	Apps        map[string]App          `json:"apps"`
	Deployments map[string]Deployment   `json:"deployments"`
	Databases   map[string]Database     `json:"databases"`
	Sessions    map[string]time.Time    `json:"sessions"`
	Users       map[string]User         `json:"users"`
	ServerStats map[string][]ServerStat `json:"server_stats"`
}

type jsonStore struct {
	mu   sync.RWMutex
	path string
	d    data
}

// Open loads (or initializes) the JSON store at path.
func Open(path string) (Store, error) {
	s := &jsonStore{path: path, d: data{
		Servers:     map[string]Server{},
		Apps:        map[string]App{},
		Deployments: map[string]Deployment{},
		Databases:   map[string]Database{},
		Sessions:    map[string]time.Time{},
		Users:       map[string]User{},
		ServerStats: map[string][]ServerStat{},
	}}
	b, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(b, &s.d); err != nil {
			return nil, err
		}
		if s.d.Servers == nil {
			s.d.Servers = map[string]Server{}
		}
		if s.d.Apps == nil {
			s.d.Apps = map[string]App{}
		}
		if s.d.Deployments == nil {
			s.d.Deployments = map[string]Deployment{}
		}
		if s.d.Databases == nil {
			s.d.Databases = map[string]Database{}
		}
		if s.d.Sessions == nil {
			s.d.Sessions = map[string]time.Time{}
		}
		if s.d.Users == nil {
			s.d.Users = map[string]User{}
		}
		if s.d.ServerStats == nil {
			s.d.ServerStats = map[string][]ServerStat{}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return s, nil
}

// flush writes the in-memory data to disk atomically. Caller holds the lock.
func (s *jsonStore) flush() error {
	b, err := json.MarshalIndent(s.d, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func (s *jsonStore) Settings() (Settings, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.d.Settings, nil
}

func (s *jsonStore) SaveSettings(v Settings) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Settings = v
	return s.flush()
}

func (s *jsonStore) ListServers() ([]Server, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Server, 0, len(s.d.Servers))
	for _, v := range s.d.Servers {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *jsonStore) GetServer(id string) (Server, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.d.Servers[id]
	if !ok {
		return Server{}, ErrNotFound
	}
	return v, nil
}

func (s *jsonStore) GetServerByToken(token string) (Server, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, v := range s.d.Servers {
		if v.AgentToken == token {
			return v, nil
		}
	}
	return Server{}, ErrNotFound
}

func (s *jsonStore) CreateServer(v Server) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Servers[v.ID] = v
	return s.flush()
}

func (s *jsonStore) UpdateServer(v Server) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.d.Servers[v.ID]; !ok {
		return ErrNotFound
	}
	s.d.Servers[v.ID] = v
	return s.flush()
}

func (s *jsonStore) DeleteServer(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.d.Servers, id)
	return s.flush()
}

func (s *jsonStore) ListApps() ([]App, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]App, 0, len(s.d.Apps))
	for _, v := range s.d.Apps {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *jsonStore) GetApp(id string) (App, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.d.Apps[id]
	if !ok {
		return App{}, ErrNotFound
	}
	return v, nil
}

func (s *jsonStore) AppsByRepo(fullName string) ([]App, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []App{}
	for _, v := range s.d.Apps {
		if v.RepoFullName == fullName {
			out = append(out, v)
		}
	}
	return out, nil
}

func (s *jsonStore) CreateApp(v App) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Apps[v.ID] = v
	return s.flush()
}

func (s *jsonStore) UpdateApp(v App) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.d.Apps[v.ID]; !ok {
		return ErrNotFound
	}
	s.d.Apps[v.ID] = v
	return s.flush()
}

func (s *jsonStore) DeleteApp(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.d.Apps, id)
	return s.flush()
}

func (s *jsonStore) ListDeployments(appID string) ([]Deployment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []Deployment{}
	for _, v := range s.d.Deployments {
		if appID == "" || v.AppID == appID {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (s *jsonStore) GetDeployment(id string) (Deployment, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.d.Deployments[id]
	if !ok {
		return Deployment{}, ErrNotFound
	}
	return v, nil
}

func (s *jsonStore) CreateDeployment(v Deployment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Deployments[v.ID] = v
	return s.flush()
}

func (s *jsonStore) UpdateDeployment(v Deployment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.d.Deployments[v.ID]; !ok {
		return ErrNotFound
	}
	s.d.Deployments[v.ID] = v
	return s.flush()
}

func (s *jsonStore) AppendDeploymentLog(deploymentID string, line LogLine) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	dep, ok := s.d.Deployments[deploymentID]
	if !ok {
		return ErrNotFound
	}
	dep.Logs = append(dep.Logs, line)
	if len(dep.Logs) > 5000 {
		dep.Logs = dep.Logs[len(dep.Logs)-5000:]
	}
	s.d.Deployments[deploymentID] = dep
	return s.flush()
}

func (s *jsonStore) ListDatabases() ([]Database, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Database, 0, len(s.d.Databases))
	for _, v := range s.d.Databases {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (s *jsonStore) GetDatabase(id string) (Database, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.d.Databases[id]
	if !ok {
		return Database{}, ErrNotFound
	}
	return v, nil
}

func (s *jsonStore) CreateDatabase(v Database) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Databases[v.ID] = v
	return s.flush()
}

func (s *jsonStore) UpdateDatabase(v Database) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.d.Databases[v.ID]; !ok {
		return ErrNotFound
	}
	s.d.Databases[v.ID] = v
	return s.flush()
}

func (s *jsonStore) DeleteDatabase(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.d.Databases, id)
	return s.flush()
}

func (s *jsonStore) CreateSession(token string, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.d.Sessions[token] = expiresAt
	return s.flush()
}

func (s *jsonStore) GetSession(token string) (time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	exp, ok := s.d.Sessions[token]
	if !ok {
		return time.Time{}, ErrNotFound
	}
	return exp, nil
}

func (s *jsonStore) DeleteSession(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.d.Sessions, token)
	return s.flush()
}

func (s *jsonStore) DeleteExpiredSessions(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for tok, exp := range s.d.Sessions {
		if now.After(exp) {
			delete(s.d.Sessions, tok)
			changed = true
		}
	}
	if changed {
		return s.flush()
	}
	return nil
}

func (s *jsonStore) DeleteSessionsForUser(username, exceptToken string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := username + ":"
	changed := false
	for tok := range s.d.Sessions {
		if tok != exceptToken && strings.HasPrefix(tok, prefix) {
			delete(s.d.Sessions, tok)
			changed = true
		}
	}
	if changed {
		return s.flush()
	}
	return nil
}

// jsonMaxStatsPerServer caps the retained time-series points per server so the
// JSON file doesn't grow without bound.
const jsonMaxStatsPerServer = 500

func (s *jsonStore) InsertServerStat(st ServerStat) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pts := append(s.d.ServerStats[st.ServerID], st)
	if len(pts) > jsonMaxStatsPerServer {
		pts = pts[len(pts)-jsonMaxStatsPerServer:]
	}
	s.d.ServerStats[st.ServerID] = pts
	return s.flush()
}

func (s *jsonStore) ServerStats(serverID string, since time.Time, limit int) ([]ServerStat, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if limit <= 0 {
		limit = 200
	}
	all := s.d.ServerStats[serverID]
	out := []ServerStat{}
	// Walk newest-first so the most recent `limit` points are returned.
	for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
		if all[i].At.Before(since) {
			continue
		}
		out = append(out, all[i])
	}
	return out, nil
}

func (s *jsonStore) ListUsers() ([]User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []User{}
	for _, u := range s.d.Users {
		out = append(out, u)
	}
	return out, nil
}

func (s *jsonStore) GetUser(id string) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if u, ok := s.d.Users[id]; ok {
		return u, nil
	}
	return User{}, ErrNotFound
}

func (s *jsonStore) GetUserByUsername(username string) (User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.d.Users {
		if u.Username == username {
			return u, nil
		}
	}
	return User{}, ErrNotFound
}

func (s *jsonStore) CreateUser(v User) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.d.Users {
		if u.Username == v.Username {
			return errors.New("username already exists")
		}
	}
	s.d.Users[v.ID] = v
	return s.flush()
}

func (s *jsonStore) DeleteUser(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.d.Users, id)
	return s.flush()
}
