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
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// GitHub App support: the "manifest flow" lets the user create a fully
// configured GitHub App in one click. We then mint short-lived installation
// access tokens (RS256 JWT -> installation token) for repo listing and clones.
//
// All crypto is stdlib — no external JWT dependency.

// installation token cache (keyed by installation id)
type tokenCache struct {
	mu     sync.Mutex
	token  string
	expiry time.Time
}

func (s *Server) ghTokens() *tokenCache {
	if s.ghTokenCache == nil {
		s.ghTokenCache = &tokenCache{}
	}
	return s.ghTokenCache
}

// baseURL reconstructs the control plane's public URL from the request,
// honoring reverse-proxy headers set by Caddy.
func baseURL(r *http.Request) string {
	scheme := "http"
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = p
	} else if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = h
	}
	return scheme + "://" + host
}

// handleGitHubAppManifest renders an auto-submitting form that POSTs an app
// manifest to GitHub, kicking off the create-app flow.
func (s *Server) handleGitHubAppManifest(w http.ResponseWriter, r *http.Request) {
	base := baseURL(r)
	manifest := map[string]any{
		"name":            "Anchor Deploy",
		"url":             base,
		"public":          false,
		"redirect_url":    base + "/api/github/app/callback",
		"setup_url":       base + "/api/github/setup",
		"setup_on_update": true,
		"hook_attributes": map[string]any{
			"url":    base + "/webhooks/github",
			"active": true,
		},
		"default_permissions": map[string]string{
			"contents": "read",
			"metadata": "read",
		},
		"default_events": []string{"push"},
	}
	mb, _ := json.Marshal(manifest)

	// org install target is optional; user can pick during creation
	action := "https://github.com/settings/apps/new"
	page := template.Must(template.New("m").Parse(`<!doctype html><html><body>
<form id="f" method="post" action="{{.Action}}">
  <input type="hidden" name="manifest" value='{{.Manifest}}'>
</form>
<script>document.getElementById('f').submit()</script>
Redirecting to GitHub…
</body></html>`))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = page.Execute(w, map[string]any{"Action": action, "Manifest": string(mb)})
}

// handleGitHubAppCallback exchanges the temporary manifest code for the app's
// credentials, stores them, and redirects the user to install the app.
func (s *Server) handleGitHubAppCallback(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing code", http.StatusBadRequest)
		return
	}
	req, _ := http.NewRequest("POST", "https://api.github.com/app-manifests/"+code+"/conversions", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		http.Error(w, "github manifest conversion: "+string(b), http.StatusBadGateway)
		return
	}
	var conv struct {
		ID            int64  `json:"id"`
		Slug          string `json:"slug"`
		PEM           string `json:"pem"`
		WebhookSecret string `json:"webhook_secret"`
		ClientID      string `json:"client_id"`
		ClientSecret  string `json:"client_secret"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&conv); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	settings, _ := s.store.Settings()
	settings.GitHubAppID = conv.ID
	settings.GitHubAppSlug = conv.Slug
	settings.GitHubAppPrivateKey = conv.PEM
	settings.GitHubAppWebhookSecret = conv.WebhookSecret
	settings.GitHubClientID = conv.ClientID
	settings.GitHubClientSecret = conv.ClientSecret
	if err := s.store.SaveSettings(settings); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Send the user to install the freshly created app.
	http.Redirect(w, r, "https://github.com/apps/"+conv.Slug+"/installations/new", http.StatusFound)
}

// handleGitHubSetup captures the installation id after the user installs the app.
func (s *Server) handleGitHubSetup(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("installation_id")
	if idStr != "" {
		if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
			settings, _ := s.store.Settings()
			settings.GitHubInstallationID = id
			_ = s.store.SaveSettings(settings)
			s.ghTokens().expiry = time.Time{} // invalidate cached token
		}
	}
	http.Redirect(w, r, "/apps", http.StatusFound)
}

// appJWT mints a short-lived RS256 JWT signed with the app's private key.
func appJWT(appID int64, pemKey string) (string, error) {
	key, err := parseRSAKey(pemKey)
	if err != nil {
		return "", err
	}
	now := time.Now()
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		"iat": now.Add(-30 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": strconv.FormatInt(appID, 10),
	}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	signingInput := b64(hb) + "." + b64(cb)

	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", err
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

func parseRSAKey(pemKey string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemKey))
	if block == nil {
		return nil, errors.New("invalid PEM private key")
	}
	if k, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return k, nil
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	rk, ok := k.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("not an RSA private key")
	}
	return rk, nil
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// installationToken returns a cached or freshly minted installation access token.
func (s *Server) installationToken() (string, error) {
	settings, _ := s.store.Settings()
	if !settings.GitHubAppConfigured() {
		return "", errors.New("github app not configured")
	}
	if settings.GitHubInstallationID == 0 {
		return "", errors.New("github app not installed")
	}

	tc := s.ghTokens()
	tc.mu.Lock()
	defer tc.mu.Unlock()
	if tc.token != "" && time.Now().Before(tc.expiry.Add(-60*time.Second)) {
		return tc.token, nil
	}

	jwt, err := appJWT(settings.GitHubAppID, settings.GitHubAppPrivateKey)
	if err != nil {
		return "", err
	}
	url := fmt.Sprintf("https://api.github.com/app/installations/%d/access_tokens", settings.GitHubInstallationID)
	req, _ := http.NewRequest("POST", url, nil)
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("installation token: %s", string(b))
	}
	var out struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	tc.token = out.Token
	tc.expiry = out.ExpiresAt
	return out.Token, nil
}

// githubCloneToken returns the best available token for git clone: an
// installation token if a GitHub App is set up, else the PAT fallback.
func (s *Server) githubCloneToken() string {
	if tok, err := s.installationToken(); err == nil {
		return tok
	}
	settings, _ := s.store.Settings()
	return settings.GitHubToken
}

// githubWebhookSecret returns the active webhook secret (App secret preferred).
func (s *Server) githubWebhookSecret() string {
	settings, _ := s.store.Settings()
	if settings.GitHubAppConfigured() && settings.GitHubAppWebhookSecret != "" {
		return settings.GitHubAppWebhookSecret
	}
	return settings.WebhookSecret
}

// listInstallationRepos lists repos accessible to the installation.
func (s *Server) listInstallationRepos() ([]repo, error) {
	tok, err := s.installationToken()
	if err != nil {
		return nil, err
	}
	var all []repo
	for page := 1; page <= 10; page++ {
		url := fmt.Sprintf("https://api.github.com/installation/repositories?per_page=100&page=%d", page)
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Accept", "application/vnd.github+json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		var body struct {
			Repositories []repo `json:"repositories"`
		}
		err = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		all = append(all, body.Repositories...)
		if len(body.Repositories) < 100 {
			break
		}
	}
	return all, nil
}

type repo struct {
	FullName      string `json:"full_name"`
	CloneURL      string `json:"clone_url"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
}
