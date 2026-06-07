package control

import (
	"encoding/json"
	"fmt"
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
		DeploymentID:  dep.ID,
		AppID:         app.ID,
		AppName:       app.Name,
		RepoURL:       app.RepoURL,
		Branch:        app.Branch,
		CommitSHA:     commitSHA,
		GitToken:      s.githubCloneToken(),
		Domain:        app.Domain,
		ContainerPort: app.ContainerPort,
		EnvVars:       app.EnvVars,
		ComposeFile:   app.ComposeFile,
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
