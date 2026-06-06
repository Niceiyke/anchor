import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, type Server } from "../api";

interface Container {
  id: string;
  name: string;
  image: string;
  state: string;
  status: string;
}

interface Line {
  stream: string;
  line: string;
}

export function Logs() {
  const { data: servers = [] } = useQuery({ queryKey: ["servers"], queryFn: () => api.get<Server[]>("/api/servers") });
  const [serverId, setServerId] = useState("");

  useEffect(() => {
    if (!serverId && servers.length) setServerId(servers.find((s) => s.online)?.id ?? servers[0].id);
  }, [servers, serverId]);

  const { data: containers = [], isFetching, refetch, error } = useQuery({
    queryKey: ["containers", serverId],
    queryFn: () => api.get<Container[]>(`/api/servers/${serverId}/containers`),
    enabled: !!serverId,
    retry: false,
  });

  const [active, setActive] = useState<Container | null>(null);

  return (
    <>
      <div className="row">
        <h2>Container Logs</h2>
        <div className="row" style={{ gap: 8 }}>
          <select style={{ width: 200 }} value={serverId} onChange={(e) => { setServerId(e.target.value); setActive(null); }}>
            {servers.map((s) => <option key={s.id} value={s.id} disabled={!s.online}>{s.name}{s.online ? "" : " (offline)"}</option>)}
          </select>
          <button className="btn secondary" onClick={() => refetch()} disabled={isFetching}>↻</button>
        </div>
      </div>

      <div style={{ display: "grid", gridTemplateColumns: "320px 1fr", gap: 16, marginTop: 12 }}>
        <div className="card" style={{ padding: 8 }}>
          {error && <div className="error" style={{ padding: 8 }}>{(error as Error).message}</div>}
          {isFetching && <div className="muted" style={{ padding: 8 }}>Loading containers…</div>}
          {containers.map((c) => (
            <div
              key={c.id}
              onClick={() => setActive(c)}
              style={{ padding: 10, borderRadius: 6, cursor: "pointer", background: active?.id === c.id ? "#21262d" : "transparent" }}
            >
              <div className="row">
                <strong style={{ fontSize: 14 }}>{c.name}</strong>
                <span className={"dot " + (c.state === "running" ? "on" : "off")} />
              </div>
              <div className="muted">{c.image}</div>
              <div className="muted">{c.status}</div>
            </div>
          ))}
          {!isFetching && !error && containers.length === 0 && <div className="muted" style={{ padding: 8 }}>No containers.</div>}
        </div>

        <div>{active ? <LogStream key={active.id + serverId} serverId={serverId} container={active} /> : <div className="muted">Select a container to tail its logs.</div>}</div>
      </div>
    </>
  );
}

function LogStream({ serverId, container }: { serverId: string; container: Container }) {
  const [lines, setLines] = useState<Line[]>([]);
  const [live, setLive] = useState(false);
  const ridRef = useRef<string | null>(null);
  const esRef = useRef<EventSource | null>(null);
  const boxRef = useRef<HTMLDivElement>(null);

  function stop() {
    esRef.current?.close();
    esRef.current = null;
    if (ridRef.current) {
      api.del(`/api/servers/${serverId}/logs/${ridRef.current}`).catch(() => {});
      ridRef.current = null;
    }
    setLive(false);
  }

  useEffect(() => {
    setLines([]);
    let cancelled = false;

    (async () => {
      try {
        const { request_id } = await api.post<{ request_id: string }>(`/api/servers/${serverId}/logs`, { container: container.name, tail: 300 });
        if (cancelled) {
          api.del(`/api/servers/${serverId}/logs/${request_id}`).catch(() => {});
          return;
        }
        ridRef.current = request_id;
        const es = new EventSource(`/api/events?topic=exec:${request_id}`, { withCredentials: true });
        esRef.current = es;
        setLive(true);
        es.onmessage = (e) => {
          const evt = JSON.parse(e.data);
          if (evt.type === "log") {
            setLines((prev) => {
              const next = [...prev, { stream: evt.data.stream, line: evt.data.line }];
              return next.length > 5000 ? next.slice(next.length - 5000) : next;
            });
          } else if (evt.type === "command_result") {
            setLive(false);
            es.close();
          }
        };
        es.onerror = () => setLive(false);
      } catch (err) {
        setLines([{ stream: "stderr", line: (err as Error).message }]);
      }
    })();

    return () => {
      cancelled = true;
      stop();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [serverId, container.id]);

  useEffect(() => { boxRef.current?.scrollTo(0, boxRef.current.scrollHeight); }, [lines]);

  return (
    <>
      <div className="row" style={{ marginBottom: 8 }}>
        <div><strong>{container.name}</strong> <span className="muted">· {live ? <span><span className="dot on" />live</span> : "stopped"}</span></div>
        <div className="row" style={{ gap: 8 }}>
          {live && <button className="btn secondary" onClick={stop}>Pause</button>}
          <button className="btn secondary" onClick={() => setLines([])}>Clear</button>
        </div>
      </div>
      <div className="logs" ref={boxRef} style={{ height: 480 }}>
        {lines.map((l, i) => <div key={i} className={l.stream}>{l.line}</div>)}
        {lines.length === 0 && <div className="muted">Waiting for log output…</div>}
      </div>
    </>
  );
}
