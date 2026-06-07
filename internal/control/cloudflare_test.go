package control

import "testing"

func TestPickZone(t *testing.T) {
	zones := []cfZone{
		{ID: "z1", Name: "wordlyte.com"},
		{ID: "z2", Name: "other.com"},
		{ID: "z3", Name: "sub.wordlyte.com"},
	}

	cases := []struct {
		domain string
		wantID string
		wantOK bool
	}{
		{"blog.apps.wordlyte.com", "z1", true}, // matches wordlyte.com
		{"x.sub.wordlyte.com", "z3", true},     // most specific zone wins
		{"wordlyte.com", "z1", true},           // exact
		{"evil.example.com", "", false},        // no zone
		{"notwordlyte.com", "", false},         // suffix but not on dot boundary
	}
	for _, c := range cases {
		z, ok := pickZone(c.domain, zones)
		if ok != c.wantOK || z.ID != c.wantID {
			t.Errorf("pickZone(%q) = (%q,%v), want (%q,%v)", c.domain, z.ID, ok, c.wantID, c.wantOK)
		}
	}
}
