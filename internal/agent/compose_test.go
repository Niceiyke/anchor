package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
