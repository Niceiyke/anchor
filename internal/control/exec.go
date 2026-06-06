package control

import (
	"encoding/json"
	"net/http"

	"github.com/oyomworld/anchor/pkg/protocol"
)

// handleExec dispatches a shell command to a server's agent. Output streams back
// to the browser over SSE on topic "exec:<request_id>" (subscribe BEFORE or
// right after this returns — early lines are also persisted? no: exec output is
// live-only, so the UI opens the SSE stream first, then POSTs here).
func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	serverID := r.PathValue("id")
	if _, err := s.store.GetServer(serverID); err != nil {
		http.Error(w, "server not found", http.StatusNotFound)
		return
	}
	var body struct {
		Command string `json:"command"`
		WorkDir string `json:"work_dir"`
	}
	if err := readJSON(r, &body); err != nil || body.Command == "" {
		http.Error(w, "command required", http.StatusBadRequest)
		return
	}

	reqID := "exec_" + randToken()[:12]
	payload, _ := json.Marshal(protocol.RunCommandRequest{
		RequestID: reqID, Command: body.Command, WorkDir: body.WorkDir,
	})
	cmd := protocol.Command{ID: randToken()[:12], Type: protocol.CmdRunCommand, Data: payload}

	if !s.hub.Send(serverID, cmd) {
		http.Error(w, "agent offline", http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"request_id": reqID})
}

func execTopic(reqID string) string { return "exec:" + reqID }
