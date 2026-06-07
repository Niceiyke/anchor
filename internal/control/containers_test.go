package control

import (
	"net/http/httptest"
	"testing"

	"github.com/oyomworld/anchor/internal/store"
)

func containerAction(t *testing.T, srv *Server, serverID, name, action string) int {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/servers/"+serverID+"/containers/"+name+"/"+action, nil)
	r.SetPathValue("id", serverID)
	r.SetPathValue("name", name)
	r.SetPathValue("action", action)
	w := httptest.NewRecorder()
	srv.handleContainerAction(w, r)
	return w.Code
}

func TestHandleContainerActionValidation(t *testing.T) {
	srv, st := newTestServer(t)
	_ = st.CreateServer(store.Server{ID: "srv_1", Name: "vps", AgentToken: "t"})

	// Unknown server -> 404.
	if code := containerAction(t, srv, "nope", "web", "restart"); code != 404 {
		t.Errorf("unknown server: got %d, want 404", code)
	}
	// Unknown action -> 400.
	if code := containerAction(t, srv, "srv_1", "web", "obliterate"); code != 400 {
		t.Errorf("bad action: got %d, want 400", code)
	}
	// Injection-looking name (leading dash) -> 400.
	if code := containerAction(t, srv, "srv_1", "-rf", "remove"); code != 400 {
		t.Errorf("bad name: got %d, want 400", code)
	}
	// Valid request but agent offline -> 409.
	if code := containerAction(t, srv, "srv_1", "web-1", "restart"); code != 409 {
		t.Errorf("offline agent: got %d, want 409", code)
	}
}

func TestHandlePruneContainers(t *testing.T) {
	srv, st := newTestServer(t)
	_ = st.CreateServer(store.Server{ID: "srv_1", Name: "vps", AgentToken: "t"})

	prune := func(serverID string) int {
		r := httptest.NewRequest("POST", "/api/servers/"+serverID+"/containers/prune", nil)
		r.SetPathValue("id", serverID)
		w := httptest.NewRecorder()
		srv.handlePruneContainers(w, r)
		return w.Code
	}

	if code := prune("nope"); code != 404 {
		t.Errorf("unknown server: got %d, want 404", code)
	}
	if code := prune("srv_1"); code != 409 { // agent offline
		t.Errorf("offline agent: got %d, want 409", code)
	}
}

func TestHandlePruneImages(t *testing.T) {
	srv, st := newTestServer(t)
	_ = st.CreateServer(store.Server{ID: "srv_1", Name: "vps", AgentToken: "t"})

	prune := func(serverID string) int {
		r := httptest.NewRequest("POST", "/api/servers/"+serverID+"/images/prune", nil)
		r.SetPathValue("id", serverID)
		w := httptest.NewRecorder()
		srv.handlePruneImages(w, r)
		return w.Code
	}

	if code := prune("nope"); code != 404 {
		t.Errorf("unknown server: got %d, want 404", code)
	}
	if code := prune("srv_1"); code != 409 { // agent offline
		t.Errorf("offline agent: got %d, want 409", code)
	}
}
