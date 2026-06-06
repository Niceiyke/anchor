package control

import (
	"log"
	"net/http"
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

	pendingMu sync.Mutex
	pending   map[string]chan protocol.Event // requestID -> waiter (request/reply)
}

// New wires up the control plane. It ensures an admin credential exists.
func New(st store.Store, adminUser, adminPass string) (*Server, error) {
	s := &Server{
		store:   st,
		hub:     NewHub(),
		auth:    newAuth(),
		live:    newBroadcaster(),
		mux:     http.NewServeMux(),
		pending: map[string]chan protocol.Event{},
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

	s.routes()
	return s, nil
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	// --- Agent-facing (agent bearer token) ---
	s.mux.HandleFunc("GET /agent/v1/stream", s.handleAgentStream)
	s.mux.HandleFunc("POST /agent/v1/events", s.handleAgentEvents)

	// --- Auth ---
	s.mux.HandleFunc("POST /api/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/logout", s.requireAuth(s.handleLogout))
	s.mux.HandleFunc("GET /api/me", s.requireAuth(s.handleMe))

	// --- Servers ---
	s.mux.HandleFunc("GET /api/servers", s.requireAuth(s.handleListServers))
	s.mux.HandleFunc("POST /api/servers", s.requireAuth(s.handleCreateServer))
	s.mux.HandleFunc("DELETE /api/servers/{id}", s.requireAuth(s.handleDeleteServer))

	// --- Apps ---
	s.mux.HandleFunc("GET /api/apps", s.requireAuth(s.handleListApps))
	s.mux.HandleFunc("POST /api/apps", s.requireAuth(s.handleCreateApp))
	s.mux.HandleFunc("GET /api/apps/{id}", s.requireAuth(s.handleGetApp))
	s.mux.HandleFunc("DELETE /api/apps/{id}", s.requireAuth(s.handleDeleteApp))
	s.mux.HandleFunc("POST /api/apps/{id}/deploy", s.requireAuth(s.handleDeployApp))

	// --- Deployments ---
	s.mux.HandleFunc("GET /api/apps/{id}/deployments", s.requireAuth(s.handleListDeployments))
	s.mux.HandleFunc("GET /api/deployments/{id}", s.requireAuth(s.handleGetDeployment))

	// --- GitHub ---
	s.mux.HandleFunc("GET /api/github/repos", s.requireAuth(s.handleGitHubRepos))
	s.mux.HandleFunc("GET /api/github/app/manifest", s.requireAuth(s.handleGitHubAppManifest))
	s.mux.HandleFunc("GET /api/github/app/callback", s.requireAuth(s.handleGitHubAppCallback))
	s.mux.HandleFunc("GET /api/github/setup", s.requireAuth(s.handleGitHubSetup))
	s.mux.HandleFunc("POST /webhooks/github", s.handleGitHubWebhook)

	// --- Terminal / exec ---
	s.mux.HandleFunc("POST /api/servers/{id}/exec", s.requireAuth(s.handleExec))

	// --- Container logs ---
	s.mux.HandleFunc("GET /api/servers/{id}/containers", s.requireAuth(s.handleListContainers))
	s.mux.HandleFunc("POST /api/servers/{id}/logs", s.requireAuth(s.handleStreamLogs))
	s.mux.HandleFunc("DELETE /api/servers/{id}/logs/{rid}", s.requireAuth(s.handleStopLogs))

	// --- Live (browser SSE) ---
	s.mux.HandleFunc("GET /api/events", s.requireAuth(s.handleEventStream))

	// --- Settings ---
	s.mux.HandleFunc("GET /api/settings", s.requireAuth(s.handleGetSettings))
	s.mux.HandleFunc("PUT /api/settings", s.requireAuth(s.handleUpdateSettings))

	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
}

// requireAuth wraps a handler with single-user session validation.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.auth.valid(bearer(r)) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
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
