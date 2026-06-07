package agent

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/oyomworld/anchor/pkg/protocol"
)

// attachComposeToNetwork makes a Compose app reachable by Anchor's Caddy without
// the user editing their compose file. Compose puts services on their own
// network (e.g. <project>_default) and names containers <project>-<service>-N,
// but Caddy proxies to "<app>:<port>" on anchor_net. So after `compose up` we
// connect the web container to anchor_net with the <app> alias.
func (a *Agent) attachComposeToNetwork(ctx context.Context, req protocol.DeployRequest) {
	project := sanitize(req.AppName)
	_ = exec.CommandContext(ctx, "docker", "network", "create", anchorNetwork).Run() // idempotent

	// Containers belonging to this compose project.
	out, err := exec.CommandContext(ctx, "docker", "ps", "-q",
		"--filter", "label=com.docker.compose.project="+project).Output()
	if err != nil {
		a.emitLog(req.DeploymentID, "", "system", "WARN: could not list compose containers: "+err.Error())
		return
	}
	ids := strings.Fields(strings.TrimSpace(string(out)))
	if len(ids) == 0 {
		a.emitLog(req.DeploymentID, "", "system", "WARN: no containers found for compose project "+project)
		return
	}

	target := a.pickWebContainer(ctx, ids, req.ContainerPort)
	if target == "" {
		if len(ids) == 1 {
			target = ids[0]
		} else {
			a.emitLog(req.DeploymentID, "", "system", fmt.Sprintf(
				"WARN: %d services and none expose port %d — can't tell which to route to. "+
					"Add `expose: [\"%d\"]` to your web service and redeploy.",
				len(ids), req.ContainerPort, req.ContainerPort))
			return
		}
	}

	cmd := exec.CommandContext(ctx, "docker", "network", "connect", "--alias", project, anchorNetwork, target)
	if out, err := cmd.CombinedOutput(); err != nil {
		if !strings.Contains(string(out), "already") { // already connected on a redeploy reuse
			a.emitLog(req.DeploymentID, "", "system", "WARN: attach to "+anchorNetwork+" failed: "+strings.TrimSpace(string(out)))
			return
		}
	}
	short := target
	if len(short) > 12 {
		short = short[:12]
	}
	a.emitLog(req.DeploymentID, "", "system", fmt.Sprintf("Routed %s → %s:%d on %s", req.Domain, project, req.ContainerPort, anchorNetwork)+" (container "+short+")")
}

// pickWebContainer returns the id of the container that exposes the app's
// container port, or "" if none do (caller decides the fallback).
func (a *Agent) pickWebContainer(ctx context.Context, ids []string, port int) string {
	for _, id := range ids {
		out, err := exec.CommandContext(ctx, "docker", "inspect", "-f",
			"{{range $p, $_ := .Config.ExposedPorts}}{{$p}} {{end}}", id).Output()
		if err == nil && portExposed(string(out), port) {
			return id
		}
	}
	return ""
}

// portExposed reports whether docker's space-separated exposed-ports list
// (e.g. "7111/tcp 8080/tcp") contains port/tcp.
func portExposed(exposed string, port int) bool {
	want := fmt.Sprintf("%d/tcp", port)
	for _, f := range strings.Fields(exposed) {
		if f == want {
			return true
		}
	}
	return false
}
