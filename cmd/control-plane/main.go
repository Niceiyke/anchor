// Command control-plane runs the Anchor central server: REST API, agent hub,
// GitHub webhooks, and (later) the embedded web UI.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/oyomworld/anchor/internal/control"
	"github.com/oyomworld/anchor/internal/store"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// loadSecretKey returns the 32-byte AES key used to encrypt secrets at rest.
// Preference: ANCHOR_SECRET_KEY env (any string, hashed to 32 bytes). Otherwise
// a key file next to the DB is used (created on first run). Keep this key safe
// and stable — losing it means re-connecting GitHub.
func loadSecretKey(dbPath string) []byte {
	if v := os.Getenv("ANCHOR_SECRET_KEY"); v != "" {
		sum := sha256.Sum256([]byte(v))
		return sum[:]
	}
	keyPath := filepath.Join(filepath.Dir(dbPath), "anchor.key")
	if b, err := os.ReadFile(keyPath); err == nil && len(b) == 32 {
		return b
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		log.Fatalf("generate secret key: %v", err)
	}
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		log.Printf("warning: could not persist secret key to %s: %v", keyPath, err)
	} else {
		log.Printf("generated encryption key at %s (set ANCHOR_SECRET_KEY to manage it yourself)", keyPath)
	}
	return key
}

// openStore picks the backend by file extension: .json -> JSON file store,
// anything else -> SQLite (the durable default).
func openStore(path string) (store.Store, error) {
	if strings.HasSuffix(path, ".json") {
		log.Printf("using JSON store at %s", path)
		return store.Open(path)
	}
	log.Printf("using SQLite store at %s", path)
	return store.OpenSQLite(path)
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[anchor] ")

	addr := env("ANCHOR_ADDR", ":8080")
	dbPath := env("ANCHOR_DB", "anchor.db")
	adminUser := env("ANCHOR_ADMIN_USER", "admin")
	adminPass := env("ANCHOR_ADMIN_PASS", "admin")

	st, err := openStore(dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}

	// Encrypt sensitive settings (GitHub keys/secrets) at rest.
	st, err = store.NewEncrypted(st, loadSecretKey(dbPath))
	if err != nil {
		log.Fatalf("init encryption: %v", err)
	}

	srv, err := control.New(st, adminUser, adminPass)
	if err != nil {
		log.Fatalf("init control plane: %v", err)
	}
	srv.ServeSPA(os.Getenv("ANCHOR_WEB_DIR")) // serve built UI in production

	// Optional: auto-register a co-located agent (all-in-one compose stack).
	if tok := os.Getenv("ANCHOR_BOOTSTRAP_AGENT_TOKEN"); tok != "" {
		if err := srv.EnsureLocalServer(env("ANCHOR_BOOTSTRAP_SERVER_NAME", "this-vps"), tok); err != nil {
			log.Printf("bootstrap local server: %v", err)
		}
	}

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           withCORS(srv.Handler()),
		ReadHeaderTimeout: 10 * time.Second,
		// no WriteTimeout: long-lived streams (agent + SSE) must stay open
	}

	go func() {
		log.Printf("control plane listening on %s", addr)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Println("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(ctx)
}

// withCORS allows the Vite dev server to call the API during development.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Credentials", "true")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		}
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
