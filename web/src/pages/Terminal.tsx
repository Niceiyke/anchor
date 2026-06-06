import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, type Server } from "../api";

interface Line {
  stream: string;
  line: string;
}

export function Terminal() {
  const { data: servers = [] } = useQuery({ queryKey: ["servers"], queryFn: () => api.get<Server[]>("/api/servers") });
  const [serverId, setServerId] = useState("");
  const [command, setCommand] = useState("");
  const [lines, setLines] = useState<Line[]>([]);
  const [running, setRunning] = useState(false);
  const [history, setHistory] = useState<string[]>([]);
  const esRef = useRef<EventSource | null>(null);
  const boxRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!serverId && servers.length) setServerId(servers[0].id);
  }, [servers, serverId]);

  useEffect(() => () => esRef.current?.close(), []);
  useEffect(() => { boxRef.current?.scrollTo(0, boxRef.current.scrollHeight); }, [lines]);

  function append(l: Line) {
    setLines((prev) => [...prev, l]);
  }

  async function run() {
    if (!command.trim() || !serverId || running) return;
    const cmd = command;
    append({ stream: "system", line: `$ ${cmd}` });
    setHistory((h) => [cmd, ...h].slice(0, 50));
    setCommand("");
    setRunning(true);
    try {
      const { request_id } = await api.post<{ request_id: string }>(`/api/servers/${serverId}/exec`, { command: cmd });
      const es = new EventSource(`/api/events?topic=exec:${request_id}`, { withCredentials: true });
      esRef.current = es;
      es.onmessage = (e) => {
        const evt = JSON.parse(e.data);
        if (evt.type === "log") {
          append({ stream: evt.data.stream, line: evt.data.line });
        } else if (evt.type === "command_result") {
          append({ stream: "system", line: `[exit ${evt.data.exit_code}]` });
          es.close();
          setRunning(false);
        }
      };
      es.onerror = () => { es.close(); setRunning(false); };
    } catch (err) {
      append({ stream: "stderr", line: (err as Error).message });
      setRunning(false);
    }
  }

  return (
    <>
      <div className="row">
        <h2>Terminal</h2>
        <select style={{ width: 220 }} value={serverId} onChange={(e) => setServerId(e.target.value)}>
          {servers.map((s) => <option key={s.id} value={s.id} disabled={!s.online}>{s.name}{s.online ? "" : " (offline)"}</option>)}
        </select>
      </div>

      <div className="logs" ref={boxRef} style={{ height: 460 }}>
        {lines.map((l, i) => <div key={i} className={l.stream}>{l.line}</div>)}
        {lines.length === 0 && <div className="muted">Run a command on the selected server. Output streams live.</div>}
      </div>

      <div className="row" style={{ marginTop: 12 }}>
        <input
          placeholder="docker ps -a"
          value={command}
          onChange={(e) => setCommand(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") run();
            if (e.key === "ArrowUp" && history.length) setCommand(history[0]);
          }}
          style={{ fontFamily: "ui-monospace, monospace" }}
        />
        <button className="btn" onClick={run} disabled={running || !serverId}>{running ? "Running…" : "Run"}</button>
        <button className="btn secondary" onClick={() => setLines([])}>Clear</button>
      </div>
      <div className="muted" style={{ marginTop: 6 }}>Commands run as the agent user via <code>sh -c</code>. Output is live-only (not persisted).</div>
    </>
  );
}
