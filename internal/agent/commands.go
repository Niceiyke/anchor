package agent

import (
	"bufio"
	"context"
	"io"
	"os/exec"

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

// streamLogs tails docker logs for an app and forwards them as log events.
func (a *Agent) streamLogs(ctx context.Context, req protocol.StreamLogsRequest) {
	tail := "200"
	if req.Tail > 0 {
		tail = itoaPos(req.Tail)
	}
	cmd := exec.CommandContext(ctx, "docker", "logs", "--tail", tail, "-f", sanitize(req.AppName))
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
