package control

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/oyomworld/anchor/internal/store"
	"github.com/oyomworld/anchor/pkg/protocol"
)

func TestNormalizeBaseDomain(t *testing.T) {
	cases := map[string]string{
		"apps.wordlyte.com":          "apps.wordlyte.com",
		" Apps.Wordlyte.com ":        "apps.wordlyte.com",
		"https://apps.wordlyte.com/": "apps.wordlyte.com",
		".apps.wordlyte.com.":        "apps.wordlyte.com",
		"":                           "",
	}
	for in, want := range cases {
		if got := normalizeBaseDomain(in); got != want {
			t.Errorf("normalizeBaseDomain(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAssignDomainUnique(t *testing.T) {
	srv, st := newTestServer(t)
	_ = st.SaveSettings(store.Settings{AdminUser: "admin", BaseDomain: "apps.wordlyte.com"})

	if got := srv.assignDomain("My Blog"); got != "my-blog.apps.wordlyte.com" {
		t.Fatalf("first assign = %q", got)
	}
	// occupy it, then a second app with the same name must get a -2 suffix.
	_ = st.CreateApp(store.App{ID: "app_1", Name: "My Blog", ServerID: "s1", Domain: "my-blog.apps.wordlyte.com"})
	if got := srv.assignDomain("My Blog"); got != "my-blog-2.apps.wordlyte.com" {
		t.Fatalf("second assign = %q", got)
	}
	// no base domain -> empty
	_ = st.SaveSettings(store.Settings{AdminUser: "admin"})
	if got := srv.assignDomain("My Blog"); got != "" {
		t.Fatalf("no base domain should give empty, got %q", got)
	}
}

func TestCreateAppAutoAssignsDomain(t *testing.T) {
	srv, st := newTestServer(t)
	_ = st.SaveSettings(store.Settings{AdminUser: "admin", BaseDomain: "apps.wordlyte.com"})

	r := httptest.NewRequest("POST", "/api/apps", strings.NewReader(`{"name":"shop","server_id":"s1","repo_url":"https://x/y.git"}`))
	w := httptest.NewRecorder()
	srv.handleCreateApp(w, r)
	if w.Code != 201 {
		t.Fatalf("create app: got %d", w.Code)
	}
	apps, _ := st.ListApps()
	if len(apps) != 1 || apps[0].Domain != "shop.apps.wordlyte.com" {
		t.Fatalf("expected auto domain, got %+v", apps)
	}
}

func TestHandleTLSCheck(t *testing.T) {
	srv, st := newTestServer(t)
	_ = st.CreateApp(store.App{ID: "app_1", Name: "shop", ServerID: "s1", Domain: "shop.apps.wordlyte.com"})

	check := func(domain string) int {
		r := httptest.NewRequest("GET", "/tls/check?domain="+domain, nil)
		w := httptest.NewRecorder()
		srv.handleTLSCheck(w, r)
		return w.Code
	}
	if code := check("shop.apps.wordlyte.com"); code != 200 {
		t.Errorf("known domain: got %d, want 200", code)
	}
	if code := check("SHOP.apps.wordlyte.com"); code != 200 {
		t.Errorf("case-insensitive: got %d, want 200", code)
	}
	if code := check("evil.example.com"); code != 404 {
		t.Errorf("unknown domain: got %d, want 404", code)
	}
	if code := check(""); code != 400 {
		t.Errorf("empty domain: got %d, want 400", code)
	}
}

func TestValidHostname(t *testing.T) {
	ok := []string{"a.example.com", "x-y.apps.example.com", "1.2.example.io"}
	bad := []string{"", "nodot", "-bad.example.com", "bad-.example.com", "a..example.com", "UP.example.com", "a.exa mple.com"}
	for _, d := range ok {
		if !validHostname(d) {
			t.Errorf("validHostname(%q) = false, want true", d)
		}
	}
	for _, d := range bad {
		if validHostname(d) {
			t.Errorf("validHostname(%q) = true, want false", d)
		}
	}
}

func TestNormalizeRoutes(t *testing.T) {
	srv, st := newTestServer(t)
	_ = st.SaveSettings(store.Settings{AdminUser: "admin", BaseDomain: "apps.example.com"})

	// Happy path: explicit domain kept, empty domain auto-assigned per service.
	if got, err := srv.normalizeRoutes("shop", "shop.apps.example.com", nil); err != nil || len(got) != 0 {
		t.Fatalf("nil routes: got %+v err %v", got, err)
	}
	got, err := srv.normalizeRoutes("shop", "shop.apps.example.com", []protocol.Route{
		{Service: "api", Domain: "API.example.com"},
		{Service: "admin"},
	})
	if err != nil {
		t.Fatalf("normalizeRoutes: %v", err)
	}
	if got[0].Domain != "api.example.com" {
		t.Errorf("explicit domain not lowercased/kept: %q", got[0].Domain)
	}
	if got[1].Domain != "shop-admin.apps.example.com" {
		t.Errorf("auto-assigned route domain = %q", got[1].Domain)
	}

	// Errors: missing service, duplicate domain, collision with primary.
	if _, err := srv.normalizeRoutes("shop", "shop.apps.example.com", []protocol.Route{{Domain: "x.example.com"}}); err == nil {
		t.Error("expected error for route without service")
	}
	if _, err := srv.normalizeRoutes("shop", "shop.apps.example.com", []protocol.Route{
		{Service: "a", Domain: "dup.example.com"}, {Service: "b", Domain: "dup.example.com"},
	}); err == nil {
		t.Error("expected error for duplicate route domain")
	}
	if _, err := srv.normalizeRoutes("shop", "shop.apps.example.com", []protocol.Route{
		{Service: "a", Domain: "shop.apps.example.com"},
	}); err == nil {
		t.Error("expected error for route colliding with primary domain")
	}
}

func TestTLSCheckMatchesRouteDomain(t *testing.T) {
	srv, st := newTestServer(t)
	_ = st.CreateApp(store.App{
		ID: "app_1", Name: "shop", ServerID: "s1", Domain: "shop.apps.example.com",
		Routes: []protocol.Route{{Service: "api", Domain: "api.example.com"}},
	})
	r := httptest.NewRequest("GET", "/tls/check?domain=api.example.com", nil)
	w := httptest.NewRecorder()
	srv.handleTLSCheck(w, r)
	if w.Code != 200 {
		t.Fatalf("route domain should be approved for TLS: got %d", w.Code)
	}
}
