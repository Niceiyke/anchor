package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/oyomworld/anchor/pkg/protocol"
)

func TestEffectiveRoutes(t *testing.T) {
	// Explicit routes win.
	rs := effectiveRoutes(protocol.DeployRequest{
		Domain: "ignored.example.com", ContainerPort: 3000,
		Routes: []protocol.Route{{Domain: "a.example.com", Service: "web"}},
	})
	if len(rs) != 1 || rs[0].Domain != "a.example.com" {
		t.Fatalf("explicit routes not preferred: %+v", rs)
	}
	// Legacy single Domain/Service/Port synthesizes one route.
	rs = effectiveRoutes(protocol.DeployRequest{Domain: "b.example.com", Service: "api", ContainerPort: 8080})
	if len(rs) != 1 || rs[0].Domain != "b.example.com" || rs[0].Service != "api" || rs[0].Port != 8080 {
		t.Fatalf("legacy route not synthesized: %+v", rs)
	}
	// No domain, no routes -> none.
	if rs := effectiveRoutes(protocol.DeployRequest{}); rs != nil {
		t.Fatalf("expected no routes, got %+v", rs)
	}
}

func TestBuildHealthTargets(t *testing.T) {
	// Multi-service: one target per routed container; primary inherits app path,
	// secondary uses its own.
	routes := []resolvedRoute{
		{domain: "app.example.com", host: "blog-web", port: 3000, container: "c1"},
		{domain: "api.example.com", host: "blog-api", port: 8080, container: "c2", healthPath: "/healthz"},
	}
	got := buildHealthTargets(protocol.DeployRequest{HealthPath: "/health"}, routes, routeTarget{container: "c1"})
	if len(got) != 2 {
		t.Fatalf("want 2 targets, got %d", len(got))
	}
	if got[0].container != "c1" || got[0].path != "/health" {
		t.Errorf("primary target = %+v (want path inherited from app)", got[0])
	}
	if got[1].container != "c2" || got[1].path != "/healthz" {
		t.Errorf("secondary target = %+v (want its own path)", got[1])
	}

	// No route containers (Dockerfile / no public routes): single primary, port
	// falls back to the app container port.
	got = buildHealthTargets(protocol.DeployRequest{AppName: "shop", ContainerPort: 3000, HealthPath: "/h"},
		nil, routeTarget{container: "shop"})
	if len(got) != 1 || got[0].container != "shop" || got[0].port != 3000 || got[0].path != "/h" {
		t.Fatalf("dockerfile target = %+v", got)
	}

	// Nothing identifiable -> no targets (health gate skipped).
	if got := buildHealthTargets(protocol.DeployRequest{}, nil, routeTarget{}); got != nil {
		t.Errorf("want nil targets, got %+v", got)
	}
}

func TestRouteAlias(t *testing.T) {
	if got := routeAlias("blog", "web", false); got != "blog" {
		t.Errorf("single-route alias = %q, want %q", got, "blog")
	}
	if got := routeAlias("blog", "web", true); got != "blog-web" {
		t.Errorf("multi-route alias = %q, want %q", got, "blog-web")
	}
	if got := routeAlias("blog", "", true); got != "blog" {
		t.Errorf("multi-route alias w/o service = %q, want %q", got, "blog")
	}
}

func TestWriteEnvFile(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want []string // substrings that must appear in the output
	}{
		{
			name: "simple value",
			env:  map[string]string{"FOO": "bar"},
			want: []string{`FOO="bar"`},
		},
		{
			name: "value with hash",
			env:  map[string]string{"FOO": "val#comment"},
			want: []string{`FOO="val#comment"`},
		},
		{
			name: "value with double quote",
			env:  map[string]string{"FOO": `say "hello"`},
			want: []string{`FOO="say \"hello\""`},
		},
		{
			name: "value with backslash",
			env:  map[string]string{"FOO": `a\b`},
			want: []string{`FOO="a\\b"`},
		},
		{
			name: "multiline value (PEM key style)",
			env:  map[string]string{"PRIVATE_KEY": "-----BEGIN RSA PRIVATE KEY-----\nMIIE...\n-----END RSA PRIVATE KEY-----"},
			want: []string{"PRIVATE_KEY=\"-----BEGIN RSA PRIVATE KEY-----\nMIIE...\n-----END RSA PRIVATE KEY-----\""},
		},
		{
			name: "empty map produces no file content",
			env:  map[string]string{},
			want: nil, // writeEnvFile returns early; we just check no error
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := writeEnvFile(dir, c.env); err != nil {
				t.Fatalf("writeEnvFile: %v", err)
			}
			if len(c.want) == 0 {
				return
			}
			b, err := os.ReadFile(filepath.Join(dir, ".env"))
			if err != nil {
				t.Fatalf("read .env: %v", err)
			}
			got := string(b)
			for _, sub := range c.want {
				if !strings.Contains(got, sub) {
					t.Errorf("output does not contain %q\ngot:\n%s", sub, got)
				}
			}
		})
	}
}

func TestParseTCPPort(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"8080/tcp", 8080},
		{"80", 80}, // bare number = tcp
		{"53/udp", 0},
		{"abc/tcp", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := parseTCPPort(c.in); got != c.want {
			t.Errorf("parseTCPPort(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestSelectComposeTarget(t *testing.T) {
	web := composeContainer{id: "c1", service: "web", ports: []int{3000}}
	api := composeContainer{id: "c2", service: "api", ports: []int{8080}}
	db := composeContainer{id: "c3", service: "db", ports: []int{5432}}

	cases := []struct {
		name    string
		cs      []composeContainer
		service string
		port    int
		wantID  string
		wantOK  bool
	}{
		{"named service wins over port", []composeContainer{web, api, db}, "api", 3000, "c2", true},
		{"named service not found", []composeContainer{web, api}, "worker", 0, "", false},
		{"single port match", []composeContainer{web, api, db}, "", 8080, "c2", true},
		{"only service fallback", []composeContainer{web}, "", 0, "c1", true},
		{"ambiguous: multi, no name, no port match", []composeContainer{web, api, db}, "", 9999, "", false},
		{"ambiguous: multi, no name, no port", []composeContainer{web, api}, "", 0, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := selectComposeTarget(c.cs, c.service, c.port)
			if ok != c.wantOK || got.id != c.wantID {
				t.Errorf("selectComposeTarget(_, %q, %d) = (%q, %v), want (%q, %v)",
					c.service, c.port, got.id, ok, c.wantID, c.wantOK)
			}
		})
	}
}

func TestResolveUpstreamPort(t *testing.T) {
	cases := []struct {
		name         string
		ports        []int
		configured   int
		wantPort     int
		wantAdjusted bool
	}{
		{"configured matches exposed", []int{3000, 8080}, 3000, 3000, false},
		{"unset, single exposed", []int{8080}, 0, 8080, false},
		{"wrong configured, single exposed -> correct", []int{8080}, 3000, 8080, true},
		{"configured set, not exposed, multiple -> keep", []int{8080, 9090}, 3000, 3000, false},
		{"unset, multiple exposed -> 0", []int{8080, 9090}, 0, 0, false},
		{"unset, none exposed -> 0", nil, 0, 0, false},
		{"configured, none exposed -> keep", nil, 3000, 3000, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			port, adjusted := resolveUpstreamPort(composeContainer{ports: c.ports}, c.configured)
			if port != c.wantPort || adjusted != c.wantAdjusted {
				t.Errorf("resolveUpstreamPort(%v, %d) = (%d, %v), want (%d, %v)",
					c.ports, c.configured, port, adjusted, c.wantPort, c.wantAdjusted)
			}
		})
	}
}
