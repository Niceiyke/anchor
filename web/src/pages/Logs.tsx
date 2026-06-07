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
  const [pruning, setPruning] = useState(false);
  const [pruneMsg, setPruneMsg] = useState("");
  const exitedCount = containers.filter((c) => c.state !== "running").length;

  async function runPrune(kind: "containers" | "images" | "system", confirmText: string) {
    if (!confirm(confirmText)) return;
    setPruneMsg("");
    setPruning(true);
    try {
      const res = await api.post<{ output: string }>(`/api/servers/${serverId}/${kind}/prune`);
      setPruneMsg(res.output?.split("\n").filter(Boolean).pop() || "Pruned.");
      if (kind === "containers" || kind === "system") {
        if (active && active.state !== "running") setActive(null);
        refetch();
      }
    } catch (e) {
      setPruneMsg((e as Error).message || "Prune failed");
    } finally {
      setPruning(false);
    }
  }

  return (
    <>
      <div className="row">
        <h2>Containers</h2>
        <div className="row" style={{ gap: 8 }}>
          <select style={{ width: 200 }} value={serverId} onChange={(e) => { setServerId(e.target.value); setActive(null); setPruneMsg(""); }}>
            {servers.map((s) => <option key={s.id} value={s.id} disabled={!s.online}>{s.name}{s.online ? "" : " (offline)"}</option>)}
          </select>
          <button
            className="btn secondary"
            onClick={() => runPrune("containers", "Remove all stopped containers on this server?")}
            disabled={pruning || !serverId || exitedCount === 0}
            title="Remove all stopped containers"
          >
            {pruning ? "Pruning…" : `Prune exited${exitedCount ? ` (${exitedCount})` : ""}`}
          </button>
          <button
            className="btn secondary"
            onClick={() => runPrune("images", "Remove dangling (unused) images on this server?")}
            disabled={pruning || !serverId}
            title="Remove dangling images to reclaim disk space"
          >
            {pruning ? "Pruning…" : "Prune images"}
          </button>
          <button
            className="btn secondary"
            onClick={() => runPrune("system", "Run system prune? Removes stopped containers, unused networks, dangling images and build cache. Volumes (database data) are NOT touched.")}
            disabled={pruning || !serverId}
            title="docker system prune — containers, networks, dangling images, build cache (never volumes)"
          >
            {pruning ? "Pruning…" : "System prune"}
          </button>
          <button className="btn secondary" onClick={() => refetch()} disabled={isFetching}>↻</button>
        </div>
      </div>
      {pruneMsg && <div className="muted" style={{ marginTop: 4 }}>{pruneMsg}</div>}

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
              <ContainerActions serverId={serverId} container={c} onDone={() => refetch()} />
            </div>
          ))}
          {!isFetching && !error && containers.length === 0 && <div className="muted" style={{ padding: 8 }}>No containers.</div>}
        </div>

        <div>{active ? <LogStream key={active.id + serverId} serverId={serverId} container={active} /> : <div className="muted">Select a container to tail its logs.</div>}</div>
      </div>
    </>
  );
}

function ContainerActions({ serverId, container, onDone }: { serverId: string; container: Container; onDone: () => void }) {
  const [busy, setBusy] = useState("");
  const [err, setErr] = useState("");
  const running = container.state === "running";

  async function run(action: string, e: React.MouseEvent) {
    e.stopPropagation();
    if ((action === "stop" || action === "remove") &&
        !confirm(`${action === "remove" ? "Remove" : "Stop"} container "${container.name}"?`)) {
      return;
    }
    setErr("");
    setBusy(action);
    try {
      await api.post(`/api/servers/${serverId}/containers/${encodeURIComponent(container.name)}/${action}`);
      onDone();
    } catch (e) {
      setErr((e as Error).message || "action failed");
    } finally {
      setBusy("");
    }
  }

  const Btn = ({ action, label, danger }: { action: string; label: string; danger?: boolean }) => (
    <button
      className={"btn " + (danger ? "danger" : "secondary")}
      style={{ padding: "2px 8px", fontSize: 12 }}
      disabled={!!busy}
      onClick={(e) => run(action, e)}
    >
      {busy === action ? "…" : label}
    </button>
  );

  return (
    <div onClick={(e) => e.stopPropagation()}>
      <div className="row" style={{ gap: 6, marginTop: 8, flexWrap: "wrap" }}>
        {running
          ? <><Btn action="restart" label="Restart" /><Btn action="stop" label="Stop" /></>
          : <Btn action="start" label="Start" />}
        <Btn action="remove" label="Remove" danger />
      </div>
      {err && <div className="error" style={{ fontSize: 12, marginTop: 4 }}>{err}</div>}
    </div>
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
    let started = false;

    // Subscribe to the SSE topic FIRST, then ask the agent to start streaming
    // (on `onopen`) — otherwise the tail backlog races ahead of the subscriber
    // and is lost. We generate the request_id so both ends agree on the topic.
    const rid = "log_" + (crypto.randomUUID?.() ?? Math.random().toString(16).slice(2)).replace(/-/g, "");
    ridRef.current = rid;
    const es = new EventSource(`/api/events?topic=exec:${rid}`, { withCredentials: true });
    esRef.current = es;

    es.onopen = () => {
      if (cancelled || started) return;
      started = true;
      setLive(true);
      api.post(`/api/servers/${serverId}/logs`, { container: container.name, tail: 300, request_id: rid })
        .catch((err) => setLines([{ stream: "stderr", line: (err as Error).message }]));
    };
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
