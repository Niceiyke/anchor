import { useEffect, useRef, useState } from "react";
import { useParams, useNavigate } from "@tanstack/react-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, type App, type Deployment, type LogLine, type Database } from "../api";
import { RunningBadge } from "../components/RunningBadge";

export function AppDetail() {
  const { appId } = useParams({ from: "/app-layout/apps/$appId" });
  const qc = useQueryClient();
  const { data: app, error: appErr } = useQuery({ queryKey: ["app", appId], queryFn: () => api.get<App>(`/api/apps/${appId}`) });
  const { data: deployments = [] } = useQuery({
    queryKey: ["deployments", appId],
    queryFn: () => api.get<Deployment[]>(`/api/apps/${appId}/deployments`),
    refetchInterval: 4000,
  });
  const { data: servers = [] } = useQuery({ queryKey: ["servers"], queryFn: () => api.get<any[]>("/api/servers") });
  const serverOnline = servers.find((s) => s.id === app?.server_id)?.online ?? false;

  const [selected, setSelected] = useState<string | null>(null);

  // deploy accepts an optional commit (redeploy a specific past commit);
  // undefined deploys the latest of the branch.
  const deploy = useMutation({
    mutationFn: (commit?: string) => api.post<Deployment>(`/api/apps/${appId}/deploy`, commit ? { commit_sha: commit } : undefined),
    onSuccess: (d) => {
      setSelected(d.id);
      qc.invalidateQueries({ queryKey: ["deployments", appId] });
    },
  });

  const stop = useMutation({
    mutationFn: () => api.post(`/api/apps/${appId}/stop`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["app", appId] }),
  });

  const rollback = useMutation({
    mutationFn: () => api.post<Deployment>(`/api/apps/${appId}/rollback`),
    onSuccess: (d) => {
      setSelected(d.id);
      qc.invalidateQueries({ queryKey: ["deployments", appId] });
    },
  });

  const activeId = selected ?? deployments[0]?.id ?? null;
  const busy = deploy.isPending || rollback.isPending;

  return (
    <>
      <div className="row">
        <div className="row" style={{ gap: 10 }}>
          <h2 style={{ marginBottom: 0 }}>{app?.name ?? "App"}</h2>
          {app && <RunningBadge serverId={app.server_id} containerName={app.container_name} online={serverOnline} />}
        </div>
        <div className="row" style={{ gap: 6 }}>
          {!serverOnline && (
            <span className="badge failed" style={{ fontSize: 12 }}>agent offline — actions unavailable</span>
          )}
          <button className="btn secondary" onClick={() => stop.mutate()} disabled={stop.isPending || !serverOnline}>
            {stop.isPending ? "Stopping…" : "Stop"}
          </button>
          {app?.last_good_sha && (
            <button className="btn secondary" onClick={() => rollback.mutate()} disabled={busy || !serverOnline}>
              {rollback.isPending ? "Rolling back…" : "Rollback"}
            </button>
          )}
          <button className="btn" onClick={() => deploy.mutate(undefined)} disabled={busy || !serverOnline}>
            {deploy.isPending ? "Deploying…" : "Deploy latest"}
          </button>
        </div>
      </div>
      {app && (
        <div className="muted" style={{ marginBottom: 8 }}>
          {app.repo_full_name || app.repo_url} · {app.branch} ·{" "}
          {app.domain
            ? <a href={`https://${app.domain}`} target="_blank" rel="noreferrer">{app.domain} ↗</a>
            : "no domain"} · port {app.container_port}
          {app.compose_file && <span> · compose: <code>{app.compose_file}</code></span>}
          {app.last_good_sha && <span> · rollback: <code>{app.last_good_sha.slice(0, 7)}</code></span>}
        </div>
      )}
      {appErr && <div className="error" style={{ marginBottom: 8 }}>Failed to load app: {(appErr as Error).message}</div>}
      {deploy.isError && <div className="error">{(deploy.error as Error).message}</div>}
      {rollback.isError && <div className="error">{(rollback.error as Error).message}</div>}
      {stop.isError && <div className="error">{(stop.error as Error).message}</div>}

      {app && <AppSettings app={app} />}

      {app && <EnvSection app={app} />}

      <h3 style={{ marginBottom: 0 }}>Deployments</h3>
      <div style={{ display: "grid", gridTemplateColumns: "300px 1fr", gap: 16, marginTop: 16 }}>
        <div className="card" style={{ padding: 8 }}>
          {deployments.map((d) => (
            <div
              key={d.id}
              onClick={() => setSelected(d.id)}
              style={{ padding: 10, borderRadius: 6, cursor: "pointer", background: d.id === activeId ? "#21262d" : "transparent" }}
            >
              <div className="row">
                <span className={"badge " + d.phase}>{d.phase}</span>
                <span className="muted">{d.stack_type || ""}</span>
              </div>
              <div className="muted" style={{ marginTop: 4 }}>{new Date(d.created_at).toLocaleString()}</div>
              <div className="row">
                <span className="muted">{d.commit_sha ? d.commit_sha.slice(0, 7) : "manual"}</span>
                <button
                  className="btn secondary"
                  style={{ padding: "2px 8px", fontSize: 12 }}
                  disabled={busy || !serverOnline}
                  title="Redeploy this commit"
                  onClick={(e) => { e.stopPropagation(); deploy.mutate(d.commit_sha || undefined); }}
                >
                  Redeploy
                </button>
              </div>
            </div>
          ))}
          {deployments.length === 0 && <div className="muted" style={{ padding: 10 }}>No deployments yet.</div>}
        </div>

        <div>{activeId ? <DeploymentLogs deploymentId={activeId} /> : <div className="muted">Select a deployment.</div>}</div>
      </div>

      {app && <DangerZone app={app} />}
    </>
  );
}

