package control

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/oyomworld/anchor/internal/store"
	"github.com/oyomworld/anchor/pkg/protocol"
)

// deleteApp dispatches a delete and returns the StopAppRequest the agent would
// receive, by draining the hub connection registered for the server.
func deleteApp(t *testing.T, srv *Server, ch chan protocol.Command, id, query string) protocol.StopAppRequest {
	t.Helper()
	r := httptest.NewRequest("DELETE", "/api/apps/"+id+query, nil)
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	srv.handleDeleteApp(w, r)
	if w.Code != 204 {
		t.Fatalf("delete: got %d", w.Code)
	}
	select {
	case cmd := <-ch:
		if cmd.Type != protocol.CmdStopApp {
			t.Fatalf("expected stop_app command, got %s", cmd.Type)
		}
		var req protocol.StopAppRequest
		if err := json.Unmarshal(cmd.Data, &req); err != nil {
			t.Fatal(err)
		}
		return req
	default:
		t.Fatal("no command dispatched to agent")
		return protocol.StopAppRequest{}
	}
}

func TestDeleteAppVolumeHandling(t *testing.T) {
	cases := []struct {
		name      string
		query     string
		wantClear bool
	}{
		{"default removes volumes", "", true},
		{"keep_volume=true keeps them", "?keep_volume=true", false},
		{"keep_volume=false removes them", "?keep_volume=false", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, st := newTestServer(t)
			if err := st.CreateApp(store.App{ID: "app_1", Name: "shop", ServerID: "s1"}); err != nil {
				t.Fatal(err)
			}
			conn := srv.hub.register("s1") // pretend the agent is online
			req := deleteApp(t, srv, conn.ch, "app_1", c.query)
			if req.RemoveVolumes != c.wantClear {
				t.Errorf("RemoveVolumes = %v, want %v", req.RemoveVolumes, c.wantClear)
			}
			if _, err := st.GetApp("app_1"); err == nil {
				t.Error("app record should be deleted")
			}
		})
	}
}
