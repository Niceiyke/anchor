package control

import (
	"encoding/json"
	"net/http"
	"regexp"
	"time"

	"github.com/oyomworld/anchor/pkg/protocol"
)

// containerActions is the set of lifecycle operations the agent accepts.
var containerActions = map[string]bool{
	"start": true, "stop": true, "restart": true, "remove": true,
}

// validContainerRef matches a Docker container name or id. Names are
// [a-zA-Z0-9][a-zA-Z0-9_.-]+; ids are hex. This blocks flag/option injection
// into the docker CLI (e.g. a leading dash).
var validContainerRef = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// handleContainerAction performs start/stop/restart/remove on a container on
// the given server's agent and returns the result synchronously.
func (s *Server) handleContainerAction(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("id")
	name := r.PathValue("name")
	action := r.PathValue("action")

	if _, err := s.store.GetServer(serverID); err != nil {
		http.Error(w, "server not found", http.StatusNotFound)
		return
	}
	if !containerActions[action] {
		http.Error(w, "unknown action", http.StatusBadRequest)
		return
	}
	if !validContainerRef.MatchString(name) {
		http.Error(w, "invalid container name", http.StatusBadRequest)
		return
	}

	reqID := "ca_" + randToken()[:12]
	ch, cancel := s.awaitReply(reqID)
	defer cancel()

	payload, _ := json.Marshal(protocol.ContainerActionRequest{
		RequestID: reqID, Container: name, Action: action,
	})
	cmd := protocol.Command{ID: randToken()[:12], Type: protocol.CmdContainerAction, Data: payload}
	if !s.hub.Send(serverID, cmd) {
		http.Error(w, "agent offline", http.StatusConflict)
		return
	}

	select {
	case evt := <-ch:
		var res protocol.CommandResult
		_ = json.Unmarshal(evt.Data, &res)
		if res.ExitCode != 0 {
			msg := res.Output
			if msg == "" {
				msg = "container action failed"
			}
			http.Error(w, msg, http.StatusBadGateway)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"action": action, "container": name, "output": res.Output})
	case <-time.After(30 * time.Second):
		http.Error(w, "agent did not respond", http.StatusGatewayTimeout)
	case <-r.Context().Done():
	}
}

// handlePruneContainers removes all stopped containers on the server's agent
// and returns docker's prune summary.
func (s *Server) handlePruneContainers(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("id")
	if _, err := s.store.GetServer(serverID); err != nil {
		http.Error(w, "server not found", http.StatusNotFound)
		return
	}

	reqID := "cp_" + randToken()[:12]
	ch, cancel := s.awaitReply(reqID)
	defer cancel()

	payload, _ := json.Marshal(protocol.PruneContainersRequest{RequestID: reqID})
	cmd := protocol.Command{ID: randToken()[:12], Type: protocol.CmdPruneContainers, Data: payload}
	if !s.hub.Send(serverID, cmd) {
		http.Error(w, "agent offline", http.StatusConflict)
		return
	}

	s.awaitPruneResult(w, r, ch, "prune failed")
}

// handlePruneImages removes dangling images on the server's agent (or all
// unused images when ?all=true) and returns docker's prune summary.
func (s *Server) handlePruneImages(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("id")
	if _, err := s.store.GetServer(serverID); err != nil {
		http.Error(w, "server not found", http.StatusNotFound)
		return
	}
	all := r.URL.Query().Get("all") == "true"

	reqID := "ip_" + randToken()[:12]
	ch, cancel := s.awaitReply(reqID)
	defer cancel()

	payload, _ := json.Marshal(protocol.PruneImagesRequest{RequestID: reqID, All: all})
	cmd := protocol.Command{ID: randToken()[:12], Type: protocol.CmdPruneImages, Data: payload}
	if !s.hub.Send(serverID, cmd) {
		http.Error(w, "agent offline", http.StatusConflict)
		return
	}

	s.awaitPruneResult(w, r, ch, "prune failed")
}

// awaitPruneResult blocks on a prune reply and writes the docker summary, or an
// error/timeout. Shared by the container and image prune handlers.
func (s *Server) awaitPruneResult(w http.ResponseWriter, r *http.Request, ch <-chan protocol.Event, failMsg string) {
	select {
	case evt := <-ch:
		var res protocol.CommandResult
		_ = json.Unmarshal(evt.Data, &res)
		if res.ExitCode != 0 {
			msg := res.Output
			if msg == "" {
				msg = failMsg
			}
			http.Error(w, msg, http.StatusBadGateway)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"output": res.Output})
	case <-time.After(30 * time.Second):
		http.Error(w, "agent did not respond", http.StatusGatewayTimeout)
	case <-r.Context().Done():
	}
}
