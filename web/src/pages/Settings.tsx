import { useEffect, useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";

interface SettingsStatus {
  admin_user: string;
  github_token_set: boolean;
  webhook_secret_set: boolean;
  github_app_configured: boolean;
  github_app_installed: boolean;
  github_app_slug: string;
  notification_webhook_set: boolean;
  base_domain: string;
  cloudflare_configured: boolean;
  public_ip: string;
}

export function Settings() {
  const qc = useQueryClient();
  const { data } = useQuery({ queryKey: ["settings"], queryFn: () => api.get<SettingsStatus>("/api/settings") });
  const [token, setToken] = useState("");
  const [ghError, setGhError] = useState("");
  const [connecting, setConnecting] = useState(false);

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
    onSuccess: () => { setToken(""); qc.invalidateQueries({ queryKey: ["settings"] }); qc.invalidateQueries({ queryKey: ["repos"] }); },
  });

  const [pw, setPw] = useState({ current: "", next: "", confirm: "" });
  const [pwMsg, setPwMsg] = useState("");
  const changePw = useMutation({
    mutationFn: () => api.post("/api/account/password", { current_password: pw.current, new_password: pw.next }),
    onSuccess: () => { setPw({ current: "", next: "", confirm: "" }); setPwMsg("Password updated."); },
    onError: (e) => setPwMsg((e as Error).message),
  });

  return (
    <>
      <h2>Settings</h2>

      <div className="card">
        <strong>Base domain</strong>
        <p className="muted">
          Apps created without a custom domain get <code>&lt;slug&gt;.&lt;base&gt;</code> automatically,
          with HTTPS issued on demand. Requires a wildcard DNS record:{" "}
          <code>*.{data?.base_domain || "apps.example.com"}</code> → this server's IP.
        </p>
        <DomainSection current={data?.base_domain ?? ""} onSaved={() => qc.invalidateQueries({ queryKey: ["settings"] })} />
      </div>

      <div className="card">
        <strong>Cloudflare DNS</strong>
        <p className="muted">
          Optional. With a Cloudflare API token, Anchor creates/removes the DNS
          A records for app domains automatically — no manual wildcard record
          needed. Use a token scoped to <code>Zone:DNS:Edit</code> + <code>Zone:Read</code> for your zone.
        </p>
        <CloudflareSection configured={data?.cloudflare_configured ?? false} publicIP={data?.public_ip ?? ""} onSaved={() => qc.invalidateQueries({ queryKey: ["settings"] })} />
      </div>

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
        <strong>Notifications</strong>
        <p className="muted">Receive deploy status alerts via Slack or Discord webhook.</p>
        <NotificationSection configured={data?.notification_webhook_set ?? false} onSaved={() => qc.invalidateQueries({ queryKey: ["settings"] })} />
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

      <UserSection />
    </>
  );
}

function CloudflareSection({ configured, publicIP, onSaved }: { configured: boolean; publicIP: string; onSaved: () => void }) {
  const [token, setToken] = useState("");
  const [ip, setIp] = useState(publicIP);
  const [msg, setMsg] = useState("");
  useEffect(() => { setIp(publicIP); }, [publicIP]);

  const save = useMutation({
    mutationFn: () => api.put("/api/settings", { ...(token ? { cloudflare_api_token: token } : {}), public_ip: ip.trim() }),
    onSuccess: () => { setToken(""); setMsg("Saved."); onSaved(); },
    onError: (e) => setMsg((e as Error).message),
  });
  const clear = useMutation({
    mutationFn: () => api.put("/api/settings", { cloudflare_api_token: "" }),
    onSuccess: () => { setMsg("Removed."); onSaved(); },
  });
  const verify = useMutation({
    mutationFn: () => api.post<{ zone: string; zone_id: string; public_ip: string }>("/api/cloudflare/verify"),
    onSuccess: (r) => setMsg(`✓ Token works — zone ${r.zone}${r.public_ip ? `, records → ${r.public_ip}` : ""}.`),
    onError: (e) => setMsg((e as Error).message),
  });

  return (
    <div style={{ marginTop: 8 }}>
      <div className="row">
        <input type="password" placeholder={configured ? "•••••••• (set — paste to replace)" : "Cloudflare API token"} value={token} onChange={(e) => setToken(e.target.value)} />
      </div>
      <div className="row" style={{ marginTop: 8 }}>
        <input type="text" placeholder="Public IP (blank = auto-detect)" value={ip} onChange={(e) => setIp(e.target.value)} />
        <button className="btn" disabled={save.isPending || (!token && ip.trim() === publicIP)} onClick={() => { setMsg(""); save.mutate(); }}>Save</button>
        {configured && <button className="btn secondary" disabled={verify.isPending} onClick={() => { setMsg(""); verify.mutate(); }}>{verify.isPending ? "Verifying…" : "Verify"}</button>}
        {configured && <button className="btn secondary" onClick={() => { setMsg(""); clear.mutate(); }}>Remove</button>}
      </div>
      {configured && <div className="muted" style={{ marginTop: 4 }}><span className="dot on" /> Token configured</div>}
      {msg && <div className={save.isError || verify.isError || clear.isError ? "error" : "muted"} style={save.isSuccess || verify.isSuccess ? { color: "var(--green)", marginTop: 4 } : { marginTop: 4 }}>{msg}</div>}
    </div>
  );
}

