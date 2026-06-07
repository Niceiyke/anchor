import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, csrfToken, type Database, type Server } from "../api";

const ENGINES = [
  { id: "postgres", label: "PostgreSQL", icon: "🐘" },
  { id: "redis", label: "Redis", icon: "🟥" },
];

function statusClass(s: string) {
  if (s === "running") return "success";
  if (s === "failed" || s === "unreachable") return "failed";
  return "building";
}

export function Databases() {
  const qc = useQueryClient();
  const { data: dbs = [] } = useQuery({
    queryKey: ["databases"],
    queryFn: () => api.get<Database[]>("/api/databases"),
    refetchInterval: 4000,
  });
  const { data: servers = [] } = useQuery({ queryKey: ["servers"], queryFn: () => api.get<Server[]>("/api/servers") });

  const [form, setForm] = useState({ name: "", server_id: "", engine: "postgres", host_port: 0 });

  const create = useMutation({
    mutationFn: () => api.post<Database>("/api/databases", form),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["databases"] });
      setForm({ ...form, name: "" });
    },
  });

  const remove = useMutation({
    mutationFn: (id: string) => api.del(`/api/databases/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["databases"] }),
  });

  return (
    <>
      <h2>Managed Databases</h2>

      <div className="card">
        <strong>Provision a database</strong>
        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr 1fr 1fr", gap: 12, marginTop: 8 }}>
          <div>
            <label>Name</label>
            <input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="app-db" />
          </div>
          <div>
            <label>Engine</label>
            <select value={form.engine} onChange={(e) => setForm({ ...form, engine: e.target.value })}>
              {ENGINES.map((e) => <option key={e.id} value={e.id}>{e.icon} {e.label}</option>)}
            </select>
          </div>
          <div>
            <label>Server</label>
            <select value={form.server_id} onChange={(e) => setForm({ ...form, server_id: e.target.value })}>
              <option value="">Select…</option>
              {servers.map((s) => <option key={s.id} value={s.id} disabled={!s.online}>{s.name}{s.online ? "" : " (offline)"}</option>)}
            </select>
          </div>
          <div>
            <label>Expose host port (optional)</label>
            <input type="number" value={form.host_port || ""} onChange={(e) => setForm({ ...form, host_port: +e.target.value })} placeholder="internal only" />
          </div>
        </div>
        <button className="btn" style={{ marginTop: 12 }} disabled={!form.name || !form.server_id || create.isPending} onClick={() => create.mutate()}>
          {create.isPending ? "Provisioning…" : "Provision"}
        </button>
        {create.isError && <div className="error">{(create.error as Error).message}</div>}
        <div className="muted" style={{ marginTop: 8 }}>
          Apps on the same server reach the database by its container name over the <code>anchor_net</code> network — no host port needed.
        </div>
      </div>

      <div className="grid">
        {dbs.map((db) => (
          <DatabaseCard key={db.id} db={db} onDelete={() => {
            if (confirm(`Delete "${db.name}" and its data volume? This cannot be undone.`)) remove.mutate(db.id);
          }} />
        ))}
        {dbs.length === 0 && <div className="muted">No databases yet.</div>}
      </div>
    </>
  );
}

function DatabaseCard({ db, onDelete }: { db: Database; onDelete: () => void }) {
  const [show, setShow] = useState(false);
  const [backingUp, setBackingUp] = useState(false);
  const engine = ENGINES.find((e) => e.id === db.engine);

  async function backup() {
    if (!confirm(`Backup "${db.name}"? The dump file will be downloaded.`)) return;
    setBackingUp(true);
    try {
      const csrf = csrfToken();
      const res = await fetch(`/api/databases/${db.id}/backup`, {
        method: "POST",
        credentials: "include",
        headers: csrf ? { "X-CSRF-Token": csrf } : undefined,
      });
      if (!res.ok) throw new Error(await res.text());
      const blob = await res.blob();
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `backup-${db.name}-${new Date().toISOString().slice(0, 10)}.sql`;
      a.click();
      URL.revokeObjectURL(url);
    } catch (e) {
      alert((e as Error).message);
    } finally {
      setBackingUp(false);
    }
  }

  return (
    <div className="card">
      <div className="row">
        <strong>{engine?.icon} {db.name}</strong>
        <span className={"badge " + statusClass(db.status)}>{db.status}</span>
      </div>
      <div className="muted" style={{ marginTop: 4 }}>{db.engine}:{db.version}{db.host_port ? ` · :${db.host_port}` : " · internal"}</div>
      {db.message && db.status !== "running" && <div className="muted" style={{ marginTop: 4 }}>{db.message}</div>}

      <label style={{ marginTop: 12 }}>Connection string</label>
      <div className="row" style={{ gap: 6 }}>
        <input readOnly value={show ? db.conn_uri : db.conn_uri.replace(/:[^:@/]+@/, ":••••••@")} style={{ fontFamily: "ui-monospace, monospace", fontSize: 12 }} />
        <button className="btn secondary" onClick={() => setShow((v) => !v)}>{show ? "Hide" : "Show"}</button>
        <button className="btn secondary" onClick={() => navigator.clipboard.writeText(db.conn_uri)}>Copy</button>
      </div>

      <details style={{ marginTop: 10 }}>
        <summary className="muted" style={{ cursor: "pointer" }}>Details</summary>
        <table style={{ marginTop: 6 }}>
          <tbody>
            <tr><td className="muted">Host</td><td>{db.host}</td></tr>
            <tr><td className="muted">Port</td><td>{db.port}{db.host_port ? ` (host ${db.host_port})` : ""}</td></tr>
            {db.engine === "postgres" && <>
              <tr><td className="muted">User</td><td>{db.username}</td></tr>
              <tr><td className="muted">Database</td><td>{db.db_name}</td></tr>
            </>}
            <tr><td className="muted">Password</td><td>{show ? db.password : "••••••••"}</td></tr>
          </tbody>
        </table>
      </details>

      <div className="row" style={{ marginTop: 12, gap: 8 }}>
        <button className="btn secondary" onClick={backup} disabled={backingUp || db.status !== "running"}>
          {backingUp ? "Backing up…" : "Backup"}
        </button>
        <button className="btn danger" onClick={onDelete}>Delete</button>
      </div>
    </div>
  );
}
