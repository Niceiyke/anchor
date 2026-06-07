package agent

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/oyomworld/anchor/pkg/protocol"
)

// runDeploy executes the full build-on-target pipeline for one deployment:
// clone/pull -> detect stack -> build -> run -> configure Caddy.
func (a *Agent) runDeploy(ctx context.Context, req protocol.DeployRequest) {
	depID := req.DeploymentID
	appDir := filepath.Join(a.cfg.WorkDir, req.AppName)

	a.emitStatus(depID, protocol.PhaseCloning, "Fetching source", "")
	if err := a.fetchSource(ctx, req, appDir); err != nil {
		a.fail(depID, "clone failed", err)
		return
	}

	a.emitStatus(depID, protocol.PhaseDetecting, "Detecting stack", "")
	stack := detectStack(appDir)
	if stack == stackUnknown {
		a.fail(depID, "no Dockerfile or docker-compose found", nil)
		return
	}
	a.emitLog(depID, "", "system", "Detected stack: "+string(stack))
	a.emitStatus(depID, protocol.PhaseBuilding, "Building image", string(stack))

	switch stack {
	case stackCompose:
		if err := a.deployCompose(ctx, req, appDir); err != nil {
			a.fail(depID, "compose deploy failed", err)
			return
		}
	case stackDockerfile:
		if err := a.deployDockerfile(ctx, req, appDir); err != nil {
			a.fail(depID, "dockerfile deploy failed", err)
			return
		}
	}

	a.emitStatus(depID, protocol.PhaseConfiguring, "Configuring routing", string(stack))
	if req.Domain != "" {
		if err := a.configureCaddy(ctx, req); err != nil {
			a.emitLog(depID, "", "system", "WARN: caddy config failed: "+err.Error())
		}
	}

	a.checkHealth(ctx, req)
	a.emitStatus(depID, protocol.PhaseSuccess, "Deployment successful", string(stack))
}

func (a *Agent) fail(depID, msg string, err error) {
	if err != nil {
		a.emitLog(depID, "", "stderr", msg+": "+err.Error())
	} else {
		a.emitLog(depID, "", "stderr", msg)
	}
	a.emitStatus(depID, protocol.PhaseFailed, msg, "")
}

// fetchSource clones the repo fresh (or pulls if it already exists) at the
// requested commit/branch, using the git token for private repos.
func (a *Agent) fetchSource(ctx context.Context, req protocol.DeployRequest, appDir string) error {
	cloneURL := req.RepoURL
	if req.GitToken != "" && strings.HasPrefix(cloneURL, "https://") {
		cloneURL = "https://x-access-token:" + req.GitToken + "@" + strings.TrimPrefix(cloneURL, "https://")
	}

	if _, err := os.Stat(filepath.Join(appDir, ".git")); err == nil {
		if err := a.run(ctx, req.DeploymentID, appDir, "git", "remote", "set-url", "origin", cloneURL); err != nil {
			return err
		}
		if err := a.run(ctx, req.DeploymentID, appDir, "git", "fetch", "origin", req.Branch); err != nil {
			return err
		}
		target := "origin/" + req.Branch
		if req.CommitSHA != "" {
			target = req.CommitSHA
		}
		return a.run(ctx, req.DeploymentID, appDir, "git", "reset", "--hard", target)
	}

	_ = os.MkdirAll(filepath.Dir(appDir), 0o755)
	if err := a.run(ctx, req.DeploymentID, "", "git", "clone", "--branch", req.Branch, "--depth", "1", cloneURL, appDir); err != nil {
		return err
	}
	if req.CommitSHA != "" {
		// best effort: checkout the exact commit if the shallow clone has it
		_ = a.run(ctx, req.DeploymentID, appDir, "git", "checkout", req.CommitSHA)
	}
	return nil
}

type stackType string

const (
	stackUnknown    stackType = "unknown"
	stackCompose    stackType = "compose"
	stackDockerfile stackType = "dockerfile"
)

// detectStack inspects the repo root. Compose takes precedence over a bare
// Dockerfile because it can describe a multi-service stack (Go + Caddy + DB).
func detectStack(dir string) stackType {
	for _, name := range []string{"docker-compose.yml", "docker-compose.yaml", "compose.yml", "compose.yaml"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return stackCompose
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "Dockerfile")); err == nil {
		return stackDockerfile
	}
	return stackUnknown
}

