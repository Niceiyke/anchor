package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// encPrefix marks a value as encrypted, so we can tell ciphertext from legacy
// plaintext and migrate transparently on next write.
const encPrefix = "enc:v1:"

// cryptoStore wraps a Store and transparently encrypts sensitive fields at rest
// with AES-256-GCM: Settings secrets (GitHub keys), Server agent tokens, and
// Database passwords.
type cryptoStore struct {
	Store
	gcm    cipher.AEAD
	detKey []byte // HMAC key for deterministic encryption (token lookups)
}

// NewEncrypted wraps inner so secret fields are encrypted at rest.
// key must be 32 bytes (AES-256).
func NewEncrypted(inner Store, key []byte) (Store, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	// Derive a separate key for deterministic-nonce encryption so we never
	// reuse the AES key's randomized-nonce domain for the deterministic one.
	dk := sha256.Sum256(append([]byte("anchor-det-nonce:"), key...))
	return &cryptoStore{Store: inner, gcm: gcm, detKey: dk[:]}, nil
}

func (c *cryptoStore) enc(s string) string {
	if s == "" || strings.HasPrefix(s, encPrefix) {
		return s
	}
	nonce := make([]byte, c.gcm.NonceSize())
	_, _ = rand.Read(nonce)
	ct := c.gcm.Seal(nonce, nonce, []byte(s), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(ct)
}

// encDet encrypts deterministically: the same plaintext always yields the same
// ciphertext, so the result can be stored and later matched by exact lookup
// (used for agent tokens, which the agent presents in plaintext to authenticate).
// The nonce is derived from the plaintext via HMAC, which is safe because nonce
// reuse under AES-GCM is only dangerous across *different* plaintexts.
func (c *cryptoStore) encDet(s string) string {
	if s == "" || strings.HasPrefix(s, encPrefix) {
		return s
	}
	mac := hmac.New(sha256.New, c.detKey)
	mac.Write([]byte(s))
	nonce := mac.Sum(nil)[:c.gcm.NonceSize()]
	ct := c.gcm.Seal(nonce, nonce, []byte(s), nil)
	return encPrefix + base64.StdEncoding.EncodeToString(ct)
}

func (c *cryptoStore) dec(s string) string {
	if !strings.HasPrefix(s, encPrefix) {
		return s // legacy plaintext — will be encrypted on next write
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, encPrefix))
	if err != nil || len(raw) < c.gcm.NonceSize() {
		return s
	}
	nonce, ct := raw[:c.gcm.NonceSize()], raw[c.gcm.NonceSize():]
	pt, err := c.gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return s // wrong key or corrupt — leave as-is
	}
	return string(pt)
}

// ---- Settings ----

// secretFields are the Settings fields encrypted at rest. AdminPass is omitted
// (it's already a one-way bcrypt hash).
func (c *cryptoStore) transformSettings(s *Settings, fn func(string) string) {
	s.GitHubToken = fn(s.GitHubToken)
	s.WebhookSecret = fn(s.WebhookSecret)
	s.GitHubAppPrivateKey = fn(s.GitHubAppPrivateKey)
	s.GitHubAppWebhookSecret = fn(s.GitHubAppWebhookSecret)
	s.GitHubClientSecret = fn(s.GitHubClientSecret)
	s.CloudflareAPIToken = fn(s.CloudflareAPIToken)
}

func (c *cryptoStore) Settings() (Settings, error) {
	v, err := c.Store.Settings()
	if err != nil {
		return v, err
	}
	c.transformSettings(&v, c.dec)
	return v, nil
}

func (c *cryptoStore) SaveSettings(v Settings) error {
	c.transformSettings(&v, c.enc)
	return c.Store.SaveSettings(v)
}

// ---- Servers (agent tokens) ----

func (c *cryptoStore) ListServers() ([]Server, error) {
	servers, err := c.Store.ListServers()
	for i := range servers {
		servers[i].AgentToken = c.dec(servers[i].AgentToken)
	}
	return servers, err
}

func (c *cryptoStore) GetServer(id string) (Server, error) {
	v, err := c.Store.GetServer(id)
	v.AgentToken = c.dec(v.AgentToken)
	return v, err
}

func (c *cryptoStore) GetServerByToken(token string) (Server, error) {
	// Tokens are encrypted deterministically, so the stored ciphertext for a
	// given plaintext is stable and can be matched directly. Try the legacy
	// plaintext form first (for rows written before encryption), then the
	// deterministic ciphertext.
	v, err := c.Store.GetServerByToken(token)
	if err == nil {
		v.AgentToken = c.dec(v.AgentToken)
		return v, nil
	}
	v, err = c.Store.GetServerByToken(c.encDet(token))
	v.AgentToken = c.dec(v.AgentToken)
	return v, err
}

func (c *cryptoStore) CreateServer(v Server) error {
	v.AgentToken = c.encDet(v.AgentToken)
	return c.Store.CreateServer(v)
}

func (c *cryptoStore) UpdateServer(v Server) error {
	// The caller may have read the decrypted token; re-encrypt before persisting
	// (no-op if already an enc: ciphertext). Deterministic so token lookups
	// keep matching.
	v.AgentToken = c.encDet(v.AgentToken)
	return c.Store.UpdateServer(v)
}

// ---- Databases (passwords) ----

func (c *cryptoStore) ListDatabases() ([]Database, error) {
	dbs, err := c.Store.ListDatabases()
	for i := range dbs {
		dbs[i].Password = c.dec(dbs[i].Password)
	}
	return dbs, err
}

func (c *cryptoStore) GetDatabase(id string) (Database, error) {
	v, err := c.Store.GetDatabase(id)
	v.Password = c.dec(v.Password)
	return v, err
}

func (c *cryptoStore) CreateDatabase(v Database) error {
	v.Password = c.enc(v.Password)
	return c.Store.CreateDatabase(v)
}

func (c *cryptoStore) UpdateDatabase(v Database) error {
	v.Password = c.enc(v.Password)
	return c.Store.UpdateDatabase(v)
}
