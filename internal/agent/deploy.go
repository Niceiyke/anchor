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
	if req.ComposeFile != "" {
		// An explicit compose file was chosen (it may live in a subdirectory the
		// root auto-detect wouldn't see), so force the compose path.
		stack = stackCompose
		a.emitLog(depID, "", "system", "Using compose file: "+req.ComposeFile)
	}
	if stack == stackUnknown {
		a.fail(depID, "no Dockerfile or docker-compose found", nil)
		return
	}
	a.emitLog(depID, "", "system", "Detected stack: "+string(stack))
	a.emitStatus(depID, protocol.PhaseBuilding, "Building image", string(stack))

	var target routeTarget
	switch stack {
	case stackCompose:
		t, err := a.deployCompose(ctx, req, appDir)
		if err != nil {
			a.fail(depID, "compose deploy failed", err)
			return
		}
		target = t
	case stackDockerfile:
		if err := a.deployDockerfile(ctx, req, appDir); err != nil {
			a.fail(depID, "dockerfile deploy failed", err)
			return
		}
		name := sanitize(req.AppName)
		target = routeTarget{container: name, host: name, port: req.ContainerPort}
	}

	a.emitStatus(depID, protocol.PhaseConfiguring, "Configuring routing", string(stack))
	if req.Domain != "" {
		host, port := target.host, target.port
		if host == "" {
			host = sanitize(req.AppName)
		}
		if port == 0 {
			port = req.ContainerPort
		}
		if err := a.configureCaddy(ctx, req, host, port); err != nil {
			a.emitLog(depID, "", "system", "WARN: caddy config failed: "+err.Error())
		}
	}

	a.emitStatus(depID, protocol.PhaseHealthCheck, "Checking health", string(stack))
	if err := a.gateHealth(ctx, req, target); err != nil {
		a.fail(depID, "health check failed", err)
		return
	}
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

// deployCompose writes an env file and runs `docker compose up -d --build`,
// then resolves which service to publish/health-check. When req.ComposeFile is
// set it's passed via -f; Compose then treats that file's directory as the
// project root, so the .env is written there.
func (a *Agent) deployCompose(ctx context.Context, req protocol.DeployRequest, appDir string) (routeTarget, error) {
	args := []string{"compose"}
	envDir := appDir
	if req.ComposeFile != "" {
		args = append(args, "-f", req.ComposeFile)
		envDir = filepath.Dir(filepath.Join(appDir, req.ComposeFile))
	}
	if err := writeEnvFile(envDir, req.EnvVars); err != nil {
		return routeTarget{}, err
	}
	if len(req.EnvVars) > 0 {
		// Compose only uses .env for ${VAR} interpolation — it does not inject
		// these into containers. Surface that so users don't expect otherwise.
		a.emitLog(req.DeploymentID, "", "system",
			"Note: env vars were written to .env for Compose interpolation (${VAR}). "+
				"A service receives them only if it references them via `environment:` or `env_file:`.")
	}
	project := sanitize(req.AppName)
	args = append(args, "-p", project, "up", "-d", "--build", "--remove-orphans")
	a.emitStatus(req.DeploymentID, protocol.PhaseStarting, "Starting services", string(stackCompose))
	if err := a.run(ctx, req.DeploymentID, appDir, "docker", args...); err != nil {
		return routeTarget{}, err
	}
	// Pick the web service, derive its port, and attach it to anchor_net so
	// Caddy can reach it by name — all without the user editing their compose.
	return a.resolveComposeRoute(ctx, req)
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
// Values are double-quoted so that newlines, '#', and other special characters
// are passed through literally. Backslashes and double quotes inside the value
// are escaped so the quoting round-trips correctly.
func writeEnvFile(dir string, env map[string]string) error {
	if len(env) == 0 {
		return nil
	}
	var b strings.Builder
	for k, v := range env {
		escaped := strings.ReplaceAll(v, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		fmt.Fprintf(&b, "%s=\"%s\"\n", k, escaped)
	}
	return os.WriteFile(filepath.Join(dir, ".env"), []byte(b.String()), 0o600)
}

// sanitize makes a string safe for use as a docker name/project. It delegates
// to protocol.Sanitize so the control plane derives the same container name.
func sanitize(s string) string {
	return protocol.Sanitize(s)
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
