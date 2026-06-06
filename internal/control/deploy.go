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
