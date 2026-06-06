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
  const { data: servers = [] } = useQuery({
    queryKey: ["servers"],
    queryFn: () => api.get<Server[]>("/api/servers"),
    refetchInterval: 5000,
  });
  const [name, setName] = useState("");
  const [created, setCreated] = useState<Server | null>(null);

  const add = useMutation({
    mutationFn: () => api.post<Server>("/api/servers", { name }),
    onSuccess: (s) => {
      setCreated(s);
      setName("");
      qc.invalidateQueries({ queryKey: ["servers"] });
    },
  });

  const cpURL = window.location.origin;

  return (
    <>
      <div className="row">
        <h2>Servers</h2>
      </div>

      <div className="card">
        <div className="row">
          <input placeholder="New server name (e.g. prod-eu-1)" value={name} onChange={(e) => setName(e.target.value)} />
          <button className="btn" onClick={() => add.mutate()} disabled={!name || add.isPending}>Add server</button>
        </div>
        {created && (
          <div style={{ marginTop: 16 }}>
            <div className="muted">Run this on <b>{created.name}</b> to connect its agent (token shown once):</div>
            <pre className="logs" style={{ height: "auto" }}>{`ANCHOR_URL=${cpURL} \\
ANCHOR_TOKEN=${created.agent_token} \\
./anchor-agent`}</pre>
          </div>
        )}
      </div>

      <div className="grid">
        {servers.map((s) => (
          <div className="card" key={s.id}>
            <div className="row">
              <strong>{s.name}</strong>
              <span className="muted"><span className={"dot " + (s.online ? "on" : "off")} />{s.online ? "online" : "offline"}</span>
            </div>
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
        {servers.length === 0 && <div className="muted">No servers yet. Add one above.</div>}
      </div>
    </>
  );
}
