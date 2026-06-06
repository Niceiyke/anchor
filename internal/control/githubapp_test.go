package control

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
)

func TestAppJWT(t *testing.T) {
	// generate a throwaway RSA key and encode it as PKCS#1 PEM (GitHub's format)
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	tok, err := appJWT(12345, string(pemBytes))
	if err != nil {
		t.Fatalf("appJWT: %v", err)
	}

	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3 JWT parts, got %d", len(parts))
	}

	// header alg must be RS256
	hb, _ := base64.RawURLEncoding.DecodeString(parts[0])
	var hdr map[string]string
	if err := json.Unmarshal(hb, &hdr); err != nil {
		t.Fatal(err)
	}
	if hdr["alg"] != "RS256" {
		t.Errorf("alg = %q, want RS256", hdr["alg"])
	}

	// claims iss must be the app id
	cb, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var claims map[string]any
	if err := json.Unmarshal(cb, &claims); err != nil {
		t.Fatal(err)
	}
	if claims["iss"] != "12345" {
		t.Errorf("iss = %v, want 12345", claims["iss"])
	}

	// signature must verify against the public key
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], sig); err != nil {
		t.Errorf("signature verification failed: %v", err)
	}
}

func TestParseRSAKeyPKCS8(t *testing.T) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	if _, err := parseRSAKey(string(pemBytes)); err != nil {
		t.Fatalf("PKCS8 parse failed: %v", err)
	}
}
