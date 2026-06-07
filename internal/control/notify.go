package control

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/oyomworld/anchor/internal/store"
	"github.com/oyomworld/anchor/pkg/protocol"
)

// notifyDeployStatus posts a notification webhook when a deploy reaches a
// terminal phase (success or failed). It is intended to be called in its own
// goroutine — it performs a blocking outbound HTTP request and must not run on
// the agent event-ingest path.
func (s *Server) notifyDeployStatus(appID string, dep store.Deployment, ds protocol.DeployStatus) {
	// Only notify on terminal phases — intermediate phases would be spammy.
	if ds.Phase != protocol.PhaseSuccess && ds.Phase != protocol.PhaseFailed {
		return
	}
	settings, _ := s.store.Settings()
	if settings.NotificationWebhook == "" {
		return
	}
	app, err := s.store.GetApp(appID)
	if err != nil {
		return
	}

	phase := string(ds.Phase)
	commit := dep.CommitSHA
	if commit == "" {
		commit = "manual"
	} else if len(commit) > 7 {
		commit = commit[:7]
	}

	payload := buildNotificationPayload(settings.NotificationWebhook, app.Name, phase, ds.Message, commit)
	body, _ := json.Marshal(payload)
	resp, err := http.Post(settings.NotificationWebhook, "application/json", bytes.NewReader(body))
	if err != nil {
		log.Printf("notification webhook: %v", err)
		return
	}
	resp.Body.Close()
}

// buildNotificationPayload renders a provider-appropriate JSON body. Slack
// expects a top-level "text" field; Discord renders rich "embeds". Unknown
// webhooks default to Slack-style "text", which most generic receivers accept.
func buildNotificationPayload(webhookURL, app, phase, msg, commit string) map[string]any {
	summary := app + " — " + phase
	detail := msg
	if detail == "" {
		detail = "commit " + commit
	} else {
		detail = detail + " (" + commit + ")"
	}

	if isDiscordWebhook(webhookURL) {
		return map[string]any{
			"embeds": []map[string]any{{
				"title":       summary,
				"description": detail,
				"color":       phaseColor(phase),
				"timestamp":   time.Now().UTC().Format(time.RFC3339),
			}},
		}
	}
	// Slack and generic webhooks.
	return map[string]any{"text": summary + " — " + detail}
}

func isDiscordWebhook(u string) bool {
	return strings.Contains(u, "discord.com/api/webhooks") || strings.Contains(u, "discordapp.com/api/webhooks")
}

func phaseColor(phase string) int {
	switch phase {
	case "success":
		return 0x3FB950 // green
	case "failed":
		return 0xF85149 // red
	default:
		return 0xD29922 // yellow
	}
}
