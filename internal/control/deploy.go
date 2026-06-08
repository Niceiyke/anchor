package control

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/oyomworld/anchor/internal/store"
	"github.com/oyomworld/anchor/pkg/protocol"
)

// triggerDeploy creates a Deployment record and dispatches a deploy command to
// the app's target server. Returns the new deployment.
func (s *Server) triggerDeploy(app store.App, commitSHA string) (store.Deployment, error) {
	if !s.tryLockDeploy(app.ID) {
		return store.Deployment{}, fmt.Errorf("deployment already in progress for %s", app.Name)
	}
	defer s.unlockDeploy(app.ID)

	dep := store.Deployment{
		ID:        "dep_" + randToken()[:12],
		AppID:     app.ID,
		CommitSHA: commitSHA,
		Branch:    app.Branch,
		Phase:     string(protocol.PhaseQueued),
		Message:   "Deployment queued",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := s.store.CreateDeployment(dep); err != nil {
		return dep, err
	}

	req := protocol.DeployRequest{
		DeploymentID:      dep.ID,
		AppID:             app.ID,
		AppName:           app.Name,
		RepoURL:           app.RepoURL,
		Branch:            app.Branch,
		CommitSHA:         commitSHA,
		GitToken:          s.githubCloneToken(),
		Domain:            app.Domain,
		ContainerPort:     app.ContainerPort,
		EnvVars:           app.EnvVars,
		ComposeFile:       app.ComposeFile,
		HealthPath:        app.HealthPath,
		HealthTimeoutSecs: app.HealthTimeoutSecs,
	}
	payload, _ := json.Marshal(req)
	cmd := protocol.Command{ID: randToken()[:12], Type: protocol.CmdDeploy, Data: payload}

	if !s.hub.Send(app.ServerID, cmd) {
		dep.Phase = string(protocol.PhaseFailed)
		dep.Message = "Target server agent is offline"
		dep.UpdatedAt = time.Now()
		_ = s.store.UpdateDeployment(dep)
		return dep, fmt.Errorf("agent for server %s is offline", app.ServerID)
	}
	return dep, nil
}

// maybeAutoRollback redeploys an app's last known-good commit after a failed
// deployment, when the app has auto-rollback enabled. The LastGoodSHA != failed
// commit guard means a rollback deploy that itself fails won't loop (its commit
// IS the last-good one).
func (s *Server) maybeAutoRollback(failed store.Deployment) {
	app, err := s.store.GetApp(failed.AppID)
	if err != nil || !app.AutoRollback {
		return
	}
	if app.LastGoodSHA == "" || app.LastGoodSHA == failed.CommitSHA {
		return // nothing healthy to roll back to (or this already was it)
	}
	s.appendDeployLog(failed.ID, "Auto-rollback: redeploying last good commit "+short(app.LastGoodSHA))
	rb, err := s.triggerDeploy(app, app.LastGoodSHA)
	if err != nil {
		s.appendDeployLog(failed.ID, "Auto-rollback could not start: "+err.Error())
		return
	}
	log.Printf("app %s: auto-rollback %s -> %s (deployment %s)",
		app.ID, short(failed.CommitSHA), short(app.LastGoodSHA), rb.ID)
}

// appendDeployLog records a system log line on a deployment and streams it to
// any live viewers (used for control-plane-originated messages like rollback).
func (s *Server) appendDeployLog(depID, line string) {
	_ = s.store.AppendDeploymentLog(depID, store.LogLine{Stream: "system", Line: line, At: time.Now()})
	raw, _ := json.Marshal(protocol.LogLine{DeploymentID: depID, Stream: "system", Line: line})
	s.broadcast(deploymentTopic(depID), protocol.Event{Type: protocol.EvtLog, Timestamp: time.Now(), Data: raw})
}

// tryLockDeploy acquires the per-app deploy gate. Returns false if a deploy is
// already running for that app.
func (s *Server) tryLockDeploy(appID string) bool {
	s.deployLocksMu.Lock()
	defer s.deployLocksMu.Unlock()
	if _, busy := s.deployLocks[appID]; busy {
		return false
	}
	s.deployLocks[appID] = struct{}{}
	return true
}

func (s *Server) unlockDeploy(appID string) {
	s.deployLocksMu.Lock()
	delete(s.deployLocks, appID)
	s.deployLocksMu.Unlock()
}
