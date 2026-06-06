// Command control-plane runs the Anchor central server: REST API, agent hub,
// GitHub webhooks, and (later) the embedded web UI.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
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

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[anchor] ")

	addr := env("ANCHOR_ADDR", ":8080")
	dbPath := env("ANCHOR_DB", "anchor.json")
	adminUser := env("ANCHOR_ADMIN_USER", "admin")
	adminPass := env("ANCHOR_ADMIN_PASS", "admin")

	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}

	srv, err := control.New(st, adminUser, adminPass)
	if err != nil {
		log.Fatalf("init control plane: %v", err)
	}
	srv.ServeSPA(os.Getenv("ANCHOR_WEB_DIR")) // serve built UI in production

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
