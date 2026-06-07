import { useEffect, useRef, useState } from "react";
import { useParams } from "@tanstack/react-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, type App, type Deployment, type LogLine, type Database } from "../api";

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

  const deploy = useMutation({
    mutationFn: () => api.post<Deployment>(`/api/apps/${appId}/deploy`),
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

  return (
    <>
      <div className="row">
        <h2>{app?.name ?? "App"}</h2>
        <div className="row" style={{ gap: 6 }}>
          {!serverOnline && (
            <span className="badge failed" style={{ fontSize: 12 }}>agent offline — deploy/stop unavailable</span>
          )}
          <button className="btn secondary" onClick={() => stop.mutate()} disabled={stop.isPending || !serverOnline}>
            {stop.isPending ? "Stopping…" : "Stop"}
          </button>
          <button className="btn" onClick={() => deploy.mutate()} disabled={deploy.isPending || !serverOnline}>
            {deploy.isPending ? "Deploying…" : "Deploy"}
          </button>
          {app?.last_good_sha && (
            <button className="btn secondary" onClick={() => rollback.mutate()} disabled={rollback.isPending || !serverOnline}>
              {rollback.isPending ? "Rolling back…" : "Rollback"}
            </button>
          )}
        </div>
      </div>
      {app && (
        <div className="muted" style={{ marginBottom: 8 }}>
          {app.repo_full_name || app.repo_url} · {app.branch} · {app.domain || "no domain"} · port {app.container_port}
          {app.compose_file && <span> · compose: <code>{app.compose_file}</code></span>}
          {app.last_good_sha && <span> · rollback: <code>{app.last_good_sha.slice(0, 7)}</code></span>}
        </div>
      )}
      {appErr && <div className="error" style={{ marginBottom: 8 }}>Failed to load app: {(appErr as Error).message}</div>}
      {deploy.isError && <div className="error">{(deploy.error as Error).message}</div>}
      {rollback.isError && <div className="error">{(rollback.error as Error).message}</div>}
      {stop.isError && <div className="error">{(stop.error as Error).message}</div>}

      {app && <EnvSection app={app} />}

      <h3 style={{ marginBottom: 0 }}>Deployments</h3>
      <div style={{ display: "grid", gridTemplateColumns: "280px 1fr", gap: 16, marginTop: 16 }}>
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
              <div className="muted">{d.commit_sha ? d.commit_sha.slice(0, 7) : "manual"}</div>
            </div>
          ))}
          {deployments.length === 0 && <div className="muted" style={{ padding: 10 }}>No deployments yet.</div>}
        </div>

        <div>{activeId ? <DeploymentLogs deploymentId={activeId} /> : <div className="muted">Select a deployment.</div>}</div>
      </div>
    </>
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

  const removeVar = useMutation({
    mutationFn: (key: string) => api.del(`/api/apps/${app.id}/env/${encodeURIComponent(key)}`),
    onSuccess: refresh,
  });

  const entries = Object.entries(app.env_vars || {});

  return (
    <div className="card">
      <strong>Environment</strong>

      <div style={{ marginTop: 10 }}>
        {entries.length === 0 && <div className="muted">No environment variables yet.</div>}
        {entries.map(([k, v]) => (
          <div className="row" key={k} style={{ padding: "4px 0", fontFamily: "ui-monospace, monospace", fontSize: 13 }}>
            <span><b>{k}</b>=<span className="muted">{reveal ? v : maskValue(v)}</span></span>
            <button className="btn secondary" onClick={() => removeVar.mutate(k)}>Remove</button>
          </div>
        ))}
        {entries.length > 0 && (
          <button className="btn secondary" style={{ marginTop: 6 }} onClick={() => setReveal((r) => !r)}>{reveal ? "Hide values" : "Show values"}</button>
        )}
      </div>

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
