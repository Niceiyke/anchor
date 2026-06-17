package agent

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/oyomworld/anchor/pkg/protocol"
)

// healthTarget is one container the health gate must clear: its container, the
// port to probe, an optional HTTP path, and a label for log messages.
type healthTarget struct {
	container string
	port      int
	path      string
	label     string // service / app name, for logs
}

// gateHealth blocks until every target looks healthy, or returns an error once
// the (shared) timeout elapses. A returned error fails the deployment (and may
// trigger an auto-rollback on the control plane). With no targets it returns
// nil — we don't fail a deploy we can't evaluate.
//
// Targets are checked in order; for a multi-service app this means each routed
// service is gated, so a crash-looping or failing secondary service fails the
// deploy too.
func (a *Agent) gateHealth(ctx context.Context, req protocol.DeployRequest, targets []healthTarget) error {
	if len(targets) == 0 {
		a.emitLog(req.DeploymentID, "", "system", "WARN: could not identify app container; skipping health gate")
		return nil
	}
	timeout := time.Duration(req.HealthTimeoutSecs) * time.Second
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	for _, t := range targets {
		if err := a.gateOne(ctx, req, t, timeout); err != nil {
			if len(targets) > 1 {
				return fmt.Errorf("%s: %w", t.label, err)
			}
			return err
		}
	}
	return nil
}

// gateOne waits up to timeout for a single container to become healthy.
func (a *Agent) gateOne(ctx context.Context, req protocol.DeployRequest, t healthTarget, timeout time.Duration) error {
	container, port := t.container, t.port
	if port == 0 {
		port = req.ContainerPort
	}
	deadline := time.Now().Add(timeout)
	a.emitLog(req.DeploymentID, "", "system",
		fmt.Sprintf("Health check: waiting up to %s for %s (%s) to become healthy", timeout, t.label, short12(container)))

	var lastErr error
	for {
		st, err := inspectState(ctx, container)
		switch {
		case err != nil:
			lastErr = err
		case st.health == "healthy":
			a.emitLog(req.DeploymentID, "", "system", t.label+": healthy (docker healthcheck)")
			return a.httpProbe(ctx, req, container, port, t.path)
		case st.health == "unhealthy":
			lastErr = fmt.Errorf("container reports unhealthy")
		case st.health == "starting":
			lastErr = fmt.Errorf("container healthcheck still starting")
		case !st.running:
			lastErr = fmt.Errorf("container not running (status=%s, restarts=%d)", st.status, st.restarts)
		case st.restarting:
			lastErr = fmt.Errorf("container is restarting (crash loop?)")
		default:
			// Running, no docker healthcheck defined. An optional HTTP path probe
			// is the only remaining gate; if it passes (or none is set), we're good.
			if perr := a.httpProbe(ctx, req, container, port, t.path); perr == nil {
				a.emitLog(req.DeploymentID, "", "system", t.label+": healthy")
				return nil
			} else {
				lastErr = perr
			}
		}

		if time.Now().After(deadline) {
			return lastErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// httpProbe checks path over HTTP from inside the container. It's best-effort:
// if the image ships neither wget nor curl, it warns and passes (container-state
// gating still applies). A non-2xx/conn error fails the gate.
func (a *Agent) httpProbe(ctx context.Context, req protocol.DeployRequest, container string, port int, path string) error {
	if path == "" {
		return nil
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	url := fmt.Sprintf("http://localhost:%d%s", port, path)
	script := fmt.Sprintf(
		`if command -v wget >/dev/null 2>&1; then wget -q -T 5 -O /dev/null %q; `+
			`elif command -v curl >/dev/null 2>&1; then curl -fsS -m 5 -o /dev/null %q; `+
			`else echo NO_HTTP_TOOL >&2; exit 3; fi`, url, url)
	out, err := exec.CommandContext(ctx, "docker", "exec", container, "sh", "-c", script).CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "NO_HTTP_TOOL") {
			a.emitLog(req.DeploymentID, "", "system",
				"WARN: no wget/curl in container — skipping HTTP health path probe of "+url)
			return nil
		}
		return fmt.Errorf("HTTP health check %s failed", url)
	}
	a.emitLog(req.DeploymentID, "", "system", "Health check passed: "+url)
	return nil
}

type containerState struct {
	running    bool
	restarting bool
	restarts   int
	status     string // created|running|restarting|exited|...
	health     string // healthy|unhealthy|starting|none
}

func inspectState(ctx context.Context, container string) (containerState, error) {
	const format = `{{.State.Running}}|{{.State.Restarting}}|{{.RestartCount}}|{{.State.Status}}|` +
		`{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}`
	out, err := exec.CommandContext(ctx, "docker", "inspect", "-f", format, container).Output()
	if err != nil {
		return containerState{}, fmt.Errorf("inspect %s: %w", short12(container), err)
	}
	parts := strings.Split(strings.TrimSpace(string(out)), "|")
	if len(parts) != 5 {
		return containerState{}, fmt.Errorf("unexpected inspect output for %s", short12(container))
	}
	restarts, _ := strconv.Atoi(parts[2])
	return containerState{
		running:    parts[0] == "true",
		restarting: parts[1] == "true",
		restarts:   restarts,
		status:     parts[3],
		health:     parts[4],
	}, nil
}

func short12(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
