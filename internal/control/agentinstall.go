package control

import (
	"embed"
	"net/http"
	"strings"
)

// Agent install assets served by the control plane so a fresh VPS can be
// enrolled with a single command:
//
//	curl -fsSL https://anchor.example.com/install.sh | sudo bash -s -- --token=<token>
//
// The installer downloads the version-matched agent binary from /agent/download
// and the app-router Caddyfile from /agent/caddyfile — all from the same origin
// the agent will dial out to, so there is nothing to scp or build by hand.

//go:embed agentassets/install.sh
var installScript string

//go:embed agentassets/Caddyfile
var agentCaddyfile string

// agentBinFS holds the cross-compiled linux agent binaries. They are built into
// internal/control/agentbin by `make agent-embed`; when absent (e.g. a bare
// `go build`), /agent/download reports that no binary is bundled.
//
//go:embed agentbin
var agentBinFS embed.FS

// handleInstallScript serves install.sh with the control plane's own URL baked
// in. Public: the script and binary are not secret — the agent token (passed by
// the operator as --token) is what authorizes enrollment.
func (s *Server) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	script := strings.ReplaceAll(installScript, "__ANCHOR_URL__", baseURL(r))
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(script))
}

// handleAgentCaddyfile serves the per-VPS app-router Caddyfile fetched by the
// installer.
func (s *Server) handleAgentCaddyfile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(agentCaddyfile))
}

// handleAgentDownload serves the cross-compiled agent binary for the requested
// architecture (amd64 or arm64, default amd64).
func (s *Server) handleAgentDownload(w http.ResponseWriter, r *http.Request) {
	arch := r.URL.Query().Get("arch")
	switch arch {
	case "", "amd64", "x86_64":
		arch = "amd64"
	case "arm64", "aarch64":
		arch = "arm64"
	default:
		http.Error(w, "unsupported arch: "+arch, http.StatusBadRequest)
		return
	}
	bin, err := agentBinFS.ReadFile("agentbin/anchor-agent-linux-" + arch)
	if err != nil {
		http.Error(w, "agent binary not bundled for linux/"+arch+
			" — rebuild the control plane with `make agent-embed`", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="anchor-agent"`)
	_, _ = w.Write(bin)
}
