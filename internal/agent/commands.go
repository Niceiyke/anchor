package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os/exec"
	"strings"

	"github.com/oyomworld/anchor/pkg/protocol"
)

// runCommand executes an arbitrary shell command, streaming its output live as
// log events (tagged with the request id) and a final result with the exit code.
func (a *Agent) runCommand(ctx context.Context, req protocol.RunCommandRequest) {
	cmd := exec.CommandContext(ctx, "sh", "-c", req.Command)
	if req.WorkDir != "" {
		cmd.Dir = req.WorkDir
	}
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		a.emitLog("", req.RequestID, "stderr", err.Error())
		a.emit(protocol.EvtCommandResult, protocol.CommandResult{RequestID: req.RequestID, ExitCode: -1})
		return
	}
	go a.pipeReqLogs(req.RequestID, "stdout", stdout)
	a.pipeReqLogs(req.RequestID, "stderr", stderr)

	exit := 0
	if err := cmd.Wait(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			exit = -1
		}
	}
	a.emit(protocol.EvtCommandResult, protocol.CommandResult{RequestID: req.RequestID, ExitCode: exit})
}

// streamLogs follows docker logs for a container and forwards each line as a
// log event tagged with the request id. The follow is cancellable via
// stopStream (registered in a.streams). A final command_result marks the end.
func (a *Agent) streamLogs(ctx context.Context, req protocol.StreamLogsRequest) {
	target := req.Container
	if target == "" {
		target = sanitize(req.AppName)
	}
	tail := "200"
	if req.Tail > 0 {
		tail = itoaPos(req.Tail)
	}

	sctx, cancel := context.WithCancel(ctx)
	a.streamsMu.Lock()
	a.streams[req.RequestID] = cancel
	a.streamsMu.Unlock()
	defer func() {
		cancel()
		a.streamsMu.Lock()
		delete(a.streams, req.RequestID)
		a.streamsMu.Unlock()
		a.emit(protocol.EvtCommandResult, protocol.CommandResult{RequestID: req.RequestID, ExitCode: 0})
	}()

	cmd := exec.CommandContext(sctx, "docker", "logs", "--tail", tail, "--timestamps", "-f", target)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		a.emitLog("", req.RequestID, "stderr", "logs: "+err.Error())
		return
	}
	go a.pipeReqLogs(req.RequestID, "stdout", stdout)
	a.pipeReqLogs(req.RequestID, "stderr", stderr)
	_ = cmd.Wait()
}

// stopStream cancels an active log follow.
func (a *Agent) stopStream(req protocol.StopStreamRequest) {
	a.streamsMu.Lock()
	if cancel, ok := a.streams[req.RequestID]; ok {
		cancel()
	}
	a.streamsMu.Unlock()
}

// listContainers reports the docker containers on this host.
func (a *Agent) listContainers(ctx context.Context, req protocol.ListContainersRequest) {
	out, err := exec.CommandContext(ctx, "docker", "ps", "-a", "--no-trunc", "--format", "{{json .}}").Output()
	list := protocol.ContainerList{RequestID: req.RequestID, Containers: []protocol.ContainerInfo{}}
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var row struct {
				ID     string `json:"ID"`
				Names  string `json:"Names"`
				Image  string `json:"Image"`
				State  string `json:"State"`
				Status string `json:"Status"`
			}
			if json.Unmarshal([]byte(line), &row) != nil {
				continue
			}
			id := row.ID
			if len(id) > 12 {
				id = id[:12]
			}
			list.Containers = append(list.Containers, protocol.ContainerInfo{
				ID:     id,
				Name:   strings.TrimPrefix(row.Names, "/"),
				Image:  row.Image,
				State:  row.State,
				Status: row.Status,
			})
		}
	}
	a.emit(protocol.EvtContainerList, list)
}

// containerAction performs a lifecycle operation on a single container and
// replies with an EvtCommandResult (exit code + combined output).
func (a *Agent) containerAction(ctx context.Context, req protocol.ContainerActionRequest) {
	var args []string
	switch req.Action {
	case "start":
		args = []string{"start", req.Container}
	case "stop":
		args = []string{"stop", req.Container}
	case "restart":
		args = []string{"restart", req.Container}
	case "remove":
		args = []string{"rm", "-f", req.Container}
	default:
		a.emit(protocol.EvtCommandResult, protocol.CommandResult{
			RequestID: req.RequestID, ExitCode: -1, Output: "unknown action: " + req.Action,
		})
		return
	}
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			exit = -1
		}
	}
	a.emit(protocol.EvtCommandResult, protocol.CommandResult{
		RequestID: req.RequestID, ExitCode: exit, Output: strings.TrimSpace(string(out)),
	})
}

// pruneContainers removes all stopped containers and replies with docker's
// summary (which containers were deleted and space reclaimed).
func (a *Agent) pruneContainers(ctx context.Context, req protocol.PruneContainersRequest) {
	out, err := exec.CommandContext(ctx, "docker", "container", "prune", "-f").CombinedOutput()
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			exit = -1
		}
	}
	a.emit(protocol.EvtCommandResult, protocol.CommandResult{
		RequestID: req.RequestID, ExitCode: exit, Output: strings.TrimSpace(string(out)),
	})
}

// pruneImages removes dangling images (or all unused images when All is set)
// and replies with docker's summary.
func (a *Agent) pruneImages(ctx context.Context, req protocol.PruneImagesRequest) {
	args := []string{"image", "prune", "-f"}
	if req.All {
		args = append(args, "-a")
	}
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			exit = -1
		}
	}
	a.emit(protocol.EvtCommandResult, protocol.CommandResult{
		RequestID: req.RequestID, ExitCode: exit, Output: strings.TrimSpace(string(out)),
	})
}

// stopApp stops and removes an app's container(s).
func (a *Agent) stopApp(ctx context.Context, req protocol.StopAppRequest) {
	name := sanitize(req.AppName)
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", name).Run()
	_ = exec.CommandContext(ctx, "docker", "compose", "-p", name, "down").Run()
}

func (a *Agent) pipeReqLogs(requestID, stream string, r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		a.emitLog("", requestID, stream, sc.Text())
	}
}

func itoaPos(n int) string {
	if n <= 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
