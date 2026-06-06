package control

import (
	"sync"

	"github.com/oyomworld/anchor/pkg/protocol"
)

// agentConn is a live command stream to one agent.
type agentConn struct {
	serverID string
	ch       chan protocol.Command // buffered outbound commands
}

// Hub tracks which agents are currently connected and routes commands to them.
type Hub struct {
	mu    sync.RWMutex
	conns map[string]*agentConn // serverID -> conn
}

func NewHub() *Hub {
	return &Hub{conns: map[string]*agentConn{}}
}

// register creates a connection slot for a server and returns it. Any previous
// connection for the same server is dropped.
func (h *Hub) register(serverID string) *agentConn {
	h.mu.Lock()
	defer h.mu.Unlock()
	if old, ok := h.conns[serverID]; ok {
		close(old.ch)
	}
	c := &agentConn{serverID: serverID, ch: make(chan protocol.Command, 32)}
	h.conns[serverID] = c
	return c
}

// unregister removes a connection if it is still the active one.
func (h *Hub) unregister(c *agentConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cur, ok := h.conns[c.serverID]; ok && cur == c {
		delete(h.conns, c.serverID)
	}
}

// Online reports whether a server currently has a live agent stream.
func (h *Hub) Online(serverID string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.conns[serverID]
	return ok
}

// Send dispatches a command to a server's agent. Returns false if offline.
func (h *Hub) Send(serverID string, cmd protocol.Command) bool {
	h.mu.RLock()
	c, ok := h.conns[serverID]
	h.mu.RUnlock()
	if !ok {
		return false
	}
	select {
	case c.ch <- cmd:
		return true
	default:
		return false // backpressure: agent not draining
	}
}
