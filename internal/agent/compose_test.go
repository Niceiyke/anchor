package agent

import "testing"

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
