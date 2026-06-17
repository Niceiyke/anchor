package control

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/oyomworld/anchor/internal/store"
	"github.com/oyomworld/anchor/pkg/protocol"
)

// validHostname reports whether d is a plausible DNS hostname: dot-separated
// labels of [a-z0-9-], no empty labels, no leading/trailing hyphen per label.
// Input is assumed already lowercased/trimmed by normalizeBaseDomain.
func validHostname(d string) bool {
	if d == "" || len(d) > 253 || !strings.Contains(d, ".") {
		return false
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

// normalizeRoutes validates and cleans an app's extra routes, assigning a
// subdomain to any route left without a domain. Each route needs a service (so
// the agent can tell the project's services apart) and a unique, valid domain
// that doesn't collide with the primary domain or another route.
func (s *Server) normalizeRoutes(appName, primaryDomain string, routes []protocol.Route) ([]protocol.Route, error) {
	out := make([]protocol.Route, 0, len(routes))
	seen := map[string]bool{strings.ToLower(strings.TrimSpace(primaryDomain)): true}
	for _, r := range routes {
		r.Service = strings.TrimSpace(r.Service)
		r.Domain = normalizeBaseDomain(r.Domain)
		r.HealthPath = strings.TrimSpace(r.HealthPath)
		if r.Service == "" {
			return nil, fmt.Errorf("each route needs a service")
		}
		if r.Port < 0 || r.Port > 65535 {
			return nil, fmt.Errorf("route port out of range")
		}
		if r.Domain == "" {
			r.Domain = s.assignDomainSlug(protocol.Sanitize(appName + "-" + r.Service))
			if r.Domain == "" {
				return nil, fmt.Errorf("route for service %q needs a domain (no base domain configured)", r.Service)
			}
		}
		if !validHostname(r.Domain) {
			return nil, fmt.Errorf("invalid route domain %q", r.Domain)
		}
		if seen[r.Domain] {
			return nil, fmt.Errorf("duplicate route domain %q", r.Domain)
		}
		seen[r.Domain] = true
		out = append(out, r)
	}
	return out, nil
}

// appDomains returns every public domain an app serves: its primary Domain plus
// each route's domain, lowercased and de-duplicated. The single source of truth
// for "which hostnames does this app own", used by DNS provisioning and the
// on-demand-TLS ask endpoint.
func appDomains(a store.App) []string {
	seen := map[string]bool{}
	var out []string
	add := func(d string) {
		d = strings.ToLower(strings.TrimSpace(d))
		if d != "" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	add(a.Domain)
	for _, r := range a.Routes {
		add(r.Domain)
	}
	return out
}

// normalizeBaseDomain cleans user input into a bare domain suffix:
// strips scheme, leading dots, trailing slashes, and lowercases.
func normalizeBaseDomain(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.Trim(s, "/.")
	return s
}

// assignDomain returns an auto-generated <slug>.<base_domain> for an app when a
// base domain is configured, ensuring it doesn't collide with an existing app's
// domain. Returns "" if no base domain is set (the app simply has no domain).
func (s *Server) assignDomain(appName string) string {
	return s.assignDomainSlug(protocol.Sanitize(appName))
}

// assignDomainSlug returns an unused <slug>.<base_domain>, suffixing -N on
// collision. Considers every domain already in use (primary and route domains).
// Returns "" when no base domain is configured.
func (s *Server) assignDomainSlug(slug string) string {
	settings, _ := s.store.Settings()
	base := normalizeBaseDomain(settings.BaseDomain)
	if base == "" {
		return ""
	}
	if slug == "" {
		slug = "app"
	}

	apps, _ := s.store.ListApps()
	taken := map[string]bool{}
	for _, a := range apps {
		for _, d := range appDomains(a) {
			taken[d] = true
		}
	}

	cand := slug + "." + base
	for i := 2; taken[strings.ToLower(cand)]; i++ {
		cand = slug + "-" + strconv.Itoa(i) + "." + base
	}
	return cand
}

// handleTLSCheck is the on-demand-TLS "ask" endpoint Caddy calls before issuing
// a certificate for a hostname. It is intentionally unauthenticated (Caddy
// can't present credentials) and only confirms whether the requested domain is
// one Anchor actually manages — so Caddy won't mint certs for arbitrary hosts
// pointed at this server's IP.
func (s *Server) handleTLSCheck(w http.ResponseWriter, r *http.Request) {
	domain := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("domain")))
	if domain == "" {
		http.Error(w, "no domain", http.StatusBadRequest)
		return
	}
	apps, _ := s.store.ListApps()
	for _, a := range apps {
		for _, d := range appDomains(a) {
			if d == domain {
				w.WriteHeader(http.StatusOK)
				return
			}
		}
	}
	http.Error(w, "unknown domain", http.StatusNotFound)
}
