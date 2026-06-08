// Package agent implements the Anchor agent that runs on each VPS. It dials OUT
// to the control plane: a long-lived GET stream to receive commands, and POSTs
// to report logs, deploy status, and system stats.
package agent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log"
	"math/rand"
	"net/http"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/oyomworld/anchor/pkg/protocol"
)

type Config struct {
	ControlPlaneURL string // e.g. https://anchor.example.com
	Token           string // agent bearer token
	WorkDir         string // where apps are cloned/built, e.g. /var/lib/anchor/apps
	AgentID         string // server id (optional, for logging)
	CaddyDir        string // dir for per-app Caddy snippets (default /etc/anchor/caddy/apps)
	CaddyReload     string // shell command to reload Caddy
}

type Agent struct {
	cfg    Config
	client *http.Client
	events chan protocol.Event // outbound event buffer

	streamsMu sync.Mutex
	streams   map[string]context.CancelFunc // requestID -> cancel for active log follows

	shaOnce sync.Once
	sha     string // sha256 of the running binary (for auto-update), see update.go
}

func New(cfg Config) *Agent {
	return &Agent{
		cfg:     cfg,
		client:  &http.Client{}, // no timeout: stream is long-lived
		events:  make(chan protocol.Event, 256),
		streams: map[string]context.CancelFunc{},
	}
}

// Run blocks, maintaining the command stream and background reporters until ctx
// is cancelled. It reconnects automatically on failure.
func (a *Agent) Run(ctx context.Context) {
	go a.eventSender(ctx)
	go a.statsReporter(ctx)

	backoff := time.Second
	for ctx.Err() == nil {
		if err := a.connect(ctx); err != nil {
			// Add jitter (0–50% of backoff) to avoid thundering herd.
			jitter := time.Duration(rand.Int63n(int64(backoff) / 2))
			delay := backoff + jitter
			log.Printf("stream disconnected: %v (retry in %s)", err, delay)
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second
	}
}

// connect opens the command stream and dispatches commands until it closes.
func (a *Agent) connect(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", a.cfg.ControlPlaneURL+"/agent/v1/stream", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.cfg.Token)
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &httpError{resp.StatusCode}
	}
	log.Printf("connected to control plane %s", a.cfg.ControlPlaneURL)

	dec := json.NewDecoder(bufio.NewReader(resp.Body))
	for {
		var cmd protocol.Command
		if err := dec.Decode(&cmd); err != nil {
			return err
		}
		if cmd.Type == protocol.CmdPing {
			continue
		}
		if cmd.Type == protocol.CmdHello {
			go a.handleHello(cmd)
			continue
		}
		go a.dispatch(ctx, cmd)
	}
}

