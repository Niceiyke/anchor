package control

import "testing"

func TestValidReqID(t *testing.T) {
	ok := []string{"log_abc123", "log_1a2b3c4d5e6f", "log_8f14e45fceea167a5a36dedd4bea2543"}
	bad := []string{"", "abc", "log/../../etc", "exec:foo", "log abc", "a;b", string(make([]byte, 70))}
	for _, s := range ok {
		if !validReqID(s) {
			t.Errorf("expected %q valid", s)
		}
	}
	for _, s := range bad {
		if validReqID(s) {
			t.Errorf("expected %q rejected", s)
		}
	}
}
