package agent

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/oyomworld/anchor/pkg/protocol"
)

// routeTarget is the resolved destination of a deploy: the container Anchor
// health-checks, and the host:port Caddy proxies public traffic to. For the
// Dockerfile stack host==container==<app>; for Compose it's the selected
// service's container, reachable on anchor_net under the <project> alias.
type routeTarget struct {
	container string // container id or name (for health gating); "" if undecidable
	host      string // network alias / container name Caddy proxies to
	port      int    // upstream port
}

// composeContainer describes one running container of a Compose project with
// the metadata Anchor needs to route and health-check it.
type composeContainer struct {
	id      string
	service string // com.docker.compose.service label
	ports   []int  // exposed tcp ports (image EXPOSE / compose `expose:`)
}

// composeContainers returns the running containers of a Compose project. It is
// the single source of truth for "what's in this project" — routing and health
// gating both build on it, so they can never disagree about the container set.
func (a *Agent) composeContainers(ctx context.Context, project string) ([]composeContainer, error) {
	out, err := exec.CommandContext(ctx, "docker", "ps", "-q",
		"--filter", "label=com.docker.compose.project="+project).Output()
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(strings.TrimSpace(string(out)))
	if len(ids) == 0 {
		return nil, nil
	}
	// One line per container: "<id> <service> <port/proto> <port/proto> ...".
	const format = `{{.Id}} {{index .Config.Labels "com.docker.compose.service"}}` +
		` {{range $p, $_ := .Config.ExposedPorts}}{{$p}} {{end}}`
	args := append([]string{"inspect", "-f", format}, ids...)
	insp, err := exec.CommandContext(ctx, "docker", args...).Output()
	if err != nil {
		return nil, err
	}
	var cs []composeContainer
	for _, line := range strings.Split(strings.TrimSpace(string(insp)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] == "" {
			continue
		}
		c := composeContainer{id: fields[0]}
		if len(fields) >= 2 {
			c.service = fields[1]
		}
		for _, p := range fields[2:] {
			if n := parseTCPPort(p); n != 0 {
				c.ports = append(c.ports, n)
			}
		}
		cs = append(cs, c)
	}
	return cs, nil
}

// resolveComposeRoute discovers the project's containers, selects the one to
// publish, derives its upstream port, and (when a domain is set) attaches it to
// anchor_net under the project alias so Caddy can reach it by name.
//
// A returned target with container=="" means the choice was ambiguous; the
// caller emits guidance but does not fail the deploy (we can't health-check or
// route what we can't identify, but the stack itself is up).
func (a *Agent) resolveComposeRoute(ctx context.Context, req protocol.DeployRequest) (routeTarget, error) {
	project := sanitize(req.AppName)
	cs, err := a.composeContainers(ctx, project)
	if err != nil {
		return routeTarget{}, fmt.Errorf("list compose containers: %w", err)
	}
	if len(cs) == 0 {
		return routeTarget{}, fmt.Errorf("no running containers for compose project %s", project)
	}

	target, ok := selectComposeTarget(cs, req.Service, req.ContainerPort)
	if !ok {
		a.emitLog(req.DeploymentID, "", "system", fmt.Sprintf(
			"WARN: %d services in this project and none clearly match — can't tell which to publish. "+
				"Set the app's \"service\" to the web service's name (e.g. %q), or set its port to one a service listens on.",
			len(cs), cs[0].service))
		return routeTarget{}, nil
	}

	port, adjusted := resolveUpstreamPort(target, req.ContainerPort)
	if adjusted {
		a.emitLog(req.DeploymentID, "", "system", fmt.Sprintf(
			"Note: service %q does not expose port %d; routing to the port it does expose (%d).",
			target.service, req.ContainerPort, port))
	}
	rt := routeTarget{container: target.id, host: project, port: port}

	if req.Domain != "" {
		if port == 0 {
			a.emitLog(req.DeploymentID, "", "system", fmt.Sprintf(
				"WARN: could not determine a port for service %q — set the app's port so Caddy can reach it.",
				target.service))
		}
		if err := a.attachToNetwork(ctx, target.id, project); err != nil {
			a.emitLog(req.DeploymentID, "", "system", "WARN: attach to "+anchorNetwork+" failed: "+err.Error())
		} else {
			a.emitLog(req.DeploymentID, "", "system", fmt.Sprintf(
				"Routed %s → %s:%d on %s (service %q, container %s)",
				req.Domain, project, port, anchorNetwork, target.service, short12(target.id)))
		}
	}
	return rt, nil
}

// attachToNetwork connects a container to anchor_net under the given alias so
// Caddy can reach it by name. Idempotent: a redeploy that reuses the container
// (already connected) is not an error.
func (a *Agent) attachToNetwork(ctx context.Context, container, alias string) error {
	_ = exec.CommandContext(ctx, "docker", "network", "create", anchorNetwork).Run() // idempotent
	cmd := exec.CommandContext(ctx, "docker", "network", "connect", "--alias", alias, anchorNetwork, container)
	if out, err := cmd.CombinedOutput(); err != nil {
		if strings.Contains(string(out), "already") {
			return nil
		}
		return fmt.Errorf("%s", strings.TrimSpace(string(out)))
	}
	return nil
}

// selectComposeTarget picks which container of a Compose project to publish and
// health-check. Preference order:
//  1. the named service (most robust — no port guessing);
//  2. the single service that exposes the requested port;
//  3. the only service, when the project has just one.
//
// ok is false when the choice is ambiguous (multiple services, no name, and no
// port match), so the caller can ask the user to disambiguate.
func selectComposeTarget(cs []composeContainer, service string, port int) (composeContainer, bool) {
	if service != "" {
		for _, c := range cs {
			if c.service == service {
				return c, true
			}
		}
		return composeContainer{}, false
	}
	if port != 0 {
		for _, c := range cs {
			if containsPort(c.ports, port) {
				return c, true
			}
		}
	}
	if len(cs) == 1 {
		return cs[0], true
	}
	return composeContainer{}, false
}

// resolveUpstreamPort returns the port Caddy should proxy to, and whether it had
// to adjust away from the configured value. The configured port wins when the
// chosen container actually exposes it. Otherwise, if the container exposes
// exactly one port, that port is used — this both fills in an unset port and
// corrects a stale/wrong configured one. When neither applies the configured
// port is kept unchanged (it may still be the right one for images that don't
// declare EXPOSE).
func resolveUpstreamPort(c composeContainer, configured int) (port int, adjusted bool) {
	if configured != 0 && containsPort(c.ports, configured) {
		return configured, false
	}
	if len(c.ports) == 1 {
		return c.ports[0], configured != 0 && configured != c.ports[0]
	}
	return configured, false
}

func containsPort(ports []int, p int) bool {
	for _, x := range ports {
		if x == p {
			return true
		}
	}
	return false
}

// parseTCPPort parses a docker exposed-port spec ("8080/tcp") to its number,
// returning 0 for non-tcp ("53/udp") or unparsable input. A bare number is
// treated as tcp.
func parseTCPPort(s string) int {
	num, proto := s, "tcp"
	if i := strings.IndexByte(s, '/'); i >= 0 {
		num, proto = s[:i], s[i+1:]
	}
	if proto != "tcp" {
		return 0
	}
	n, err := strconv.Atoi(num)
	if err != nil {
		return 0
	}
	return n
}
