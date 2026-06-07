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
  const [ghError, setGhError] = useState("");
  const [connecting, setConnecting] = useState(false);

  // Fetch the app manifest over the authenticated XHR path, then submit a form
  // to GitHub to start the create-app flow.
  async function connectGitHubApp() {
    setGhError("");
    setConnecting(true);
    try {
      const { action, manifest } = await api.get<{ action: string; manifest: string }>("/api/github/app/manifest");
      const form = document.createElement("form");
      form.method = "POST";
      form.action = action;
      const input = document.createElement("input");
      input.type = "hidden";
      input.name = "manifest";
      input.value = manifest;
      form.appendChild(input);
      document.body.appendChild(form);
      form.submit();
    } catch (e) {
      setGhError((e as Error).message || "Failed to start GitHub App setup");
      setConnecting(false);
    }
  }

  const saveToken = useMutation({
    mutationFn: () => api.put("/api/settings", { github_token: token }),
    onSuccess: () => {
      setToken("");
      qc.invalidateQueries({ queryKey: ["settings"] });
      qc.invalidateQueries({ queryKey: ["repos"] });
    },
  });

  const [pw, setPw] = useState({ current: "", next: "", confirm: "" });
  const [pwMsg, setPwMsg] = useState("");
  const changePw = useMutation({
    mutationFn: () => api.post("/api/account/password", { current_password: pw.current, new_password: pw.next }),
    onSuccess: () => {
      setPw({ current: "", next: "", confirm: "" });
      setPwMsg("Password updated.");
    },
    onError: (e) => setPwMsg((e as Error).message),
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
            <button className="btn secondary" style={{ marginTop: 12 }} disabled={connecting} onClick={connectGitHubApp}>
              {connecting ? "Opening GitHub…" : "Re-create app"}
            </button>
          </div>
        ) : (
          <button className="btn" disabled={connecting} onClick={connectGitHubApp}>
            {connecting ? "Opening GitHub…" : "Connect GitHub App"}
          </button>
        )}
        {ghError && <div className="error">{ghError}</div>}
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

      <div className="card">
        <strong>Change admin password</strong>
        <p className="muted">Signed in as <b>{data?.admin_user}</b>. Min 8 characters.</p>
        <label>Current password</label>
        <input type="password" value={pw.current} onChange={(e) => setPw({ ...pw, current: e.target.value })} />
        <label>New password</label>
        <input type="password" value={pw.next} onChange={(e) => setPw({ ...pw, next: e.target.value })} />
        <label>Confirm new password</label>
        <input type="password" value={pw.confirm} onChange={(e) => setPw({ ...pw, confirm: e.target.value })} />
        <button
          className="btn"
          style={{ marginTop: 12 }}
          disabled={!pw.current || pw.next.length < 8 || pw.next !== pw.confirm || changePw.isPending}
          onClick={() => { setPwMsg(""); changePw.mutate(); }}
        >
          {changePw.isPending ? "Updating…" : "Update password"}
        </button>
        {pw.next && pw.confirm && pw.next !== pw.confirm && <div className="error">Passwords don't match.</div>}
        {pwMsg && <div className={changePw.isError ? "error" : "muted"} style={changePw.isSuccess ? { color: "var(--green)" } : undefined}>{pwMsg}</div>}
      </div>
    </>
  );
}
