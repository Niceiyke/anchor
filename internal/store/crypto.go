package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"strings"
)

// encPrefix marks a value as encrypted, so we can tell ciphertext from legacy
// plaintext and migrate transparently on next write.
const encPrefix = "enc:v1:"

// cryptoStore wraps a Store and transparently encrypts sensitive Settings
// fields at rest with AES-256-GCM. All other operations pass straight through
// via the embedded Store interface.
type cryptoStore struct {
	Store
	gcm cipher.AEAD
}

// NewEncrypted wraps inner so secret Settings fields are encrypted at rest.
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
	return &cryptoStore{Store: inner, gcm: gcm}, nil
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

func (c *cryptoStore) dec(s string) string {
	if !strings.HasPrefix(s, encPrefix) {
		return s // legacy plaintext
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

// secretFields are the Settings fields encrypted at rest. AdminPass is omitted
// (it's already a one-way bcrypt hash).
func (c *cryptoStore) transform(s *Settings, fn func(string) string) {
	s.GitHubToken = fn(s.GitHubToken)
	s.WebhookSecret = fn(s.WebhookSecret)
	s.GitHubAppPrivateKey = fn(s.GitHubAppPrivateKey)
	s.GitHubAppWebhookSecret = fn(s.GitHubAppWebhookSecret)
	s.GitHubClientSecret = fn(s.GitHubClientSecret)
}

func (c *cryptoStore) Settings() (Settings, error) {
	v, err := c.Store.Settings()
	if err != nil {
		return v, err
	}
	c.transform(&v, c.dec)
	return v, nil
}

func (c *cryptoStore) SaveSettings(v Settings) error {
	c.transform(&v, c.enc)
	return c.Store.SaveSettings(v)
}
