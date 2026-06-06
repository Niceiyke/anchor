import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";

interface SettingsStatus {
  admin_user: string;
  github_token_set: boolean;
  webhook_secret_set: boolean;
  github_app_configured: boolean;
  github_app_installed: boolean;
  github_app_slug: string;
}

export function Settings() {
  const qc = useQueryClient();
  const { data } = useQuery({ queryKey: ["settings"], queryFn: () => api.get<SettingsStatus>("/api/settings") });
  const [token, setToken] = useState("");

  const saveToken = useMutation({
    mutationFn: () => api.put("/api/settings", { github_token: token }),
    onSuccess: () => {
      setToken("");
      qc.invalidateQueries({ queryKey: ["settings"] });
      qc.invalidateQueries({ queryKey: ["repos"] });
    },
  });

  return (
    <>
      <h2>Settings</h2>

      <div className="card">
        <strong>GitHub App</strong>
        <p className="muted">
          Recommended. Creates a GitHub App via the manifest flow — short-lived
          installation tokens, auto-managed webhook, fine-grained repo access.
        </p>
        {data?.github_app_configured ? (
          <div>
            <div><span className="dot on" /> App configured: <b>{data.github_app_slug}</b></div>
            <div style={{ marginTop: 6 }}>
              {data.github_app_installed
                ? <span><span className="dot on" /> Installed</span>
                : <span><span className="dot off" /> Not installed yet — </span>}
              {!data.github_app_installed && (
                <a href={`https://github.com/apps/${data.github_app_slug}/installations/new`}>install it</a>
              )}
            </div>
            <a className="btn secondary" style={{ marginTop: 12, display: "inline-block" }} href="/api/github/app/manifest">
              Re-create app
            </a>
          </div>
        ) : (
          <a className="btn" style={{ display: "inline-block" }} href="/api/github/app/manifest">
            Connect GitHub App
          </a>
        )}
      </div>

      <div className="card">
        <strong>Personal access token (fallback)</strong>
        <p className="muted">Used only if no GitHub App is configured. Needs <code>repo</code> scope.</p>
        <div className="row">
          <input type="password" placeholder={data?.github_token_set ? "•••••••• (set)" : "ghp_…"} value={token} onChange={(e) => setToken(e.target.value)} />
          <button className="btn" disabled={!token || saveToken.isPending} onClick={() => saveToken.mutate()}>Save</button>
        </div>
      </div>

      <div className="card">
        <strong>Webhook</strong>
        <p className="muted">
          With a GitHub App the webhook is configured automatically. For PAT mode,
          add a webhook to your repo pointing at:
        </p>
        <pre className="logs" style={{ height: "auto" }}>{`${window.location.origin}/webhooks/github`}</pre>
        <div className="muted">Webhook secret: {data?.webhook_secret_set ? "set" : "auto-generated on first run"}</div>
      </div>
    </>
  );
}
