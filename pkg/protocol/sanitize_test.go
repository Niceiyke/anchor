package protocol

import "testing"

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"MyApp":          "myapp",
		"my app":         "my-app",
		"web.api":        "web-api",
		"--Trim__":       "trim",
		"a/b:c":          "a-b-c",
		"already-ok_1":   "already-ok_1",
		"UPPER_Snake_99": "upper_snake_99",
	}
	for in, want := range cases {
		if got := Sanitize(in); got != want {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}
