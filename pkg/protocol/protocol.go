// Package protocol defines the message types exchanged between the Anchor
// control plane and the agents that run on each VPS.
//
// Transport: the agent dials OUT to the control plane (NAT/firewall friendly).
//   - Receive commands: long-lived GET /agent/v1/stream  (newline-delimited JSON Commands)
//   - Send results:     POST /agent/v1/events            (one Event per request)
package protocol

import (
	"encoding/json"
	"strings"
	"time"
)

// ProtocolVersion is incremented on breaking changes to the agent protocol.
const ProtocolVersion = 1

// Sanitize converts an app name into a docker-safe container / compose-project
// name. Shared by the agent (which names containers) and the control plane
// (which derives the expected container name to report running status).
func Sanitize(s string) string {
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

// ---- Control plane -> Agent ------------------------------------------------

type CommandType string

const (
	CmdDeploy          CommandType = "deploy"
	CmdRunCommand      CommandType = "run_command"
	CmdStreamLogs      CommandType = "stream_logs"
	CmdStopStream      CommandType = "stop_stream"
	CmdListContainers  CommandType = "list_containers"
	CmdStopApp         CommandType = "stop_app"
	CmdContainerAction CommandType = "container_action"
	CmdPruneContainers CommandType = "prune_containers"
	CmdPruneImages     CommandType = "prune_images"
	CmdSystemPrune     CommandType = "system_prune"
	CmdProvisionDB     CommandType = "provision_db"
	CmdRemoveDB        CommandType = "remove_db"
	CmdBackupDB        CommandType = "backup_db"
	CmdUpdateAgent     CommandType = "update_agent"
	CmdPing            CommandType = "ping"
	CmdHello           CommandType = "hello"
)

// UpdateAgentRequest tells the agent to replace its own binary with the one the
// control plane serves at /agent/download. SHA256 is the expected hash of the
// new binary so the agent can verify the download before swapping.
type UpdateAgentRequest struct {
	SHA256 string `json:"sha256"`
}

// Command is a single instruction pushed to an agent over the stream.
type Command struct {
	ID   string          `json:"id"`
	Type CommandType     `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

// DeployRequest is the payload for CmdDeploy.
type DeployRequest struct {
	DeploymentID  string            `json:"deployment_id"`
	AppID         string            `json:"app_id"`
	AppName       string            `json:"app_name"`
	RepoURL       string            `json:"repo_url"` // https clone URL
	Branch        string            `json:"branch"`
	CommitSHA     string            `json:"commit_sha"`
	GitToken      string            `json:"git_token"` // ephemeral, used for clone only
	Domain        string            `json:"domain"`    // public domain for Caddy routing
	ContainerPort int               `json:"container_port"`
	EnvVars       map[string]string `json:"env_vars"`
	ComposeFile   string            `json:"compose_file,omitempty"` // explicit -f path; "" = auto-detect

	// Health gating. After the app starts, the agent waits for it to become
	// healthy before reporting success; an unhealthy app fails the deploy (and
	// the control plane may auto-roll-back). HealthPath, when set, is probed over
	// HTTP inside the container (http://localhost:<port><path>).
	HealthPath        string `json:"health_path,omitempty"`
	HealthTimeoutSecs int    `json:"health_timeout_secs,omitempty"` // 0 = default (45s)
}

// RunCommandRequest is the payload for CmdRunCommand.
type RunCommandRequest struct {
	RequestID string `json:"request_id"`
	Command   string `json:"command"`
	WorkDir   string `json:"work_dir"`
}

// StreamLogsRequest is the payload for CmdStreamLogs. Container takes precedence;
// AppName is the fallback (resolved to the app's container name).
type StreamLogsRequest struct {
	RequestID string `json:"request_id"`
	Container string `json:"container"`
	AppName   string `json:"app_name"`
	Tail      int    `json:"tail"`
}

// StopStreamRequest cancels an in-flight log follow (CmdStopStream).
type StopStreamRequest struct {
	RequestID string `json:"request_id"`
}

// ListContainersRequest is the payload for CmdListContainers.
type ListContainersRequest struct {
	RequestID string `json:"request_id"`
}

// StopAppRequest is the payload for CmdStopApp.
type StopAppRequest struct {
	AppName string `json:"app_name"`
}

// ContainerActionRequest is the payload for CmdContainerAction. The reply is an
// EvtCommandResult carrying the exit code and any output. Action is one of
// start | stop | restart | remove.
type ContainerActionRequest struct {
	RequestID string `json:"request_id"`
	Container string `json:"container"`
	Action    string `json:"action"`
}

// PruneContainersRequest is the payload for CmdPruneContainers. It removes all
// stopped containers (docker container prune). The reply is an EvtCommandResult
// whose output is docker's prune summary.
type PruneContainersRequest struct {
	RequestID string `json:"request_id"`
}

// PruneImagesRequest is the payload for CmdPruneImages. By default it removes
// only dangling images; All also removes images not used by any container
// (docker image prune -a). The reply is an EvtCommandResult.
type PruneImagesRequest struct {
	RequestID string `json:"request_id"`
	All       bool   `json:"all"`
}

// SystemPruneRequest is the payload for CmdSystemPrune (docker system prune):
// stopped containers, unused networks, dangling images and build cache. Volumes
// are never touched (they hold managed-database data). The reply is an
// EvtCommandResult.
type SystemPruneRequest struct {
	RequestID string `json:"request_id"`
}

// ProvisionDBRequest is the payload for CmdProvisionDB. The control plane
// computes the container name and credentials; the agent runs the container.
type ProvisionDBRequest struct {
	DatabaseID string `json:"database_id"`
	Container  string `json:"container"` // docker container + in-network hostname
	Volume     string `json:"volume"`    // named docker volume for persistence
	Engine     string `json:"engine"`    // postgres | redis
	Version    string `json:"version"`   // image tag
	Username   string `json:"username"`  // postgres
	Password   string `json:"password"`
	DBName     string `json:"db_name"`   // postgres database name
	HostPort   int    `json:"host_port"` // 0 = internal only (anchor_net)
}

// RemoveDBRequest is the payload for CmdRemoveDB.
type RemoveDBRequest struct {
	DatabaseID   string `json:"database_id"`
	Container    string `json:"container"`
	Volume       string `json:"volume"`
	DeleteVolume bool   `json:"delete_volume"`
}

// BackupDBRequest is the payload for CmdBackupDB.
type BackupDBRequest struct {
	RequestID  string `json:"request_id"`
	DatabaseID string `json:"database_id"`
	Engine     string `json:"engine"`
	Container  string `json:"container"`
	Username   string `json:"username"`
	DBName     string `json:"db_name"`
}

// BackupResult is the payload for EvtBackupResult.
type BackupResult struct {
	RequestID  string `json:"request_id"`
	DatabaseID string `json:"database_id"`
	Size       int64  `json:"size"`
	Data       string `json:"data"` // base64-encoded dump file
	Error      string `json:"error,omitempty"`
}

// ---- Agent -> Control plane ------------------------------------------------

type EventType string

const (
	EvtLog           EventType = "log"
	EvtDeployStatus  EventType = "deploy_status"
	EvtSystemStats   EventType = "system_stats"
	EvtCommandResult EventType = "command_result"
	EvtContainerList EventType = "container_list"
	EvtDBStatus      EventType = "db_status"
	EvtHello         EventType = "hello"
	EvtBackupResult  EventType = "backup_result"
)

// Hello is the payload for CmdHello / EvtHello — sent by the control plane
// on stream connect, answered by the agent with its own version. The agent's
// reply also carries the architecture and SHA-256 of its running binary so the
// control plane can offer an auto-update when it has a newer bundled binary.
type Hello struct {
	Version int    `json:"version"`
	Arch    string `json:"arch,omitempty"`    // agent reply: runtime.GOARCH (amd64|arm64)
	BinSHA  string `json:"bin_sha,omitempty"` // agent reply: sha256 of the running binary
}

// Event is a single message reported by an agent.
type Event struct {
	AgentID   string          `json:"agent_id"`
	Type      EventType       `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data"`
}

// LogLine is the payload for EvtLog.
type LogLine struct {
	DeploymentID string `json:"deployment_id,omitempty"`
	RequestID    string `json:"request_id,omitempty"`
	Stream       string `json:"stream"` // stdout | stderr | system
	Line         string `json:"line"`
}

type DeployPhase string

const (
	PhaseQueued      DeployPhase = "queued"
	PhaseCloning     DeployPhase = "cloning"
	PhaseDetecting   DeployPhase = "detecting"
	PhaseBuilding    DeployPhase = "building"
	PhaseStarting    DeployPhase = "starting"
	PhaseConfiguring DeployPhase = "configuring"
	PhaseHealthCheck DeployPhase = "health_check"
	PhaseSuccess     DeployPhase = "success"
	PhaseFailed      DeployPhase = "failed"
)

// DeployStatus is the payload for EvtDeployStatus.
type DeployStatus struct {
	DeploymentID string      `json:"deployment_id"`
	Phase        DeployPhase `json:"phase"`
	Message      string      `json:"message"`
	StackType    string      `json:"stack_type,omitempty"` // dockerfile | compose
}

// CommandResult is the payload for EvtCommandResult.
type CommandResult struct {
	RequestID string `json:"request_id"`
	ExitCode  int    `json:"exit_code"`
	Output    string `json:"output"`
}

// ContainerInfo describes one container reported by the agent.
type ContainerInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Image  string `json:"image"`
	State  string `json:"state"`  // running | exited | ...
	Status string `json:"status"` // "Up 3 hours", "Exited (0) 5m ago"
}

// ContainerList is the payload for EvtContainerList.
type ContainerList struct {
	RequestID  string          `json:"request_id"`
	Containers []ContainerInfo `json:"containers"`
}

// DBStatus is the payload for EvtDBStatus.
type DBStatus struct {
	DatabaseID string `json:"database_id"`
	Status     string `json:"status"` // provisioning | running | failed | removed
	Message    string `json:"message"`
}

// SystemStats is the payload for EvtSystemStats.
type SystemStats struct {
	CPUPercent float64    `json:"cpu_percent"`
	MemUsed    uint64     `json:"mem_used"`
	MemTotal   uint64     `json:"mem_total"`
	DiskUsed   uint64     `json:"disk_used"`
	DiskTotal  uint64     `json:"disk_total"`
	UptimeSecs float64    `json:"uptime_seconds"`
	Containers int        `json:"containers"`
	LoadAvg    [3]float64 `json:"load_avg"`
}
