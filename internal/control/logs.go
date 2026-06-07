package control

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/oyomworld/anchor/pkg/protocol"
)

// ---- request/reply waiter (for synchronous-style agent queries) ----

// awaitReply registers a one-shot waiter for an agent event with the given
// request id. The caller must call the returned cancel func when done.
func (s *Server) awaitReply(reqID string) (<-chan protocol.Event, func()) {
	ch := make(chan protocol.Event, 1)
	s.pendingMu.Lock()
	s.pending[reqID] = ch
	s.pendingMu.Unlock()
	return ch, func() {
		s.pendingMu.Lock()
		delete(s.pending, reqID)
		s.pendingMu.Unlock()
	}
}

// deliverReply routes an agent event to a waiting requester, if any.
func (s *Server) deliverReply(reqID string, evt protocol.Event) {
	s.pendingMu.Lock()
	ch, ok := s.pending[reqID]
	s.pendingMu.Unlock()
	if ok {
		select {
		case ch <- evt:
		default:
		}
	}
}

// ---- handlers ----

// handleListContainers asks the server's agent for its container list and
// returns it synchronously (with a short timeout).
func (s *Server) handleListContainers(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("id")
	if _, err := s.store.GetServer(serverID); err != nil {
		http.Error(w, "server not found", http.StatusNotFound)
		return
	}

	reqID := "lc_" + randToken()[:12]
	ch, cancel := s.awaitReply(reqID)
	defer cancel()

	payload, _ := json.Marshal(protocol.ListContainersRequest{RequestID: reqID})
	cmd := protocol.Command{ID: randToken()[:12], Type: protocol.CmdListContainers, Data: payload}
	if !s.hub.Send(serverID, cmd) {
		http.Error(w, "agent offline", http.StatusConflict)
		return
	}

	select {
	case evt := <-ch:
		var list protocol.ContainerList
		_ = json.Unmarshal(evt.Data, &list)
		writeJSON(w, http.StatusOK, list.Containers)
	case <-time.After(8 * time.Second):
		http.Error(w, "agent did not respond", http.StatusGatewayTimeout)
	case <-r.Context().Done():
	}
}

// validReqID reports whether a client-supplied request id is a safe topic key
// (used verbatim as the SSE topic and the agent request id).
func validReqID(s string) bool {
	if len(s) < 4 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-'
		if !ok {
			return false
		}
	}
	return true
}

// handleStreamLogs starts following a container's logs. Output streams to the
// browser over SSE on topic exec:<request_id>; call DELETE to stop the follow.
func (s *Server) handleStreamLogs(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("id")
	if _, err := s.store.GetServer(serverID); err != nil {
		http.Error(w, "server not found", http.StatusNotFound)
		return
	}
	var body struct {
		Container string `json:"container"`
		Tail      int    `json:"tail"`
		RequestID string `json:"request_id"`
	}
	if err := readJSON(r, &body); err != nil || body.Container == "" {
		http.Error(w, "container required", http.StatusBadRequest)
		return
	}
	if body.Tail == 0 {
		body.Tail = 200
	}

	// The client may supply the request_id so it can subscribe to the SSE topic
	// *before* the agent starts streaming — otherwise the tail backlog races
	// ahead of the subscriber and is lost (and an idle container then shows
	// nothing). Validate it's a safe topic key before trusting it.
	reqID := body.RequestID
	if !validReqID(reqID) {
		reqID = "log_" + randToken()[:12]
	}
	payload, _ := json.Marshal(protocol.StreamLogsRequest{
		RequestID: reqID, Container: body.Container, Tail: body.Tail,
	})
	cmd := protocol.Command{ID: randToken()[:12], Type: protocol.CmdStreamLogs, Data: payload}
	if !s.hub.Send(serverID, cmd) {
		http.Error(w, "agent offline", http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"request_id": reqID})
}

// handleStopLogs cancels a running log follow on the agent.
func (s *Server) handleStopLogs(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("id")
	reqID := r.PathValue("rid")
	payload, _ := json.Marshal(protocol.StopStreamRequest{RequestID: reqID})
	cmd := protocol.Command{ID: randToken()[:12], Type: protocol.CmdStopStream, Data: payload}
	s.hub.Send(serverID, cmd) // best-effort
	w.WriteHeader(http.StatusNoContent)
}
