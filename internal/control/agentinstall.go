package control

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/oyomworld/anchor/pkg/protocol"
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

// ---- Agent auto-update ------------------------------------------------------

var (
	agentSHAOnce sync.Once
	agentSHAs    = map[string]string{} // arch -> sha256 of the bundled binary
)

// bundledAgentSHA returns the SHA-256 of the embedded agent binary for an arch,
// or ok=false when no binary is bundled (e.g. a bare `go build`).
func bundledAgentSHA(arch string) (string, bool) {
	agentSHAOnce.Do(func() {
		for _, a := range []string{"amd64", "arm64"} {
			if b, err := agentBinFS.ReadFile("agentbin/anchor-agent-linux-" + a); err == nil {
				sum := sha256.Sum256(b)
				agentSHAs[a] = hex.EncodeToString(sum[:])
			}
		}
	})
	s, ok := agentSHAs[arch]
	return s, ok
}

// maybeUpdateAgent compares the agent's running binary against the control
// plane's bundled one and dispatches a self-update command when they differ.
// A per-server cooldown prevents an update loop if the swap keeps failing.
func (s *Server) maybeUpdateAgent(serverID string, h protocol.Hello) {
	if h.Arch == "" || h.BinSHA == "" {
		return // older agent that doesn't report its binary; nothing to do
	}
	want, ok := bundledAgentSHA(h.Arch)
	if !ok || want == h.BinSHA {
		return // no bundled binary for this arch, or already up to date
	}

	s.agentUpdateMu.Lock()
	last := s.agentUpdated[serverID]
	if time.Since(last) < 10*time.Minute {
		s.agentUpdateMu.Unlock()
		return
	}
	s.agentUpdated[serverID] = time.Now()
	s.agentUpdateMu.Unlock()

	data, _ := json.Marshal(protocol.UpdateAgentRequest{SHA256: want})
	if s.hub.Send(serverID, protocol.Command{Type: protocol.CmdUpdateAgent, Data: data}) {
		log.Printf("agent %s: pushing self-update (%s -> %s)", serverID, short(h.BinSHA), short(want))
	}
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}
