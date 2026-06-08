package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/oyomworld/anchor/pkg/protocol"
)

// selfSHA returns the SHA-256 of the running agent binary, computed once. It is
// reported in the Hello reply so the control plane can detect a version drift
// against its bundled binary and trigger an auto-update.
func (a *Agent) selfSHA() string {
	a.shaOnce.Do(func() {
		exe, err := os.Executable()
		if err != nil {
			return
		}
		f, err := os.Open(exe)
		if err != nil {
			return
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return
		}
		a.sha = hex.EncodeToString(h.Sum(nil))
	})
	return a.sha
}

// selfUpdate downloads the control plane's bundled agent binary, verifies it
// against the expected SHA, atomically swaps it over the running executable, and
// exits so systemd (Restart=always) relaunches the new binary.
func (a *Agent) selfUpdate(ctx context.Context, req protocol.UpdateAgentRequest) {
	exe, err := os.Executable()
	if err != nil {
		log.Printf("self-update: cannot locate own binary: %v", err)
		return
	}
	exe, _ = filepath.EvalSymlinks(exe)

	url := a.cfg.ControlPlaneURL + "/agent/download?arch=" + runtime.GOARCH
	log.Printf("self-update: downloading new binary from %s", url)
	newBin, err := a.download(ctx, url)
	if err != nil {
		log.Printf("self-update: download failed: %v", err)
		return
	}

	sum := sha256.Sum256(newBin)
	got := hex.EncodeToString(sum[:])
	if req.SHA256 != "" && got != req.SHA256 {
		log.Printf("self-update: sha mismatch (want %s, got %s) — aborting", req.SHA256, got)
		return
	}
	if got == a.selfSHA() {
		log.Printf("self-update: downloaded binary matches running one — nothing to do")
		return
	}

	// Write next to the current binary (same filesystem) so the rename is atomic,
	// then swap it into place. Renaming over a running executable is safe on
	// Linux: the running process keeps its open inode.
	tmp := exe + ".new"
	if err := os.WriteFile(tmp, newBin, 0o755); err != nil {
		log.Printf("self-update: write temp binary: %v", err)
		return
	}
	if err := os.Rename(tmp, exe); err != nil {
		log.Printf("self-update: swap binary: %v", err)
		_ = os.Remove(tmp)
		return
	}

	log.Printf("self-update: installed new agent binary (%s) — restarting", got[:12])
	os.Exit(0) // systemd Restart=always relaunches the updated binary
}

// download fetches a URL into memory with a bounded size.
func (a *Agent) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 128<<20)) // 128 MB cap
}