function DomainSection({ current, onSaved }: { current: string; onSaved: () => void }) {
  const [val, setVal] = useState(current);
  const [msg, setMsg] = useState("");
  // sync the input once the loaded value arrives (only while untouched)
  useEffect(() => { setVal(current); }, [current]);
  const save = useMutation({
    mutationFn: (v: string) => api.put("/api/settings", { base_domain: v.trim() }),
    onSuccess: () => { setMsg("Saved."); onSaved(); },
    onError: (e) => setMsg((e as Error).message),
  });
  return (
    <div style={{ marginTop: 8 }}>
      <div className="row">
        <input type="text" placeholder="apps.example.com" value={val} onChange={(e) => setVal(e.target.value)} />
        <button className="btn" disabled={save.isPending} onClick={() => { setMsg(""); save.mutate(val); }}>Save</button>
        {current && <button className="btn secondary" onClick={() => { setMsg(""); setVal(""); save.mutate(""); }}>Disable</button>}
      </div>
      {current && <div className="muted" style={{ marginTop: 4 }}><span className="dot on" /> Active: <code>{current}</code></div>}
      {msg && <div className={save.isError ? "error" : "muted"} style={save.isSuccess ? { color: "var(--green)", marginTop: 4 } : { marginTop: 4 }}>{msg}</div>}
    </div>
  );
}

function NotificationSection({ configured, onSaved }: { configured: boolean; onSaved: () => void }) {
  const [url, setUrl] = useState("");
  const [msg, setMsg] = useState("");
  const save = useMutation({
    mutationFn: () => api.put("/api/settings", { notification_webhook: url }),
    onSuccess: () => { setUrl(""); setMsg("Saved."); onSaved(); },
    onError: (e) => setMsg((e as Error).message),
  });
  const clear = useMutation({
    mutationFn: () => api.put("/api/settings", { notification_webhook: "" }),
    onSuccess: () => { setMsg("Removed."); onSaved(); },
  });

  return (
    <div style={{ marginTop: 8 }}>
      <div className="row">
        <input type="text" placeholder="https://hooks.slack.com/…" value={url} onChange={(e) => setUrl(e.target.value)} />
        <button className="btn" disabled={!url || save.isPending} onClick={() => { setMsg(""); save.mutate(); }}>Save</button>
        {configured && <button className="btn secondary" onClick={() => { setMsg(""); clear.mutate(); }}>Remove</button>}
      </div>
      {configured && !url && <div className="muted" style={{ marginTop: 4 }}><span className="dot on" /> Webhook configured</div>}
      {msg && <div className={save.isError || clear.isError ? "error" : "muted"} style={save.isSuccess ? { color: "var(--green)" } : { marginTop: 4 }}>{msg}</div>}
    </div>
  );
}

interface User {
  id: string;
  username: string;
  role: string;
  created_at: string;
}

function UserSection() {
  const qc = useQueryClient();
  const { data: users = [] } = useQuery({ queryKey: ["users"], queryFn: () => api.get<User[]>("/api/users") });
  const [form, setForm] = useState({ username: "", password: "", role: "viewer" });

  const create = useMutation({
    mutationFn: () => api.post<User>("/api/users", form),
    onSuccess: () => { qc.invalidateQueries({ queryKey: ["users"] }); setForm({ username: "", password: "", role: "viewer" }); },
  });

  const remove = useMutation({
    mutationFn: (id: string) => api.del(`/api/users/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["users"] }),
  });

  return (
    <div className="card">
      <strong>Users</strong>
      <p className="muted">Manage who can access Anchor. Admins have full access; viewers are read-only.</p>

      <div className="row" style={{ marginTop: 10, gap: 8 }}>
        <input placeholder="Username" value={form.username} onChange={(e) => setForm({ ...form, username: e.target.value })} style={{ flex: 2 }} />
        <input type="password" placeholder="Password" value={form.password} onChange={(e) => setForm({ ...form, password: e.target.value })} style={{ flex: 2 }} />
        <select value={form.role} onChange={(e) => setForm({ ...form, role: e.target.value })} style={{ flex: 1 }}>
          <option value="viewer">Viewer</option>
          <option value="admin">Admin</option>
        </select>
        <button className="btn" disabled={!form.username || !form.password || create.isPending} onClick={() => create.mutate()}>
          {create.isPending ? "Adding…" : "Add"}
        </button>
      </div>
      {create.isError && <div className="error">{(create.error as Error).message}</div>}

      <div style={{ marginTop: 14 }}>
        {users.map((u) => (
          <div className="row" key={u.id} style={{ padding: "6px 0", borderBottom: "1px solid var(--border)" }}>
            <span><b>{u.username}</b> <span className="badge" style={{ marginLeft: 6 }}>{u.role}</span></span>
            <span className="muted" style={{ fontSize: 12 }}>{new Date(u.created_at).toLocaleDateString()}</span>
            <button className="btn secondary" onClick={() => { if (confirm(`Delete user "${u.username}"?`)) remove.mutate(u.id); }}>Remove</button>
          </div>
        ))}
        {users.length === 0 && <div className="muted">No additional users. The bootstrap admin is always available.</div>}
      </div>
    </div>
  );
}
