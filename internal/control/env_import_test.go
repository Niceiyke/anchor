package control

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oyomworld/anchor/internal/store"
)

func TestParseDotenv(t *testing.T) {
	sample := `# ── Postgres ─────────────────────────────────────────────
POSTGRES_USER=hush
POSTGRES_PASSWORD=HcZTk7bPKplbkIMuEFEPwC32rrw
POSTGRES_DB=hush

# ── JWT (auth service) ───────────────────────────────────
# Generate a strong secret: openssl rand -base64 32
JWT_SECRET=HcZTk7bPKplbkIMuEFEPwCc3uXPSrjyGkXNDD+ggsL8=

export STORAGE_USE_SSL=true
STORAGE_BUCKET="hush-media"
EMPTY=
# a banner with = signs ===========
POSTGRES_DSN=postgres://hush:HcZTk7bPKplbkIMuEFEPwC32rrw@localhost:5432/hush?sslmode=disable
1INVALID=nope
ANTHROPIC_API_KEY=sk-ant-...`

	got := parseDotenv(sample)

	want := map[string]string{
		"POSTGRES_USER":     "hush",
		"POSTGRES_PASSWORD": "HcZTk7bPKplbkIMuEFEPwC32rrw",
		"POSTGRES_DB":       "hush",
		"JWT_SECRET":        "HcZTk7bPKplbkIMuEFEPwCc3uXPSrjyGkXNDD+ggsL8=",
		"STORAGE_USE_SSL":   "true",       // `export ` stripped
		"STORAGE_BUCKET":    "hush-media", // quotes stripped
		"EMPTY":             "",
		"POSTGRES_DSN":      "postgres://hush:HcZTk7bPKplbkIMuEFEPwC32rrw@localhost:5432/hush?sslmode=disable",
		"ANTHROPIC_API_KEY": "sk-ant-...",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d keys, want %d: %v", len(got), len(want), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := got["1INVALID"]; ok {
		t.Error("key starting with a digit should be rejected")
	}
}

func TestHandleImportEnv(t *testing.T) {
	srv, st := newTestServer(t)
	_ = st.CreateApp(store.App{ID: "app_1", Name: "x", ServerID: "s1", RepoURL: "u", Branch: "main", ContainerPort: 3000, EnvVars: map[string]string{"KEEP": "1"}})

	body := `{"content":"A=1\n# c\nB=two\n","secret":false}`
	r := httptest.NewRequest("POST", "/api/apps/app_1/env/import", strings.NewReader(body))
	r.SetPathValue("id", "app_1")
	w := httptest.NewRecorder()
	srv.handleImportEnv(w, r)
	if w.Code != 200 {
		t.Fatalf("import: got %d", w.Code)
	}
	a, _ := st.GetApp("app_1")
	if a.EnvVars["A"] != "1" || a.EnvVars["B"] != "two" || a.EnvVars["KEEP"] != "1" {
		t.Fatalf("merge wrong: %+v", a.EnvVars)
	}
	if a.EnvSecret["A"] != false {
		t.Error("imported with secret=false should be plain")
	}

	r2 := httptest.NewRequest("POST", "/api/apps/app_1/env/import", strings.NewReader(`{"content":"# only a comment\n"}`))
	r2.SetPathValue("id", "app_1")
	w2 := httptest.NewRecorder()
	srv.handleImportEnv(w2, r2)
	if w2.Code != 400 {
		t.Errorf("no pairs: got %d, want 400", w2.Code)
	}
}

func TestHandleUpdateServerPublicIP(t *testing.T) {
	srv, st := newTestServer(t)
	_ = st.CreateServer(store.Server{ID: "srv_1", Name: "vps", AgentToken: "t"})

	r := httptest.NewRequest("PATCH", "/api/servers/srv_1", strings.NewReader(`{"public_ip":"203.0.113.10"}`))
	r.SetPathValue("id", "srv_1")
	w := httptest.NewRecorder()
	srv.handleUpdateServer(w, r)
	if w.Code != 200 {
		t.Fatalf("update server: got %d", w.Code)
	}
	got, _ := st.GetServer("srv_1")
	if got.PublicIP != "203.0.113.10" || got.Name != "vps" {
		t.Fatalf("public ip not saved / name clobbered: %+v", got)
	}
}
