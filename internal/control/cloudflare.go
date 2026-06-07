package control

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

const cfAPI = "https://api.cloudflare.com/client/v4"

// cfClient is a minimal Cloudflare DNS client (stdlib only). It caches resolved
// zone IDs. All calls take the API token explicitly so a token change in
// Settings takes effect immediately.
type cfClient struct {
	http    *http.Client
	mu      sync.Mutex
	zoneIDs map[string]string // zone name -> id
}

func newCFClient() *cfClient {
	return &cfClient{http: &http.Client{Timeout: 12 * time.Second}, zoneIDs: map[string]string{}}
}

type cfZone struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// pickZone returns the most specific accessible zone that the domain belongs to
// (the longest zone name that equals the domain or is a dot-boundary suffix).
func pickZone(domain string, zones []cfZone) (cfZone, bool) {
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	var best cfZone
	found := false
	for _, z := range zones {
		name := strings.ToLower(z.Name)
		if domain == name || strings.HasSuffix(domain, "."+name) {
			if len(name) > len(best.Name) {
				best, found = z, true
			}
		}
	}
	return best, found
}

func (c *cfClient) do(token, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, cfAPI+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var env struct {
		Success bool              `json:"success"`
		Errors  []json.RawMessage `json:"errors"`
		Result  json.RawMessage   `json:"result"`
	}
	_ = json.Unmarshal(raw, &env)
	if resp.StatusCode/100 != 2 || !env.Success {
		return fmt.Errorf("cloudflare %s %s: %s", method, path, strings.TrimSpace(string(raw)))
	}
	if out != nil && len(env.Result) > 0 {
		return json.Unmarshal(env.Result, out)
	}
	return nil
}

// zoneIDForDomain finds (and caches) the Cloudflare zone id that owns domain.
func (c *cfClient) zoneIDForDomain(token, domain string) (string, error) {
	var zones []cfZone
	if err := c.do(token, "GET", "/zones?per_page=50", nil, &zones); err != nil {
		return "", err
	}
	z, ok := pickZone(domain, zones)
	if !ok {
		return "", fmt.Errorf("no Cloudflare zone found for %s (token can't access it?)", domain)
	}
	c.mu.Lock()
	c.zoneIDs[z.Name] = z.ID
	c.mu.Unlock()
	return z.ID, nil
}

type cfRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     int    `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

func (c *cfClient) findRecord(token, zoneID, name string) (string, error) {
	var recs []cfRecord
	if err := c.do(token, "GET", "/zones/"+zoneID+"/dns_records?type=A&name="+name, nil, &recs); err != nil {
		return "", err
	}
	if len(recs) > 0 {
		return recs[0].ID, nil
	}
	return "", nil
}

// upsertA creates or updates an A record name -> ip (grey-cloud / not proxied so
// Caddy can complete its TLS challenge directly against the origin).
func (c *cfClient) upsertA(token, zoneID, name, ip string) error {
	rec := cfRecord{Type: "A", Name: name, Content: ip, TTL: 1, Proxied: false}
	id, err := c.findRecord(token, zoneID, name)
	if err != nil {
		return err
	}
	if id == "" {
		return c.do(token, "POST", "/zones/"+zoneID+"/dns_records", rec, nil)
	}
	return c.do(token, "PUT", "/zones/"+zoneID+"/dns_records/"+id, rec, nil)
}

func (c *cfClient) deleteA(token, zoneID, name string) error {
	id, err := c.findRecord(token, zoneID, name)
	if err != nil || id == "" {
		return err
	}
	return c.do(token, "DELETE", "/zones/"+zoneID+"/dns_records/"+id, nil, nil)
}

// ---- Server integration ----

// cfConfig returns the API token and target IP if Cloudflare DNS is enabled.
func (s *Server) cfConfig() (token, ip string, ok bool) {
	settings, _ := s.store.Settings()
	token = strings.TrimSpace(settings.CloudflareAPIToken)
	if token == "" {
		return "", "", false
	}
	ip = strings.TrimSpace(settings.PublicIP)
	if ip == "" {
		ip = s.detectPublicIP()
	}
	if ip == "" {
		return "", "", false
	}
	return token, ip, true
}

// detectPublicIP best-effort discovers this host's public IP (cached).
func (s *Server) detectPublicIP() string {
	resp, err := http.Get("https://api.ipify.org")
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64))
	return strings.TrimSpace(string(b))
}

// cfEnsureDomain creates/updates the A record for domain (best-effort, async).
func (s *Server) cfEnsureDomain(domain string) {
	domain = strings.TrimSpace(strings.ToLower(domain))
	token, ip, ok := s.cfConfig()
	if !ok || domain == "" {
		return
	}
	zoneID, err := s.cf.zoneIDForDomain(token, domain)
	if err != nil {
		log.Printf("cloudflare: %v", err)
		return
	}
	if err := s.cf.upsertA(token, zoneID, domain, ip); err != nil {
		log.Printf("cloudflare: upsert %s: %v", domain, err)
		return
	}
	log.Printf("cloudflare: %s A -> %s", domain, ip)
}

// cfDeleteDomain removes the A record for domain (best-effort, async).
func (s *Server) cfDeleteDomain(domain string) {
	domain = strings.TrimSpace(strings.ToLower(domain))
	token, _, ok := s.cfConfig()
	if !ok || domain == "" {
		return
	}
	zoneID, err := s.cf.zoneIDForDomain(token, domain)
	if err != nil {
		return
	}
	if err := s.cf.deleteA(token, zoneID, domain); err != nil {
		log.Printf("cloudflare: delete %s: %v", domain, err)
	}
}

// handleCloudflareVerify checks the saved token by resolving the zone for the
// base domain (or a ?domain= override), so the UI can confirm setup.
func (s *Server) handleCloudflareVerify(w http.ResponseWriter, r *http.Request) {
	settings, _ := s.store.Settings()
	token := strings.TrimSpace(settings.CloudflareAPIToken)
	if token == "" {
		http.Error(w, "no Cloudflare API token configured", http.StatusPreconditionRequired)
		return
	}
	domain := strings.TrimSpace(r.URL.Query().Get("domain"))
	if domain == "" {
		domain = normalizeBaseDomain(settings.BaseDomain)
	}
	if domain == "" {
		http.Error(w, "set a base domain (or pass ?domain=) to verify against", http.StatusBadRequest)
		return
	}
	var zones []cfZone
	if err := s.cf.do(token, "GET", "/zones?per_page=50", nil, &zones); err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	z, found := pickZone(domain, zones)
	if !found {
		http.Error(w, "token has no zone covering "+domain, http.StatusBadGateway)
		return
	}
	_, ip, _ := s.cfConfig()
	writeJSON(w, http.StatusOK, map[string]string{"zone": z.Name, "zone_id": z.ID, "public_ip": ip})
}
