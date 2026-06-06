package control

import (
	"bufio"
	"encoding/json"
	"net/http"
	"time"

	"github.com/oyomworld/anchor/internal/store"
	"github.com/oyomworld/anchor/pkg/protocol"
)

// authServer resolves the agent bearer token to a server, or writes 401.
func (s *Server) authServer(w http.ResponseWriter, r *http.Request) (store.Server, bool) {
	tok := bearer(r)
	if tok == "" {
		http.Error(w, "missing agent token", http.StatusUnauthorized)
		return store.Server{}, false
	}
	srv, err := s.store.GetServerByToken(tok)
	if err != nil {
		http.Error(w, "invalid agent token", http.StatusUnauthorized)
		return store.Server{}, false
	}
	return srv, true
}

// handleAgentStream holds a long-lived connection and streams newline-delimited
// JSON commands to the agent. The agent reconnects if it drops.
func (s *Server) handleAgentStream(w http.ResponseWriter, r *http.Request) {
	srv, ok := s.authServer(w, r)
	if !ok {
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	conn := s.hub.register(srv.ID)
	defer s.hub.unregister(conn)
	s.markOnline(srv.ID, true)
	defer s.markOnline(srv.ID, false)

	enc := json.NewEncoder(w)
	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case cmd, ok := <-conn.ch:
			if !ok {
				return // superseded by a newer connection
			}
			if err := enc.Encode(cmd); err != nil {
				return
			}
			flusher.Flush()
		case <-keepalive.C:
			// ping keeps proxies from closing the idle connection
			if err := enc.Encode(protocol.Command{Type: protocol.CmdPing}); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// handleAgentEvents ingests a batch of events posted by an agent.
func (s *Server) handleAgentEvents(w http.ResponseWriter, r *http.Request) {
	srv, ok := s.authServer(w, r)
	if !ok {
		return
	}
	dec := json.NewDecoder(bufio.NewReader(r.Body))
	for {
		var evt protocol.Event
		if err := dec.Decode(&evt); err != nil {
			break // EOF or end of batch
		}
		s.ingestEvent(srv.ID, evt)
	}
	w.WriteHeader(http.StatusNoContent)
}

// ingestEvent applies a single agent event to the store.
func (s *Server) ingestEvent(serverID string, evt protocol.Event) {
	switch evt.Type {
	case protocol.EvtSystemStats:
		var st protocol.SystemStats
		if json.Unmarshal(evt.Data, &st) != nil {
			return
		}
		srv, err := s.store.GetServer(serverID)
		if err != nil {
			return
		}
		srv.LastSeen = time.Now()
		srv.Stats = &store.Stats{
			CPUPercent: st.CPUPercent,
			MemUsed:    st.MemUsed,
			MemTotal:   st.MemTotal,
			DiskUsed:   st.DiskUsed,
			DiskTotal:  st.DiskTotal,
			Containers: st.Containers,
			UpdatedAt:  time.Now(),
		}
		_ = s.store.UpdateServer(srv)

	case protocol.EvtDeployStatus:
		var ds protocol.DeployStatus
		if json.Unmarshal(evt.Data, &ds) != nil {
			return
		}
		dep, err := s.store.GetDeployment(ds.DeploymentID)
		if err != nil {
			return
		}
		dep.Phase = string(ds.Phase)
		dep.Message = ds.Message
		if ds.StackType != "" {
			dep.StackType = ds.StackType
		}
		dep.UpdatedAt = time.Now()
		_ = s.store.UpdateDeployment(dep)
		s.broadcast(deploymentTopic(ds.DeploymentID), evt)

	case protocol.EvtLog:
		var ll protocol.LogLine
		if json.Unmarshal(evt.Data, &ll) != nil {
			return
		}
		if ll.DeploymentID != "" {
			if dep, err := s.store.GetDeployment(ll.DeploymentID); err == nil {
				dep.Logs = append(dep.Logs, store.LogLine{Stream: ll.Stream, Line: ll.Line, At: evt.Timestamp})
				if len(dep.Logs) > 5000 {
					dep.Logs = dep.Logs[len(dep.Logs)-5000:]
				}
				dep.UpdatedAt = time.Now()
				_ = s.store.UpdateDeployment(dep)
			}
			s.broadcast(deploymentTopic(ll.DeploymentID), evt)
		}

	case protocol.EvtCommandResult:
		s.broadcast("server:"+serverID, evt)
	}
}
