package agent

import (
	"context"
	"os/exec"
	"strconv"

	"github.com/oyomworld/anchor/pkg/protocol"
)

// provisionDB runs a managed database container (Postgres/Redis) with a
// persistent named volume on the anchor_net network, then reports status.
func (a *Agent) provisionDB(ctx context.Context, req protocol.ProvisionDBRequest) {
	a.emitDBStatus(req.DatabaseID, "provisioning", "Pulling image and starting container")

	// ensure shared network + volume exist (idempotent)
	_ = exec.CommandContext(ctx, "docker", "network", "create", anchorNetwork).Run()
	_ = exec.CommandContext(ctx, "docker", "volume", "create", req.Volume).Run()
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", req.Container).Run()

	args := []string{"run", "-d", "--name", req.Container, "--restart", "unless-stopped",
		"--network", anchorNetwork, "--label", "anchor.db=" + req.DatabaseID}

	switch req.Engine {
	case "postgres":
		if req.HostPort > 0 {
			args = append(args, "-p", strconv.Itoa(req.HostPort)+":5432")
		}
		args = append(args,
			"-e", "POSTGRES_USER="+req.Username,
			"-e", "POSTGRES_PASSWORD="+req.Password,
			"-e", "POSTGRES_DB="+req.DBName,
			"-v", req.Volume+":/var/lib/postgresql/data",
			"postgres:"+req.Version,
		)
	case "redis":
		if req.HostPort > 0 {
			args = append(args, "-p", strconv.Itoa(req.HostPort)+":6379")
		}
		args = append(args,
			"-v", req.Volume+":/data",
			"redis:"+req.Version,
			"redis-server", "--requirepass", req.Password, "--appendonly", "yes",
		)
	default:
		a.emitDBStatus(req.DatabaseID, "failed", "unsupported engine: "+req.Engine)
		return
	}

	if out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput(); err != nil {
		a.emitDBStatus(req.DatabaseID, "failed", "docker run: "+string(out))
		return
	}
	a.emitDBStatus(req.DatabaseID, "running", "Database is running")
}

// removeDB stops and removes a database container (and optionally its volume).
func (a *Agent) removeDB(ctx context.Context, req protocol.RemoveDBRequest) {
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", req.Container).Run()
	if req.DeleteVolume && req.Volume != "" {
		_ = exec.CommandContext(ctx, "docker", "volume", "rm", req.Volume).Run()
	}
	a.emitDBStatus(req.DatabaseID, "removed", "Database removed")
}

func (a *Agent) emitDBStatus(id, status, msg string) {
	a.emit(protocol.EvtDBStatus, protocol.DBStatus{DatabaseID: id, Status: status, Message: msg})
}

func (a *Agent) backupDB(ctx context.Context, req protocol.BackupDBRequest) {
	var cmd *exec.Cmd
	switch req.Engine {
	case "postgres":
		cmd = exec.CommandContext(ctx, "docker", "exec", req.Container,
			"pg_dump", "-U", req.Username, "-d", req.DBName, "--no-owner", "--no-acl", "--clean")
	case "redis":
		cmd = exec.CommandContext(ctx, "docker", "exec", req.Container,
			"redis-cli", "--rdb", "/tmp/dump.rdb", "SAVE")
	default:
		a.emit(protocol.EvtBackupResult, protocol.BackupResult{
			RequestID: req.RequestID, DatabaseID: req.DatabaseID, Error: "unsupported engine: " + req.Engine,
		})
		return
	}
	out, err := cmd.Output()
	if err != nil {
		a.emit(protocol.EvtBackupResult, protocol.BackupResult{
			RequestID: req.RequestID, DatabaseID: req.DatabaseID, Error: "backup: " + err.Error(),
		})
		return
	}
	a.emit(protocol.EvtBackupResult, protocol.BackupResult{
		RequestID: req.RequestID, DatabaseID: req.DatabaseID, Size: int64(len(out)), Data: string(out),
	})
}
