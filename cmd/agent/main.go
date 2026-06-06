// Command agent runs on each VPS. It dials out to the Anchor control plane to
// receive deploy/run/log commands and report status.
//
// Configure via environment variables:
//
//	ANCHOR_URL        control plane base URL (required), e.g. https://anchor.example.com
//	ANCHOR_TOKEN      agent bearer token from the control plane (required)
//	ANCHOR_WORKDIR    app build dir            (default /var/lib/anchor/apps)
//	ANCHOR_CADDY_DIR  caddy snippet dir        (default /etc/anchor/caddy/apps)
//	ANCHOR_CADDY_RELOAD  caddy reload command  (default docker exec caddy caddy reload ...)
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/oyomworld/anchor/internal/agent"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("[anchor-agent] ")

	cfg := agent.Config{
		ControlPlaneURL: os.Getenv("ANCHOR_URL"),
		Token:           os.Getenv("ANCHOR_TOKEN"),
		WorkDir:         env("ANCHOR_WORKDIR", "/var/lib/anchor/apps"),
		CaddyDir:        env("ANCHOR_CADDY_DIR", "/etc/anchor/caddy/apps"),
		CaddyReload:     os.Getenv("ANCHOR_CADDY_RELOAD"),
	}
	if cfg.ControlPlaneURL == "" || cfg.Token == "" {
		log.Fatal("ANCHOR_URL and ANCHOR_TOKEN are required")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-stop
		log.Println("shutting down agent...")
		cancel()
	}()

	a := agent.New(cfg)
	log.Printf("starting agent -> %s", cfg.ControlPlaneURL)
	a.Run(ctx)
}
