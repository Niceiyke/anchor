package control

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/oyomworld/anchor/internal/store"
	"github.com/oyomworld/anchor/pkg/protocol"
)

// notifyDeployStatus sends a notification webhook when a deploy phase changes.
func (s *Server) notifyDeployStatus(appID string, dep store.Deployment, ds protocol.DeployStatus) {
	settings, _ := s.store.Settings()
	if settings.NotificationWebhook == "" {
		return
	}
	app, err := s.store.GetApp(appID)
	if err != nil {
		return
	}
	name := app.Name
	phase := string(ds.Phase)
	msg := ds.Message
	color := getColor(phase)
	payload := map[string]any{
		"embeds": []map[string]any{{
			"title":       name + " — " + phase,
			"description": msg,
			"color":       color,
			"fields": []map[string]any{
				{"name": "App", "value": name, "inline": true},
				{"name": "Server", "value": app.ServerID, "inline": true},
				{"name": "Commit", "value": dep.CommitSHA, "inline": true},
			},
			"timestamp": time.Now().UTC().Format(time.RFC3339),
		}},
	}
	body, _ := json.Marshal(payload)
	resp, err := http.Post(settings.NotificationWebhook, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("notification webhook: %v", err)
		return
	}
	resp.Body.Close()
}

func (s *Server) notifyAppAppropriate(appID string, title, desc string) {
	settings, _ := s.store.Settings()
	if settings.NotificationWebhook == "" {
		return
	}
	app, err := s.store.GetApp(appID)
	if err != nil {
		return
	}
	payload := map[string]any{
		"content": title + ": " + app.Name,
	}
	b, _ := json.Marshal(payload)
	_ = desc // used for structured output
	resp, err := http.Post(settings.NotificationWebhook, "application/json", bytes.NewReader(b))
	if err != nil {
		return
	}
	resp.Body.Close()
}

func getColor(phase string) int {
	switch phase {
	case "success":
		return 0x3FB950 // green
	case "failed":
		return 0xF85149 // red
	case "queued", "building", "starting", "cloning", "detecting", "configuring":
		return 0xD29922 // yellow
	default:
		return 0x8B949E // gray
	}
}