// deployCompose writes an env file and runs `docker compose up -d --build`.
func (a *Agent) deployCompose(ctx context.Context, req protocol.DeployRequest, appDir string) error {
	if err := writeEnvFile(appDir, req.EnvVars); err != nil {
		return err
	}
	project := sanitize(req.AppName)
	a.emitStatus(req.DeploymentID, protocol.PhaseStarting, "Starting services", string(stackCompose))
	return a.run(ctx, req.DeploymentID, appDir, "docker", "compose", "-p", project, "up", "-d", "--build", "--remove-orphans")
}

// deployDockerfile builds an image and runs a single container, replacing any
// previous container for this app.
func (a *Agent) deployDockerfile(ctx context.Context, req protocol.DeployRequest, appDir string) error {
	name := sanitize(req.AppName)
	image := "anchor/" + name + ":latest"

	if err := a.run(ctx, req.DeploymentID, appDir, "docker", "build", "-t", image, "."); err != nil {
		return err
	}

	a.emitStatus(req.DeploymentID, protocol.PhaseStarting, "Starting container", string(stackDockerfile))
	_ = a.run(ctx, req.DeploymentID, "", "docker", "rm", "-f", name)

	args := []string{"run", "-d", "--name", name, "--restart", "unless-stopped",
		"--label", "anchor.app=" + name, "--network", anchorNetwork}
	for k, v := range req.EnvVars {
		args = append(args, "-e", k+"="+v)
	}
	// expose the container port on the docker network; Caddy reaches it by name
	args = append(args, image)

	// ensure the shared network exists (idempotent)
	_ = a.run(ctx, req.DeploymentID, "", "docker", "network", "create", anchorNetwork)
	return a.run(ctx, req.DeploymentID, appDir, "docker", args...)
}

const anchorNetwork = "anchor_net"

// writeEnvFile renders a .env file consumed by docker compose.
func writeEnvFile(dir string, env map[string]string) error {
	if len(env) == 0 {
		return nil
	}
	var b strings.Builder
	for k, v := range env {
		fmt.Fprintf(&b, "%s=%s\n", k, v)
	}
	return os.WriteFile(filepath.Join(dir, ".env"), []byte(b.String()), 0o600)
}

// sanitize makes a string safe for use as a docker name/project.
func sanitize(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	return strings.Trim(b.String(), "-_")
}

// run executes a command, streaming combined output as log events.
func (a *Agent) run(ctx context.Context, depID, dir, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	a.emitLog(depID, "", "system", "$ "+name+" "+strings.Join(args, " "))

	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		return err
	}
	go a.pipeLogs(depID, "stdout", stdout)
	a.pipeLogs(depID, "stderr", stderr)
	return cmd.Wait()
}

func (a *Agent) pipeLogs(depID, stream string, r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		a.emitLog(depID, "", stream, sc.Text())
	}
}

// checkHealth verifies the deployed container is running and its port is
// reachable on the docker network. A failure is emitted as a warning log but
// does not fail the deployment — the app may need a moment to bind.
func (a *Agent) checkHealth(ctx context.Context, req protocol.DeployRequest) {
	container := sanitize(req.AppName)
	out, err := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Running}}", container).Output()
	if err != nil || strings.TrimSpace(string(out)) != "true" {
		a.emitLog(req.DeploymentID, "", "system", "WARN: container health check failed: container not running")
		return
	}
	target := fmt.Sprintf("%s:%d", container, req.ContainerPort)
	// best-effort TCP check (requires nc/netcat in the agent image)
	status, err := exec.CommandContext(ctx, "sh", "-c",
		"command -v nc >/dev/null && nc -z -w3 "+strings.Fields(target)[0]+" "+
			strings.Fields(target)[1]+" 2>/dev/null && echo ok || echo fail").Output()
	if err != nil || strings.TrimSpace(string(status)) != "ok" {
		a.emitLog(req.DeploymentID, "", "system",
			"WARN: port check skipped or failed on "+target+" (app may still be starting)")
		return
	}
	a.emitLog(req.DeploymentID, "", "system", "Health check passed: "+target+" is reachable")
}
