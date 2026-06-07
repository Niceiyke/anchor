package control

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"time"

	"github.com/oyomworld/anchor/internal/store"
	"golang.org/x/crypto/bcrypt"
)

// auth is the single-user session manager. Sessions are persisted in the store
// so they survive control-plane restarts (e.g. redeploys).
type auth struct {
	store store.Store
	ttl   time.Duration
}

func newAuth(st store.Store) *auth {
	return &auth{store: st, ttl: 7 * 24 * time.Hour}
}

// ---- password hashing (bcrypt, with legacy sha256 migration) ----

func hashPass(p string) string {
	b, err := bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
	if err != nil {
		// bcrypt only errors on >72-byte passwords; truncate and retry.
		b, _ = bcrypt.GenerateFromPassword([]byte(p[:72]), bcrypt.DefaultCost)
	}
	return string(b)
}

func legacyHash(p string) string {
	sum := sha256.Sum256([]byte("anchor:" + p))
	return hex.EncodeToString(sum[:])
}

// checkPass verifies a password against a stored hash. It accepts both bcrypt
// hashes and the legacy sha256 format (so existing installs keep working).
func checkPass(p, hash string) bool {
	if strings.HasPrefix(hash, "$2") { // bcrypt
		return bcrypt.CompareHashAndPassword([]byte(hash), []byte(p)) == nil
	}
	return subtle.ConstantTimeCompare([]byte(legacyHash(p)), []byte(hash)) == 1
}

// isLegacyHash reports whether a stored hash is the old sha256 format and
// should be upgraded to bcrypt on next successful login.
func isLegacyHash(hash string) bool { return !strings.HasPrefix(hash, "$2") }

func randToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---- sessions ----

func (a *auth) issue() string {
	tok := randToken()
	_ = a.store.CreateSession(tok, time.Now().Add(a.ttl))
	return tok
}

func (a *auth) valid(tok string) bool {
	if tok == "" {
		return false
	}
	exp, err := a.store.GetSession(tok)
	if err != nil {
		return false
	}
	if time.Now().After(exp) {
		_ = a.store.DeleteSession(tok)
		return false
	}
	return true
}

func (a *auth) revoke(tok string) {
	_ = a.store.DeleteSession(tok)
}

// bearer pulls a token from the Authorization header or the anchor_session cookie.
func bearer(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	if c, err := r.Cookie("anchor_session"); err == nil {
		return c.Value
	}
	return ""
}
