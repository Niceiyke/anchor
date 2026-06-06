package control

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

// auth is a minimal single-user session manager. Sessions live in memory; the
// admin credential is persisted (sha256 hash) in the store.
//
// NOTE: sha256 is used for MVP simplicity. Swap for bcrypt/argon2 before this
// is exposed to the public internet.
type auth struct {
	mu       sync.RWMutex
	sessions map[string]time.Time // token -> expiry
	ttl      time.Duration
}

func newAuth() *auth {
	return &auth{sessions: map[string]time.Time{}, ttl: 24 * time.Hour}
}

func hashPass(p string) string {
	sum := sha256.Sum256([]byte("anchor:" + p))
	return hex.EncodeToString(sum[:])
}

func checkPass(p, hash string) bool {
	return subtle.ConstantTimeCompare([]byte(hashPass(p)), []byte(hash)) == 1
}

func randToken() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (a *auth) issue() string {
	tok := randToken()
	a.mu.Lock()
	a.sessions[tok] = time.Now().Add(a.ttl)
	a.mu.Unlock()
	return tok
}

func (a *auth) valid(tok string) bool {
	a.mu.RLock()
	exp, ok := a.sessions[tok]
	a.mu.RUnlock()
	if !ok {
		return false
	}
	if time.Now().After(exp) {
		a.mu.Lock()
		delete(a.sessions, tok)
		a.mu.Unlock()
		return false
	}
	return true
}

func (a *auth) revoke(tok string) {
	a.mu.Lock()
	delete(a.sessions, tok)
	a.mu.Unlock()
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
