package store

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/oyomworld/anchor/pkg/protocol"
)

// TestAppRoundTrip ensures every persisted App field survives a write/read,
// across both backends — in particular routes, service, and the health-gating
// fields, which are easy to drop from the column list.
func TestAppRoundTrip(t *testing.T) {
	json, _ := Open(filepath.Join(t.TempDir(), "s.json"))
	sqlite, err := OpenSQLite(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	want := App{
		ID: "app_1", Name: "shop", ServerID: "s1", RepoFullName: "o/shop",
		RepoURL: "https://x/shop.git", Branch: "main", Domain: "shop.example.com",
		ContainerPort: 3000, AutoDeploy: true,
		EnvVars:     map[string]string{"K": "v"},
		EnvSecret:   map[string]bool{"K": false},
		ComposeFile: "deploy/compose.yaml", Service: "web",
		Routes: []protocol.Route{
			{Service: "api", Domain: "api.example.com", Port: 8080, HealthPath: "/healthz"},
		},
		HealthPath: "/health", HealthTimeoutSecs: 90, AutoRollback: true,
		LastGoodSHA: "abc123",
		CreatedAt:   time.Now().UTC().Truncate(time.Second),
	}
	for name, st := range map[string]Store{"json": json, "sqlite": sqlite} {
		t.Run(name, func(t *testing.T) {
			if err := st.CreateApp(want); err != nil {
				t.Fatal(err)
			}
			got, err := st.GetApp("app_1")
			if err != nil {
				t.Fatal(err)
			}
			got.ContainerName = "" // computed on read in the API layer, not persisted
			if !reflect.DeepEqual(got, want) {
				t.Errorf("round-trip mismatch\n got: %+v\nwant: %+v", got, want)
			}
		})
	}
}
