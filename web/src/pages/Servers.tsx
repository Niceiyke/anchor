import { useState } from "react";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, type Server } from "../api";

function pct(used = 0, total = 0) {
  return total > 0 ? Math.round((used / total) * 100) : 0;
}
function gb(n = 0) {
  return (n / 1024 ** 3).toFixed(1) + " GB";
}

export function Servers() {
  const qc = useQueryClient();
  const { data: servers = [], error } = useQuery({
    queryKey: ["servers"],
    queryFn: () => api.get<Server[]>("/api/servers"),
    refetchInterval: 5000,
  });
  const [name, setName] = useState("");
  const [ip, setIp] = useState("");
  const [created, setCreated] = useState<Server | null>(null);

  const add = useMutation({
    mutationFn: () => api.post<Server>("/api/servers", { name, public_ip: ip.trim() }),
    onSuccess: (s) => {
      setCreated(s);
      setName(""); setIp("");
      qc.invalidateQueries({ queryKey: ["servers"] });
    },
  });

  const cpURL = window.location.origin;

  return (
    <>
      <div className="row">
        <h2>Servers</h2>
        {error && <span className="badge failed">API unreachable — check connection</span>}
      </div>

      <div className="card">
        <div className="row" style={{ gap: 8 }}>
          <input style={{ flex: 2 }} placeholder="New server name (e.g. prod-eu-1)" value={name} onChange={(e) => setName(e.target.value)} />
          <input style={{ flex: 1 }} placeholder="Public IP (optional, for DNS)" value={ip} onChange={(e) => setIp(e.target.value)} />
          <button className="btn" onClick={() => add.mutate()} disabled={!name || add.isPending}>Add server</button>
        </div>
        {add.isError && <div className="error">{(add.error as Error).message}</div>}
        {created && <InstallCommand server={created} cpURL={cpURL} />}
      </div>

      <div className="grid">
        {servers.map((s) => (
          <div className="card" key={s.id}>
            <div className="row">
              <strong>{s.name}</strong>
              <span className="muted"><span className={"dot " + (s.online ? "on" : "off")} />{s.online ? "online" : "offline"}</span>
            </div>
            <ServerIP server={s} onSaved={() => qc.invalidateQueries({ queryKey: ["servers"] })} />
            {!s.online && (
              <div className="muted" style={{ fontSize: 12, color: "var(--yellow)", marginTop: 8 }}>
                Agent disconnected. Deploy, terminal, and database operations are unavailable for this server.
              </div>
            )}
            {s.stats ? (
              <div style={{ marginTop: 10 }}>
                <div className="muted">CPU {Math.round(s.stats.cpu_percent)}%</div>
                <div className="bar"><div style={{ width: `${Math.min(100, s.stats.cpu_percent)}%` }} /></div>
                <div className="muted" style={{ marginTop: 8 }}>Memory {pct(s.stats.mem_used, s.stats.mem_total)}% · {gb(s.stats.mem_used)} / {gb(s.stats.mem_total)}</div>
                <div className="bar"><div style={{ width: `${pct(s.stats.mem_used, s.stats.mem_total)}%` }} /></div>
                <div className="muted" style={{ marginTop: 8 }}>Disk {pct(s.stats.disk_used, s.stats.disk_total)}% · {gb(s.stats.disk_used)} / {gb(s.stats.disk_total)}</div>
                <div className="bar"><div style={{ width: `${pct(s.stats.disk_used, s.stats.disk_total)}%` }} /></div>
                <div className="muted" style={{ marginTop: 8 }}>{s.stats.containers} containers</div>
              </div>
            ) : (
              <div className="muted" style={{ marginTop: 10 }}>No stats yet</div>
            )}
          </div>
        ))}
        {!error && servers.length === 0 && <div className="muted">No servers yet. Add one above.</div>}
      </div>
    </>
  );
}

function InstallCommand({ server, cpURL }: { server: Server; cpURL: string }) {
  const [copied, setCopied] = useState(false);
  const cmd = `curl -fsSL ${cpURL}/install.sh | sudo bash -s -- --token=${server.agent_token}`;
  const copy = () => {
    navigator.clipboard.writeText(cmd);
    setCopied(true);
    setTimeout(() => setCopied(false), 1500);
  };
  return (
    <div style={{ marginTop: 16 }}>
      <div className="row">
        <div className="muted">Run this on <b>{server.name}</b> to install and connect its agent (token shown once):</div>
        <button className="btn secondary" style={{ padding: "2px 10px", fontSize: 12 }} onClick={copy}>{copied ? "Copied" : "Copy"}</button>
      </div>
      <pre className="logs" style={{ height: "auto", whiteSpace: "pre-wrap", wordBreak: "break-all" }}>{cmd}</pre>
      <div className="muted" style={{ fontSize: 12 }}>
        Installs Docker (if missing), the version-matched agent binary, a per-VPS Caddy router, and a systemd service. No inbound ports needed.
      </div>
    </div>
  );
}

function ServerIP({ server, onSaved }: { server: Server; onSaved: () => void }) {
  const [editing, setEditing] = useState(false);
  const [ip, setIp] = useState(server.public_ip ?? "");
  const save = useMutation({
    mutationFn: () => api.patch(`/api/servers/${server.id}`, { public_ip: ip.trim() }),
    onSuccess: () => { setEditing(false); onSaved(); },
  });
  if (!editing) {
    return (
      <div className="muted" style={{ fontSize: 12, marginTop: 6 }}>
        IP: {server.public_ip ? <code>{server.public_ip}</code> : <span>not set</span>}{" "}
        <a href="#" onClick={(e) => { e.preventDefault(); setIp(server.public_ip ?? ""); setEditing(true); }}>edit</a>
      </div>
    );
  }
  return (
    <div className="row" style={{ gap: 6, marginTop: 6 }}>
      <input style={{ fontSize: 12, padding: "2px 6px" }} placeholder="203.0.113.10" value={ip} onChange={(e) => setIp(e.target.value)} />
      <button className="btn secondary" style={{ padding: "2px 8px", fontSize: 12 }} disabled={save.isPending} onClick={() => save.mutate()}>Save</button>
      <button className="btn secondary" style={{ padding: "2px 8px", fontSize: 12 }} onClick={() => setEditing(false)}>Cancel</button>
    </div>
  );
}
