package control

import (
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/oyomworld/anchor/internal/store"
	"github.com/oyomworld/anchor/pkg/protocol"
)

// Server is the Anchor control plane: HTTP API + agent hub + live broadcaster.
type Server struct {
	store        store.Store
	hub          *Hub
	auth         *auth
	live         *broadcaster
	mux          *http.ServeMux
	ghTokenCache *tokenCache
	limiter      *rateLimiter

	pendingMu sync.Mutex
	pending   map[string]chan protocol.Event // requestID -> waiter (request/reply)

	deployLocksMu sync.Mutex
	deployLocks   map[string]struct{} // appID -> active deploy gate
}

// New wires up the control plane. It ensures an admin credential exists.
func New(st store.Store, adminUser, adminPass string) (*Server, error) {
	s := &Server{
		store:       st,
		hub:         NewHub(),
		auth:        newAuth(st),
		live:        newBroadcaster(),
		mux:         http.NewServeMux(),
		pending:     map[string]chan protocol.Event{},
		limiter:     newRateLimiter(),
		deployLocks: map[string]struct{}{},
	}

	settings, _ := st.Settings()
	if settings.AdminUser == "" {
		settings.AdminUser = adminUser
		settings.AdminPass = hashPass(adminPass)
		if settings.WebhookSecret == "" {
			settings.WebhookSecret = randToken()
		}
		if err := st.SaveSettings(settings); err != nil {
			return nil, err
		}
		log.Printf("initialized admin user %q", adminUser)
	}

	// purge expired sessions in the background
	go s.sessionJanitor()

	s.routes()
	return s, nil
}

// sessionJanitor periodically deletes expired sessions from the store.
func (s *Server) sessionJanitor() {
	_ = s.store.DeleteExpiredSessions(time.Now())
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	for range t.C {
		_ = s.store.DeleteExpiredSessions(time.Now())
	}
}

func (s *Server) Handler() http.Handler {
	return withBodyLimit(s.mux)
}

// withBodyLimit caps request bodies to prevent memory exhaustion. Agent event
// batches and webhook payloads get a higher limit.
func withBodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := int64(1 << 20) // 1 MB default
		if strings.HasPrefix(r.URL.Path, "/agent/") || strings.HasPrefix(r.URL.Path, "/webhooks/") {
			limit = 5 << 20 // 5 MB for agent event batches and webhooks
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next.ServeHTTP(w, r)
	})
}

// EnsureLocalServer registers a server with a fixed agent token if none with
// that token exists yet. Used to bootstrap an agent that runs alongside the
// control plane (e.g. the all-in-one docker-compose stack) so the host appears
// as a deploy target without a manual "Add server" step.
func (s *Server) EnsureLocalServer(name, token string) error {
	if token == "" {
		return nil
	}
	if _, err := s.store.GetServerByToken(token); err == nil {
		return nil // already registered
	}
	if name == "" {
		name = "local"
	}
	if err := s.store.CreateServer(store.Server{
		ID:         "srv_" + randToken()[:12],
		Name:       name,
		AgentToken: token,
		CreatedAt:  time.Now(),
	}); err != nil {
		return err
	}
	log.Printf("bootstrapped local server %q", name)
	return nil
}