function DangerZone({ app }: { app: App }) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const del = useMutation({
    mutationFn: () => api.del(`/api/apps/${app.id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["apps"] });
      navigate({ to: "/apps" });
    },
  });
  return (
    <div className="card" style={{ marginTop: 24, borderColor: "var(--red, #f85149)" }}>
      <strong>Danger zone</strong>
      <div className="row" style={{ marginTop: 8 }}>
        <span className="muted">Delete this app and stop/remove its container(s) on the server. This cannot be undone.</span>
        <button
          className="btn danger"
          disabled={del.isPending}
          onClick={() => { if (confirm(`Delete app "${app.name}"? Its container(s) will be stopped and removed.`)) del.mutate(); }}
        >
          {del.isPending ? "Deleting…" : "Delete app"}
        </button>
      </div>
      {del.isError && <div className="error" style={{ marginTop: 8 }}>{(del.error as Error).message}</div>}
    </div>
  );
}

function AppSettings({ app }: { app: App }) {
  const qc = useQueryClient();
  const [form, setForm] = useState({
    branch: app.branch,
    domain: app.domain,
    container_port: app.container_port,
    auto_deploy: app.auto_deploy,
    compose_file: app.compose_file ?? "",
    health_path: app.health_path ?? "",
    health_timeout_secs: app.health_timeout_secs ?? 0,
    auto_rollback: app.auto_rollback ?? false,
  });
  const [msg, setMsg] = useState("");

  const dirty =
    form.branch !== app.branch ||
    form.domain !== app.domain ||
    form.container_port !== app.container_port ||
    form.auto_deploy !== app.auto_deploy ||
    (form.compose_file ?? "") !== (app.compose_file ?? "") ||
    (form.health_path ?? "") !== (app.health_path ?? "") ||
    form.health_timeout_secs !== (app.health_timeout_secs ?? 0) ||
    form.auto_rollback !== (app.auto_rollback ?? false);

  const save = useMutation({
    mutationFn: () => api.patch<App>(`/api/apps/${app.id}`, form),
    onSuccess: () => {
      setMsg("Saved. Redeploy to apply.");
      qc.invalidateQueries({ queryKey: ["app", app.id] });
      qc.invalidateQueries({ queryKey: ["apps"] });
    },
    onError: (e) => setMsg((e as Error).message),
  });

  return (
    <details className="card">
      <summary style={{ cursor: "pointer", fontWeight: 600 }}>Configuration</summary>
      <p className="muted" style={{ marginTop: 8 }}>
        Server and repository are fixed. Changes here apply on the next deploy.
      </p>
      <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 12 }}>
        <div>
          <label>Branch</label>
          <input value={form.branch} onChange={(e) => setForm({ ...form, branch: e.target.value })} />
        </div>
        <div>
          <label>Domain</label>
          <input value={form.domain} onChange={(e) => setForm({ ...form, domain: e.target.value })} placeholder="app.example.com" />
        </div>
        <div>
          <label>Container port</label>
          <input type="number" value={form.container_port} onChange={(e) => setForm({ ...form, container_port: +e.target.value })} />
        </div>
        <div>
          <label>Compose file</label>
          <input value={form.compose_file} onChange={(e) => setForm({ ...form, compose_file: e.target.value })} placeholder="auto-detect — or e.g. docker-compose.prod.yml" />
        </div>
        <div>
          <label>Health check path</label>
          <input value={form.health_path} onChange={(e) => setForm({ ...form, health_path: e.target.value })} placeholder="optional — e.g. /healthz" />
        </div>
        <div>
          <label>Health timeout (seconds)</label>
          <input type="number" min={0} max={600} value={form.health_timeout_secs} onChange={(e) => setForm({ ...form, health_timeout_secs: +e.target.value })} placeholder="0 = default (45s)" />
        </div>
      </div>
      <label style={{ display: "flex", alignItems: "center", gap: 8, marginTop: 12 }}>
        <input type="checkbox" style={{ width: "auto" }} checked={form.auto_deploy} onChange={(e) => setForm({ ...form, auto_deploy: e.target.checked })} />
        Auto-deploy on push
      </label>
      <label style={{ display: "flex", alignItems: "center", gap: 8, marginTop: 8 }}>
        <input type="checkbox" style={{ width: "auto" }} checked={form.auto_rollback} onChange={(e) => setForm({ ...form, auto_rollback: e.target.checked })} />
        Auto-rollback to last good deploy if a deploy fails its health check
      </label>
      <button className="btn" style={{ marginTop: 12 }} disabled={!dirty || save.isPending} onClick={() => { setMsg(""); save.mutate(); }}>
        {save.isPending ? "Saving…" : "Save changes"}
      </button>
      {msg && <div className={save.isError ? "error" : "muted"} style={save.isSuccess ? { color: "var(--green)", marginTop: 8 } : { marginTop: 8 }}>{msg}</div>}
    </details>
  );
}

function EnvSection({ app }: { app: App }) {
  const qc = useQueryClient();
  const { data: databases = [] } = useQuery({ queryKey: ["databases"], queryFn: () => api.get<Database[]>("/api/databases") });

  const sameServerDBs = databases.filter((d) => d.server_id === app.server_id);
  const [dbId, setDbId] = useState("");
  const [varName, setVarName] = useState("");
  const [notice, setNotice] = useState("");
  const [reveal, setReveal] = useState(false);

  // add/edit a single variable
  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  const [secret, setSecret] = useState(true);
  const [editing, setEditing] = useState(false);

  // a var is secret unless explicitly marked plain (false) — safe default
  const isSecret = (k: string) => app.env_secret?.[k] !== false;

  // bulk .env import
  const [importText, setImportText] = useState("");
  const [importSecret, setImportSecret] = useState(true);
  const [dragOver, setDragOver] = useState(false);
  const importEnv = useMutation({
    mutationFn: () => api.post<{ imported: number }>(`/api/apps/${app.id}/env/import`, { content: importText, secret: importSecret }),
    onSuccess: (res) => { setNotice(`Imported ${res.imported} variable(s). Redeploy to apply.`); setImportText(""); refresh(); },
  });
  function onDropFile(e: React.DragEvent) {
    e.preventDefault(); setDragOver(false);
    const file = e.dataTransfer.files?.[0];
    if (file) file.text().then((t) => setImportText((prev) => (prev ? prev + "\n" : "") + t));
  }

  const selectedDb = sameServerDBs.find((d) => d.id === dbId);
  const suggestedVar = selectedDb ? (selectedDb.engine === "redis" ? "REDIS_URL" : "DATABASE_URL") : "";

  const refresh = () => qc.invalidateQueries({ queryKey: ["app", app.id] });

  const attach = useMutation({
    mutationFn: () => api.post<{ attached_var: string; same_server: boolean }>(`/api/apps/${app.id}/attach-db`, { database_id: dbId, var_name: varName || suggestedVar }),
    onSuccess: (res) => {
      setNotice(`Attached as ${res.attached_var}. Redeploy to apply.`);
      setDbId(""); setVarName("");
      refresh();
    },
  });

  const setVar = useMutation({
    mutationFn: (v: { key: string; value: string; secret: boolean }) =>
      api.post(`/api/apps/${app.id}/env`, { key: v.key.trim(), value: v.value, secret: v.secret }),
    onSuccess: (_d, v) => {
      setNotice(`Saved ${v.key.trim()}. Redeploy to apply.`);
      setKey(""); setValue(""); setSecret(true); setEditing(false);
      refresh();
    },
  });

  const removeVar = useMutation({
    mutationFn: (k: string) => api.del(`/api/apps/${app.id}/env/${encodeURIComponent(k)}`),
    onSuccess: refresh,
  });

  function startEdit(k: string, v: string) {
    setKey(k); setValue(v); setSecret(isSecret(k)); setEditing(true); setNotice("");
  }

  // flip a var between secret (masked) and plain without changing its value
  function toggleSecret(k: string) {
    setVar.mutate({ key: k, value: app.env_vars[k], secret: !isSecret(k) });
  }

  const entries = Object.entries(app.env_vars || {});

  return (
    <div className="card">
      <strong>Environment &amp; secrets</strong>
      <p className="muted" style={{ marginTop: 4 }}>Injected into the container on deploy. 🔒 secrets are masked; 🔓 plain values are shown. Toggle per variable.</p>

      <div style={{ marginTop: 10 }}>
        {entries.length === 0 && <div className="muted">No environment variables yet.</div>}
        {entries.map(([k, v]) => {
          const sec = isSecret(k);
          const shown = !sec || reveal;
          return (
            <div className="row" key={k} style={{ padding: "4px 0", fontFamily: "ui-monospace, monospace", fontSize: 13 }}>
              <span style={{ overflow: "hidden", textOverflow: "ellipsis" }}>
                <span title={sec ? "secret" : "plain"} style={{ marginRight: 4 }}>{sec ? "🔒" : "🔓"}</span>
                <b>{k}</b>=<span className="muted">{shown ? v : maskValue(v)}</span>
              </span>
              <span className="row" style={{ gap: 6 }}>
                <button className="btn secondary" style={{ padding: "2px 8px" }} title={sec ? "Mark as plain (show value)" : "Mark as secret (mask value)"} onClick={() => toggleSecret(k)}>
                  {sec ? "Unmask" : "Mask"}
                </button>
                <button className="btn secondary" style={{ padding: "2px 8px" }} onClick={() => startEdit(k, v)}>Edit</button>
                <button className="btn secondary" style={{ padding: "2px 8px" }} onClick={() => { if (confirm(`Remove ${k}?`)) removeVar.mutate(k); }}>Remove</button>
              </span>
            </div>
          );
        })}
        {entries.some(([k]) => isSecret(k)) && (
          <button className="btn secondary" style={{ marginTop: 6 }} onClick={() => setReveal((r) => !r)}>{reveal ? "Hide secrets" : "Reveal secrets"}</button>
        )}
      </div>

      <div style={{ borderTop: "1px solid var(--border)", marginTop: 14, paddingTop: 14 }}>
        <label>{editing ? "Edit variable" : "Add a variable"}</label>
        <div className="row" style={{ gap: 8, flexWrap: "wrap" }}>
          <input
            style={{ flex: 1, fontFamily: "ui-monospace, monospace" }}
            placeholder="KEY"
            value={key}
            disabled={editing}
            onChange={(e) => setKey(e.target.value.replace(/[^A-Za-z0-9_]/g, "_"))}
          />
          <input
            style={{ flex: 2, fontFamily: "ui-monospace, monospace" }}
            placeholder="value"
            value={value}
            onChange={(e) => setValue(e.target.value)}
          />
          <button className="btn" disabled={!key.trim() || setVar.isPending} onClick={() => { setNotice(""); setVar.mutate({ key, value, secret }); }}>
            {setVar.isPending ? "Saving…" : editing ? "Save" : "Add"}
          </button>
          {editing && <button className="btn secondary" onClick={() => { setEditing(false); setKey(""); setValue(""); setSecret(true); }}>Cancel</button>}
        </div>
        <label style={{ display: "flex", alignItems: "center", gap: 8, marginTop: 8 }}>
          <input type="checkbox" style={{ width: "auto" }} checked={secret} onChange={(e) => setSecret(e.target.checked)} />
          Secret (mask value in the UI)
        </label>
        {setVar.isError && <div className="error" style={{ marginTop: 6 }}>{(setVar.error as Error).message}</div>}
      </div>

      <details style={{ borderTop: "1px solid var(--border)", marginTop: 14, paddingTop: 14 }}>
        <summary style={{ cursor: "pointer" }}>Bulk import from .env</summary>
        <p className="muted" style={{ marginTop: 8 }}>Paste a <code>.env</code> block or drop a file. Comments and blank lines are ignored; existing keys are overwritten.</p>
        <textarea
          value={importText}
          onChange={(e) => setImportText(e.target.value)}
          onDragOver={(e) => { e.preventDefault(); setDragOver(true); }}
          onDragLeave={() => setDragOver(false)}
          onDrop={onDropFile}
          placeholder={"# paste or drop a .env file\nDATABASE_URL=postgres://…\nJWT_SECRET=…"}
          spellCheck={false}
          style={{
            width: "100%", minHeight: 120, fontFamily: "ui-monospace, monospace", fontSize: 13,
            padding: 10, borderRadius: 6,
            border: "1px dashed " + (dragOver ? "var(--green)" : "var(--border)"),
            background: dragOver ? "rgba(63,185,80,0.06)" : undefined,
          }}
        />
        <div className="row" style={{ marginTop: 8 }}>
          <label style={{ display: "flex", alignItems: "center", gap: 8 }}>
            <input type="checkbox" style={{ width: "auto" }} checked={importSecret} onChange={(e) => setImportSecret(e.target.checked)} />
            Import as secrets
          </label>
          <button className="btn" disabled={!importText.trim() || importEnv.isPending} onClick={() => { setNotice(""); importEnv.mutate(); }}>
            {importEnv.isPending ? "Importing…" : "Import variables"}
          </button>
        </div>
        {importEnv.isError && <div className="error" style={{ marginTop: 6 }}>{(importEnv.error as Error).message}</div>}
      </details>

      <div style={{ borderTop: "1px solid var(--border)", marginTop: 14, paddingTop: 14 }}>
        <label>Attach a database</label>
        {sameServerDBs.length === 0 ? (
          <div className="muted">No databases on this app's server. Create one on the Databases page (same server) to attach it.</div>
        ) : (
          <div className="row" style={{ gap: 8, flexWrap: "wrap" }}>
            <select style={{ flex: 2 }} value={dbId} onChange={(e) => { setDbId(e.target.value); setNotice(""); }}>
              <option value="">Select a database…</option>
              {sameServerDBs.map((d) => <option key={d.id} value={d.id}>{d.name} ({d.engine})</option>)}
            </select>
            <input style={{ flex: 1 }} placeholder={suggestedVar || "VAR_NAME"} value={varName} onChange={(e) => setVarName(e.target.value)} />
            <button className="btn" disabled={!dbId || attach.isPending} onClick={() => attach.mutate()}>
              {attach.isPending ? "Attaching…" : "Attach"}
            </button>
          </div>
        )}
        {notice && <div className="muted" style={{ marginTop: 8, color: "var(--green)" }}>{notice}</div>}
        {attach.isError && <div className="error">{(attach.error as Error).message}</div>}
      </div>
    </div>
  );
}

function maskValue(v: string) {
  if (v.includes("://")) return v.replace(/:[^:@/]+@/, ":••••••@");
  if (v.length <= 6) return "••••";
  return v.slice(0, 3) + "••••" + v.slice(-2);
}

function DeploymentLogs({ deploymentId }: { deploymentId: string }) {
  const [lines, setLines] = useState<LogLine[]>([]);
  const boxRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    setLines([]);
    let closed = false;
    api.get<Deployment>(`/api/deployments/${deploymentId}`).then((d) => {
      if (!closed) setLines(d.logs ?? []);
    });

    const es = new EventSource(`/api/events?topic=deployment:${deploymentId}`, { withCredentials: true });
    es.onmessage = (e) => {
      const evt = JSON.parse(e.data);
      if (evt.type === "log") {
        setLines((prev) => [...prev, { stream: evt.data.stream, line: evt.data.line, at: evt.timestamp }]);
      }
    };
    return () => {
      closed = true;
      es.close();
    };
  }, [deploymentId]);

  useEffect(() => {
    boxRef.current?.scrollTo(0, boxRef.current.scrollHeight);
  }, [lines]);

  return (
    <div className="logs" ref={boxRef}>
      {lines.map((l, i) => <div key={i} className={l.stream}>{l.line}</div>)}
      {lines.length === 0 && <div className="muted">Waiting for logs…</div>}
    </div>
  );
}
