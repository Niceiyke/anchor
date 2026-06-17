package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/oyomworld/anchor/pkg/protocol"
)

// configureCaddy writes a per-app reverse-proxy snippet and reloads Caddy.
//
// Model: Caddy and the app containers share the `anchor_net` Docker network, so
// Caddy can reach the app by its container name. Caddy handles HTTPS via
// on-demand/automatic TLS for the configured domain.
//
// The snippet directory and reload command are configurable so the same agent
// works whether Caddy runs on the host or as a container.
func (a *Agent) configureCaddy(ctx context.Context, req protocol.DeployRequest, routes []resolvedRoute) error {
	dir := a.cfg.CaddyDir
	if dir == "" {
		dir = "/etc/anchor/caddy/apps"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// One file per app holds all its route blocks, so the snippet is rewritten
	// atomically on every deploy (old routes can't linger). on_demand TLS lets
	// Caddy obtain a cert for each domain on first request, gated by the control
	// plane's /tls/check ask endpoint — so auto-assigned subdomains (and custom
	// domains) get HTTPS without pre-provisioning.
	var b strings.Builder
	for _, r := range routes {
		upstream := fmt.Sprintf("%s:%d", r.host, r.port)
		fmt.Fprintf(&b, "%s {\n\treverse_proxy %s\n\ttls {\n\t\ton_demand\n\t}\n}\n", r.domain, upstream)
		a.emitLog(req.DeploymentID, "", "system", "Wrote Caddy route: "+r.domain+" -> "+upstream)
	}
	path := filepath.Join(dir, sanitize(req.AppName)+".caddy")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return err
	}

	return a.reloadCaddy(ctx, req.DeploymentID)
}

func (a *Agent) reloadCaddy(ctx context.Context, depID string) error {
	reload := a.cfg.CaddyReload
	if reload == "" {
		// default assumes Caddy runs as a container named "caddy"
		reload = "docker exec caddy caddy reload --config /etc/caddy/Caddyfile"
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", reload)
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		a.emitLog(depID, "", "system", string(out))
	}
	return err
}