func (a *Agent) dispatch(ctx context.Context, cmd protocol.Command) {
	switch cmd.Type {
	case protocol.CmdDeploy:
		var req protocol.DeployRequest
		if err := json.Unmarshal(cmd.Data, &req); err != nil {
			log.Printf("bad deploy payload: %v", err)
			return
		}
		a.runDeploy(ctx, req)
	case protocol.CmdRunCommand:
		var req protocol.RunCommandRequest
		if err := json.Unmarshal(cmd.Data, &req); err != nil {
			return
		}
		a.runCommand(ctx, req)
	case protocol.CmdStreamLogs:
		var req protocol.StreamLogsRequest
		if err := json.Unmarshal(cmd.Data, &req); err != nil {
			return
		}
		a.streamLogs(ctx, req)
	case protocol.CmdStopStream:
		var req protocol.StopStreamRequest
		if err := json.Unmarshal(cmd.Data, &req); err != nil {
			return
		}
		a.stopStream(req)
	case protocol.CmdListContainers:
		var req protocol.ListContainersRequest
		if err := json.Unmarshal(cmd.Data, &req); err != nil {
			return
		}
		a.listContainers(ctx, req)
	case protocol.CmdProvisionDB:
		var req protocol.ProvisionDBRequest
		if err := json.Unmarshal(cmd.Data, &req); err != nil {
			return
		}
		a.provisionDB(ctx, req)
	case protocol.CmdRemoveDB:
		var req protocol.RemoveDBRequest
		if err := json.Unmarshal(cmd.Data, &req); err != nil {
			return
		}
		a.removeDB(ctx, req)
	case protocol.CmdStopApp:
		var req protocol.StopAppRequest
		if err := json.Unmarshal(cmd.Data, &req); err != nil {
			return
		}
		a.stopApp(ctx, req)
	case protocol.CmdContainerAction:
		var req protocol.ContainerActionRequest
		if err := json.Unmarshal(cmd.Data, &req); err != nil {
			return
		}
		a.containerAction(ctx, req)
	case protocol.CmdPruneContainers:
		var req protocol.PruneContainersRequest
		if err := json.Unmarshal(cmd.Data, &req); err != nil {
			return
		}
		a.pruneContainers(ctx, req)
	case protocol.CmdPruneImages:
		var req protocol.PruneImagesRequest
		if err := json.Unmarshal(cmd.Data, &req); err != nil {
			return
		}
		a.pruneImages(ctx, req)
	case protocol.CmdSystemPrune:
		var req protocol.SystemPruneRequest
		if err := json.Unmarshal(cmd.Data, &req); err != nil {
			return
		}
		a.systemPrune(ctx, req)
	case protocol.CmdBackupDB:
		var req protocol.BackupDBRequest
		if err := json.Unmarshal(cmd.Data, &req); err != nil {
			return
		}
		a.backupDB(ctx, req)
	case protocol.CmdUpdateAgent:
		var req protocol.UpdateAgentRequest
		if err := json.Unmarshal(cmd.Data, &req); err != nil {
			return
		}
		a.selfUpdate(ctx, req)
	default:
		log.Printf("unknown command type %q", cmd.Type)
	}
}

func (a *Agent) handleHello(cmd protocol.Command) {
	var hello protocol.Hello
	if err := json.Unmarshal(cmd.Data, &hello); err != nil {
		return
	}
	log.Printf("control plane protocol version %d (agent version %d)", hello.Version, protocol.ProtocolVersion)
	if hello.Version > protocol.ProtocolVersion {
		log.Printf("WARNING: control plane protocol is newer (%d > %d) — some commands may be unsupported",
			hello.Version, protocol.ProtocolVersion)
	}
	a.emit(protocol.EvtHello, protocol.Hello{
		Version: protocol.ProtocolVersion,
		Arch:    runtime.GOARCH,
		BinSHA:  a.selfSHA(),
	})
}

// ---- Outbound events -------------------------------------------------------

func (a *Agent) emit(t protocol.EventType, data any) {
	raw, _ := json.Marshal(data)
	evt := protocol.Event{AgentID: a.cfg.AgentID, Type: t, Timestamp: time.Now(), Data: raw}
	select {
	case a.events <- evt:
	default: // drop on overflow rather than block the deploy
	}
}

func (a *Agent) emitLog(deploymentID, requestID, stream, line string) {
	a.emit(protocol.EvtLog, protocol.LogLine{
		DeploymentID: deploymentID, RequestID: requestID, Stream: stream, Line: line,
	})
}

func (a *Agent) emitStatus(deploymentID string, phase protocol.DeployPhase, msg, stack string) {
	a.emit(protocol.EvtDeployStatus, protocol.DeployStatus{
		DeploymentID: deploymentID, Phase: phase, Message: msg, StackType: stack,
	})
}

// eventSender drains the event buffer and POSTs batches to the control plane.
func (a *Agent) eventSender(ctx context.Context) {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	var batch []protocol.Event
	flush := func() {
		if len(batch) == 0 {
			return
		}
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		for _, e := range batch {
			_ = enc.Encode(e)
		}
		batch = batch[:0]
		req, _ := http.NewRequestWithContext(ctx, "POST", a.cfg.ControlPlaneURL+"/agent/v1/events", &buf)
		req.Header.Set("Authorization", "Bearer "+a.cfg.Token)
		req.Header.Set("Content-Type", "application/x-ndjson")
		resp, err := a.client.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}
	for {
		select {
		case <-ctx.Done():
			flush()
			return
		case e := <-a.events:
			batch = append(batch, e)
			if len(batch) >= 64 {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}

type httpError struct{ code int }

func (e *httpError) Error() string { return "control plane returned status " + strconv.Itoa(e.code) }
