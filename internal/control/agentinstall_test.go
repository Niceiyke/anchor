package control

import (
	"encoding/json"
	"testing"

	"github.com/oyomworld/anchor/pkg/protocol"
)

func TestMaybeUpdateAgent(t *testing.T) {
	amd64SHA, ok := bundledAgentSHA("amd64")
	if !ok {
		t.Skip("no bundled agent binary (run `make agent-embed`)")
	}

	// drain reads any command dispatched to the registered agent, or "" if none.
	drain := func(c *agentConn) protocol.CommandType {
		select {
		case cmd := <-c.ch:
			return cmd.Type
		default:
			return ""
		}
	}

	t.Run("stale binary triggers update", func(t *testing.T) {
		s, _ := newTestServer(t)
		c := s.hub.register("srv1")
		s.maybeUpdateAgent("srv1", protocol.Hello{Arch: "amd64", BinSHA: "deadbeef"})
		if got := drain(c); got != protocol.CmdUpdateAgent {
			t.Fatalf("expected update command, got %q", got)
		}
		// Verify the command carries the expected sha for the agent to verify.
		c2 := s.hub.register("srv2")
		s.maybeUpdateAgent("srv2", protocol.Hello{Arch: "amd64", BinSHA: "stale"})
		cmd := <-c2.ch
		var req protocol.UpdateAgentRequest
		if json.Unmarshal(cmd.Data, &req) != nil || req.SHA256 != amd64SHA {
			t.Fatalf("update command missing expected sha: %s", cmd.Data)
		}
	})

	t.Run("up-to-date binary does nothing", func(t *testing.T) {
		s, _ := newTestServer(t)
		c := s.hub.register("srv1")
		s.maybeUpdateAgent("srv1", protocol.Hello{Arch: "amd64", BinSHA: amd64SHA})
		if got := drain(c); got != "" {
			t.Fatalf("expected no command for current binary, got %q", got)
		}
	})

	t.Run("missing fields are ignored", func(t *testing.T) {
		s, _ := newTestServer(t)
		c := s.hub.register("srv1")
		s.maybeUpdateAgent("srv1", protocol.Hello{Version: 1}) // no arch/sha
		if got := drain(c); got != "" {
			t.Fatalf("expected no command for legacy hello, got %q", got)
		}
	})

	t.Run("cooldown suppresses repeat updates", func(t *testing.T) {
		s, _ := newTestServer(t)
		c := s.hub.register("srv1")
		s.maybeUpdateAgent("srv1", protocol.Hello{Arch: "amd64", BinSHA: "stale"})
		if got := drain(c); got != protocol.CmdUpdateAgent {
			t.Fatalf("first call should update, got %q", got)
		}
		s.maybeUpdateAgent("srv1", protocol.Hello{Arch: "amd64", BinSHA: "stale"})
		if got := drain(c); got != "" {
			t.Fatalf("second call within cooldown should be suppressed, got %q", got)
		}
	})
}
