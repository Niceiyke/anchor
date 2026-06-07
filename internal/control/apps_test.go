package control

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oyomworld/anchor/internal/store"
)

func newTestServer(t *testing.T) (*Server, store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "s.json"))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := New(st, "admin", "pw")
	if err != nil {
		t.Fatal(err)
	}
	return srv, st
}

func patchApp(t *testing.T, srv *Server, id, body string) int {
	t.Helper()
	r := httptest.NewRequest("PATCH", "/api/apps/"+id, strings.NewReader(body))
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	srv.handleUpdateApp(w, r)
	return w.Code
}

func TestHandleUpdateApp(t *testing.T) {
	srv, st := newTestServer(t)
	_ = st.CreateApp(store.App{
		ID: "app_1", Name: "x", ServerID: "s1", RepoURL: "u",
		Branch: "main", ContainerPort: 3000, EnvVars: map[string]string{},
	})

	// Partial update: only provided fields change; others are untouched.
	if code := patchApp(t, srv, "app_1", `{"domain":"a.example.com","compose_file":"deploy/compose.prod.yml"}`); code != 200 {
		t.Fatalf("valid patch: got %d", code)
	}
	got, _ := st.GetApp("app_1")
	if got.Domain != "a.example.com" || got.ComposeFile != "deploy/compose.prod.yml" {
		t.Fatalf("fields not applied: %+v", got)
	}
	if got.Branch != "main" || got.ContainerPort != 3000 {
		t.Fatalf("untouched fields changed: %+v", got)
	}

	// Path traversal in compose_file is rejected and nothing is persisted.
	if code := patchApp(t, srv, "app_1", `{"compose_file":"../../etc/passwd"}`); code != 400 {
		t.Fatalf("traversal should 400, got %d", code)
	}
	got, _ = st.GetApp("app_1")
	if got.ComposeFile != "deploy/compose.prod.yml" {
		t.Fatalf("rejected patch mutated state: %q", got.ComposeFile)
	}

	// Out-of-range port rejected; empty branch rejected.
	if code := patchApp(t, srv, "app_1", `{"container_port":0}`); code != 400 {
		t.Fatalf("bad port should 400, got %d", code)
	}
	if code := patchApp(t, srv, "app_1", `{"branch":"  "}`); code != 400 {
		t.Fatalf("empty branch should 400, got %d", code)
	}

	// Unknown app id -> 404.
	if code := patchApp(t, srv, "nope", `{"domain":"x"}`); code != 404 {
		t.Fatalf("missing app should 404, got %d", code)
	}
}
