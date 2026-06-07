package control

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/oyomworld/anchor/pkg/protocol"
)

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
	settings, _ := s.store.Settings()
	base := normalizeBaseDomain(settings.BaseDomain)
	if base == "" {
		return ""
	}
	slug := protocol.Sanitize(appName)
	if slug == "" {
		slug = "app"
	}

	apps, _ := s.store.ListApps()
	taken := map[string]bool{}
	for _, a := range apps {
		if a.Domain != "" {
			taken[strings.ToLower(a.Domain)] = true
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
		if strings.EqualFold(a.Domain, domain) {
			w.WriteHeader(http.StatusOK)
			return
		}
	}
	http.Error(w, "unknown domain", http.StatusNotFound)
}
