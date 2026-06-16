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

func TestPortExposed(t *testing.T) {
	cases := []struct {
		exposed string
		port    int
		want    bool
	}{
		{"7111/tcp 8080/tcp ", 7111, true},
		{"7111/tcp", 7111, true},
		{"8080/tcp ", 7111, false},
		{"", 7111, false},
		{"17111/tcp", 7111, false}, // must not substring-match
		{"7111/udp", 7111, false},  // tcp only
	}
	for _, c := range cases {
		if got := portExposed(c.exposed, c.port); got != c.want {
			t.Errorf("portExposed(%q, %d) = %v, want %v", c.exposed, c.port, got, c.want)
		}
	}
}
