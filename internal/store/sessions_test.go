package store

import (
	"path/filepath"
	"testing"
	"time"
)

// TestDeleteSessionsForUser verifies a password change can revoke a user's
// other sessions while preserving the caller's own, across both backends.
func TestDeleteSessionsForUser(t *testing.T) {
	json, _ := Open(filepath.Join(t.TempDir(), "s.json"))
	sqlite, err := OpenSQLite(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	for name, st := range map[string]Store{"json": json, "sqlite": sqlite} {
		t.Run(name, func(t *testing.T) {
			exp := time.Now().Add(time.Hour)
			// admin has two sessions; a username with a LIKE metacharacter and
			// another user must be untouched.
			_ = st.CreateSession("admin:admin:keep", exp)
			_ = st.CreateSession("admin:admin:revoke", exp)
			_ = st.CreateSession("admin_x:admin:other", exp) // '_' must not act as wildcard
			_ = st.CreateSession("viewer:viewer:safe", exp)

			if err := st.DeleteSessionsForUser("admin", "admin:admin:keep"); err != nil {
				t.Fatal(err)
			}

			if _, err := st.GetSession("admin:admin:keep"); err != nil {
				t.Error("caller's own session was revoked")
			}
			if _, err := st.GetSession("admin:admin:revoke"); err == nil {
				t.Error("other admin session should have been revoked")
			}
			if _, err := st.GetSession("admin_x:admin:other"); err != nil {
				t.Error("admin_x session wrongly revoked (LIKE wildcard leak)")
			}
			if _, err := st.GetSession("viewer:viewer:safe"); err != nil {
				t.Error("unrelated user session wrongly revoked")
			}
		})
	}
}
