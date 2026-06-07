package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// TestEncryptedAgentTokenLookup guards the regression where agent tokens were
// encrypted with a random nonce, making GetServerByToken (an exact-match lookup)
// fail for every server — breaking agent authentication entirely.
func TestEncryptedAgentTokenLookup(t *testing.T) {
	inner, _ := Open(filepath.Join(t.TempDir(), "s.json"))
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	enc, err := NewEncrypted(inner, key)
	if err != nil {
		t.Fatal(err)
	}

	const tok = "agent-secret-token-123"
	if err := enc.CreateServer(Server{ID: "srv_1", Name: "vps", AgentToken: tok, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	// The token must be ciphertext at rest, not plaintext.
	raw, _ := inner.GetServer("srv_1")
	if !strings.HasPrefix(raw.AgentToken, encPrefix) || strings.Contains(raw.AgentToken, tok) {
		t.Errorf("agent token not encrypted at rest: %q", raw.AgentToken)
	}

	// The agent authenticates with the plaintext token — it must resolve.
	srv, err := enc.GetServerByToken(tok)
	if err != nil {
		t.Fatalf("agent auth broken: GetServerByToken(plaintext) -> %v", err)
	}
	if srv.ID != "srv_1" || srv.AgentToken != tok {
		t.Fatalf("wrong server/token: %+v", srv)
	}

	// EnsureLocalServer-style idempotency: a second lookup still resolves (the
	// deterministic ciphertext is stable), so no duplicate row is created.
	if _, err := enc.GetServerByToken(tok); err != nil {
		t.Fatalf("second lookup failed (would cause duplicate local servers): %v", err)
	}
}

func TestEncryptedAppEnvAtRest(t *testing.T) {
	inner, _ := Open(filepath.Join(t.TempDir(), "s.json"))
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	enc, err := NewEncrypted(inner, key)
	if err != nil {
		t.Fatal(err)
	}

	app := App{ID: "app_1", Name: "x", ServerID: "s1", EnvVars: map[string]string{"JWT_SECRET": "super-secret", "PUBLIC": "ok"}}
	if err := enc.CreateApp(app); err != nil {
		t.Fatal(err)
	}

	// The caller's map must NOT have been mutated to ciphertext.
	if app.EnvVars["JWT_SECRET"] != "super-secret" {
		t.Fatalf("caller map was mutated: %q", app.EnvVars["JWT_SECRET"])
	}

	// At rest (inner store) the values are ciphertext, keys are plaintext.
	raw, _ := inner.GetApp("app_1")
	if !strings.HasPrefix(raw.EnvVars["JWT_SECRET"], encPrefix) || strings.Contains(raw.EnvVars["JWT_SECRET"], "super-secret") {
		t.Errorf("env value not encrypted at rest: %q", raw.EnvVars["JWT_SECRET"])
	}
	if _, ok := raw.EnvVars["JWT_SECRET"]; !ok {
		t.Error("env key should be preserved in plaintext")
	}

	// Through the wrapper, values decrypt back.
	got, _ := enc.GetApp("app_1")
	if got.EnvVars["JWT_SECRET"] != "super-secret" || got.EnvVars["PUBLIC"] != "ok" {
		t.Errorf("decrypt env = %+v", got.EnvVars)
	}

	// Reading twice must not corrupt the at-rest data (in-place mutation guard).
	_, _ = enc.GetApp("app_1")
	raw2, _ := inner.GetApp("app_1")
	if !strings.HasPrefix(raw2.EnvVars["JWT_SECRET"], encPrefix) {
		t.Error("at-rest value became plaintext after a read (map aliasing bug)")
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
