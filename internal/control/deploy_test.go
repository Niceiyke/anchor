package control

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/oyomworld/anchor/internal/store"
	"github.com/oyomworld/anchor/pkg/protocol"
)

// rollbackTarget runs maybeAutoRollback for an app/deployment pair and returns
// the commit SHA of the dispatched rollback deploy, or "" if none was sent.
func rollbackTarget(t *testing.T, app store.App, failedSHA string) string {
	t.Helper()
	srv, st := newTestServer(t)
	app.ID = "app1"
	app.ServerID = "srv1"
	app.Name = "demo"
	app.RepoURL = "https://example.com/x/y"
	if err := st.CreateApp(app); err != nil {
		t.Fatal(err)
	}
	failed := store.Deployment{ID: "dep_failed", AppID: app.ID, CommitSHA: failedSHA, CreatedAt: time.Now()}
	if err := st.CreateDeployment(failed); err != nil {
		t.Fatal(err)
	}
	c := srv.hub.register(app.ServerID) // so triggerDeploy's hub.Send succeeds

	srv.maybeAutoRollback(failed)

	select {
	case cmd := <-c.ch:
		if cmd.Type != protocol.CmdDeploy {
			t.Fatalf("expected deploy command, got %q", cmd.Type)
		}
		var req protocol.DeployRequest
		if err := json.Unmarshal(cmd.Data, &req); err != nil {
			t.Fatal(err)
		}
		return req.CommitSHA
	case <-time.After(200 * time.Millisecond):
		return ""
	}
}

func TestMaybeAutoRollback(t *testing.T) {
	t.Run("rolls back to last good commit", func(t *testing.T) {
		got := rollbackTarget(t, store.App{AutoRollback: true, LastGoodSHA: "good123"}, "bad999")
		if got != "good123" {
			t.Fatalf("expected rollback to good123, got %q", got)
		}
	})

	t.Run("disabled: no rollback", func(t *testing.T) {
		if got := rollbackTarget(t, store.App{AutoRollback: false, LastGoodSHA: "good123"}, "bad999"); got != "" {
			t.Fatalf("expected no rollback, got %q", got)
		}
	})

	t.Run("no last-good commit: no rollback", func(t *testing.T) {
		if got := rollbackTarget(t, store.App{AutoRollback: true, LastGoodSHA: ""}, "bad999"); got != "" {
			t.Fatalf("expected no rollback, got %q", got)
		}
	})

	t.Run("failed commit is the last-good one: no loop", func(t *testing.T) {
		if got := rollbackTarget(t, store.App{AutoRollback: true, LastGoodSHA: "good123"}, "good123"); got != "" {
			t.Fatalf("expected no rollback (loop guard), got %q", got)
		}
	})
}
