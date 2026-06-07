package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestEncryptedSettingsAtRest(t *testing.T) {
	inner, err := Open(filepath.Join(t.TempDir(), "s.json"))
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	enc, err := NewEncrypted(inner, key)
	if err != nil {
		t.Fatal(err)
	}

	if err := enc.SaveSettings(Settings{
		AdminUser:           "admin",
		AdminPass:           "$2a$bcrypt-hash",
		GitHubToken:         "ghp_secret",
		GitHubAppPrivateKey: "-----BEGIN RSA PRIVATE KEY-----",
	}); err != nil {
		t.Fatal(err)
	}

	// Raw (inner) values must be ciphertext, not plaintext.
	raw, _ := inner.Settings()
	if !strings.HasPrefix(raw.GitHubToken, encPrefix) {
		t.Errorf("token not encrypted at rest: %q", raw.GitHubToken)
	}
	if strings.Contains(raw.GitHubToken, "ghp_secret") {
		t.Error("plaintext token leaked to storage")
	}
	if !strings.HasPrefix(raw.GitHubAppPrivateKey, encPrefix) {
		t.Error("private key not encrypted at rest")
	}
	// AdminPass (already a hash) is not encrypted.
	if strings.HasPrefix(raw.AdminPass, encPrefix) {
		t.Error("AdminPass should not be encrypted")
	}

	// Through the wrapper, values decrypt back.
	got, _ := enc.Settings()
	if got.GitHubToken != "ghp_secret" {
		t.Errorf("decrypt token = %q", got.GitHubToken)
	}
	if got.GitHubAppPrivateKey != "-----BEGIN RSA PRIVATE KEY-----" {
		t.Errorf("decrypt key = %q", got.GitHubAppPrivateKey)
	}
}

func TestEncryptedReadsLegacyPlaintext(t *testing.T) {
	inner, _ := Open(filepath.Join(t.TempDir(), "s.json"))
	// simulate a pre-encryption install: plaintext token in storage
	_ = inner.SaveSettings(Settings{GitHubToken: "legacy-plain"})

	key := make([]byte, 32)
	enc, _ := NewEncrypted(inner, key)
	got, _ := enc.Settings()
	if got.GitHubToken != "legacy-plain" {
		t.Errorf("legacy plaintext should read through unchanged, got %q", got.GitHubToken)
	}
}
