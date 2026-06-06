// Package protocol defines the message types exchanged between the Anchor
// control plane and the agents that run on each VPS.
//
// Transport: the agent dials OUT to the control plane (NAT/firewall friendly).
//   - Receive commands: long-lived GET /agent/v1/stream  (newline-delimited JSON Commands)
//   - Send results:     POST /agent/v1/events            (one Event per request)
package protocol

import (
	"encoding/json"
	"time"
)

// ---- Control plane -> Agent ------------------------------------------------

type CommandType string

const (
	CmdDeploy     CommandType = "deploy"
	CmdRunCommand CommandType = "run_command"
	CmdStreamLogs CommandType = "stream_logs"
	CmdStopApp    CommandType = "stop_app"
	CmdPing       CommandType = "ping"
)

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
}

// RunCommandRequest is the payload for CmdRunCommand.
type RunCommandRequest struct {
	RequestID string `json:"request_id"`
	Command   string `json:"command"`
	WorkDir   string `json:"work_dir"`
}

// StreamLogsRequest is the payload for CmdStreamLogs.
type StreamLogsRequest struct {
	RequestID string `json:"request_id"`
	AppName   string `json:"app_name"`
	Tail      int    `json:"tail"`
}

// StopAppRequest is the payload for CmdStopApp.
type StopAppRequest struct {
	AppName string `json:"app_name"`
}

// ---- Agent -> Control plane ------------------------------------------------

type EventType string

const (
	EvtLog           EventType = "log"
	EvtDeployStatus  EventType = "deploy_status"
	EvtSystemStats   EventType = "system_stats"
	EvtCommandResult EventType = "command_result"
)

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