func (s *Server) routes() {
	// Shortcut: auth only (safe) or auth + CSRF (mutating).
	auth := s.requireAuth
	authCSRF := func(f http.HandlerFunc) http.HandlerFunc {
		return auth(s.requireCSRF(f))
	}
	adminCSRF := func(f http.HandlerFunc) http.HandlerFunc {
		return s.requireAdmin(s.requireCSRF(f))
	}

	// --- Agent-facing (agent bearer token) ---
	s.mux.HandleFunc("GET /agent/v1/stream", s.handleAgentStream)
	s.mux.HandleFunc("POST /agent/v1/events", s.handleAgentEvents)

	// --- Auth ---
	s.mux.HandleFunc("POST /api/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/logout", auth(s.handleLogout))
	s.mux.HandleFunc("GET /api/me", auth(s.handleMe))
	s.mux.HandleFunc("POST /api/account/password", authCSRF(s.handleChangePassword))

	// --- Servers ---
	s.mux.HandleFunc("GET /api/servers", auth(s.handleListServers))
	s.mux.HandleFunc("POST /api/servers", authCSRF(s.handleCreateServer))
	s.mux.HandleFunc("DELETE /api/servers/{id}", authCSRF(s.handleDeleteServer))

	// --- Apps ---
	s.mux.HandleFunc("GET /api/apps", auth(s.handleListApps))
	s.mux.HandleFunc("POST /api/apps", authCSRF(s.handleCreateApp))
	s.mux.HandleFunc("GET /api/apps/{id}", auth(s.handleGetApp))
	s.mux.HandleFunc("PATCH /api/apps/{id}", authCSRF(s.handleUpdateApp))
	s.mux.HandleFunc("DELETE /api/apps/{id}", authCSRF(s.handleDeleteApp))
	s.mux.HandleFunc("POST /api/apps/{id}/deploy", authCSRF(s.handleDeployApp))
	s.mux.HandleFunc("POST /api/apps/{id}/rollback", authCSRF(s.handleRollbackApp))
	s.mux.HandleFunc("POST /api/apps/{id}/stop", authCSRF(s.handleStopApp))
	s.mux.HandleFunc("POST /api/apps/{id}/attach-db", authCSRF(s.handleAttachDB))
	s.mux.HandleFunc("POST /api/apps/{id}/env", authCSRF(s.handleSetEnv))
	s.mux.HandleFunc("DELETE /api/apps/{id}/env/{key}", authCSRF(s.handleDeleteEnv))

	// --- Deployments ---
	s.mux.HandleFunc("GET /api/apps/{id}/deployments", auth(s.handleListDeployments))
	s.mux.HandleFunc("GET /api/deployments/{id}", auth(s.handleGetDeployment))

	// --- GitHub ---
	s.mux.HandleFunc("GET /api/github/repos", auth(s.handleGitHubRepos))
	s.mux.HandleFunc("GET /api/github/compose-files", auth(s.handleListComposeFiles))
	s.mux.HandleFunc("GET /api/github/app/manifest", auth(s.handleGitHubAppManifest))
	s.mux.HandleFunc("GET /api/github/app/callback", auth(s.handleGitHubAppCallback))
	s.mux.HandleFunc("GET /api/github/setup", auth(s.handleGitHubSetup))
	s.mux.HandleFunc("POST /webhooks/github", s.handleGitHubWebhook)

	// --- Terminal / exec ---
	s.mux.HandleFunc("POST /api/servers/{id}/exec", authCSRF(s.handleExec))

	// --- Container logs + lifecycle ---
	s.mux.HandleFunc("GET /api/servers/{id}/containers", auth(s.handleListContainers))
	s.mux.HandleFunc("POST /api/servers/{id}/containers/prune", authCSRF(s.handlePruneContainers))
	s.mux.HandleFunc("POST /api/servers/{id}/images/prune", authCSRF(s.handlePruneImages))
	s.mux.HandleFunc("POST /api/servers/{id}/containers/{name}/{action}", authCSRF(s.handleContainerAction))
	s.mux.HandleFunc("POST /api/servers/{id}/logs", authCSRF(s.handleStreamLogs))
	s.mux.HandleFunc("DELETE /api/servers/{id}/logs/{rid}", authCSRF(s.handleStopLogs))

	// --- Managed databases ---
	s.mux.HandleFunc("GET /api/databases", auth(s.handleListDatabases))
	s.mux.HandleFunc("POST /api/databases", authCSRF(s.handleCreateDatabase))
	s.mux.HandleFunc("GET /api/databases/{id}", auth(s.handleGetDatabase))
	s.mux.HandleFunc("DELETE /api/databases/{id}", authCSRF(s.handleDeleteDatabase))
	s.mux.HandleFunc("POST /api/databases/{id}/backup", authCSRF(s.handleBackupDatabase))

	// --- Server stats ---
	s.mux.HandleFunc("GET /api/servers/{id}/stats", auth(s.handleServerStats))

	// --- Users (admin only — including listing) ---
	s.mux.HandleFunc("GET /api/users", adminCSRF(s.handleListUsers))
	s.mux.HandleFunc("POST /api/users", adminCSRF(s.handleCreateUser))
	s.mux.HandleFunc("DELETE /api/users/{id}", adminCSRF(s.handleDeleteUser))

	// --- Live (browser SSE) ---
	s.mux.HandleFunc("GET /api/events", auth(s.handleEventStream))

	// --- Settings ---
	s.mux.HandleFunc("GET /api/settings", auth(s.handleGetSettings))
	s.mux.HandleFunc("PUT /api/settings", authCSRF(s.handleUpdateSettings))

	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
}

// requireAuth wraps a handler with multi-user session validation.
// Viewers can only read (GET/HEAD); admins have full access.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		if !s.auth.valid(tok) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		role := roleFromToken(tok)
		if role == "" {
			role = "admin" // legacy bootstrap session without role prefix
		}
		if role == "viewer" && r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			http.Error(w, "viewer role cannot perform write operations", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// requireAdmin wraps a handler so only authenticated admins may reach it
// (used for user management, including listing). Legacy bootstrap sessions
// without a role prefix are treated as admin.
func (s *Server) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		if !s.auth.valid(tok) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		role := roleFromToken(tok)
		if role == "" {
			role = "admin" // legacy bootstrap session
		}
		if role != "admin" {
			http.Error(w, "admin role required", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// requireCSRF wraps a handler with CSRF validation (double-submit cookie).
// Safe methods (GET, HEAD, OPTIONS) pass through. Mutating methods must present
// an X-CSRF-Token header that matches the anchor_csrf cookie.
func (s *Server) requireCSRF(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next(w, r)
			return
		}
		cookie, err := r.Cookie("anchor_csrf")
		if err != nil {
			http.Error(w, "csrf token missing", http.StatusForbidden)
			return
		}
		header := r.Header.Get("X-CSRF-Token")
		if header == "" || cookie.Value == "" || cookie.Value != header {
			http.Error(w, "csrf token mismatch", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// markOnline updates a server's online flag and last-seen time.
func (s *Server) markOnline(serverID string, online bool) {
	srv, err := s.store.GetServer(serverID)
	if err != nil {
		return
	}
	srv.Online = online
	srv.LastSeen = time.Now()
	_ = s.store.UpdateServer(srv)
	log.Printf("server %s online=%v", srv.Name, online)
}
