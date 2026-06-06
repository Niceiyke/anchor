package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

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
func (a *Agent) configureCaddy(ctx context.Context, req protocol.DeployRequest) error {
	dir := a.cfg.CaddyDir
	if dir == "" {
		dir = "/etc/anchor/caddy/apps"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	upstream := fmt.Sprintf("%s:%d", sanitize(req.AppName), req.ContainerPort)
	snippet := fmt.Sprintf("%s {\n\treverse_proxy %s\n}\n", req.Domain, upstream)
	path := filepath.Join(dir, sanitize(req.AppName)+".caddy")
	if err := os.WriteFile(path, []byte(snippet), 0o644); err != nil {
		return err
	}
	a.emitLog(req.DeploymentID, "", "system", "Wrote Caddy route: "+req.Domain+" -> "+upstream)

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
