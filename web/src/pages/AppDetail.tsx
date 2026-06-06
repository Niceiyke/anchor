import { useEffect, useRef, useState } from "react";
import { useParams } from "@tanstack/react-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, type App, type Deployment, type LogLine } from "../api";

export function AppDetail() {
  const { appId } = useParams({ from: "/app-layout/apps/$appId" });
  const qc = useQueryClient();
  const { data: app } = useQuery({ queryKey: ["app", appId], queryFn: () => api.get<App>(`/api/apps/${appId}`) });
  const { data: deployments = [] } = useQuery({
    queryKey: ["deployments", appId],
    queryFn: () => api.get<Deployment[]>(`/api/apps/${appId}/deployments`),
    refetchInterval: 4000,
  });

  const [selected, setSelected] = useState<string | null>(null);

  const deploy = useMutation({
    mutationFn: () => api.post<Deployment>(`/api/apps/${appId}/deploy`),
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
        <button className="btn" onClick={() => deploy.mutate()} disabled={deploy.isPending}>
          {deploy.isPending ? "Deploying…" : "Deploy now"}
        </button>
      </div>
      {app && (
        <div className="muted">{app.repo_full_name || app.repo_url} · {app.branch} · {app.domain || "no domain"} · port {app.container_port}</div>
      )}
      {deploy.isError && <div className="error">{(deploy.error as Error).message}</div>}

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

function DeploymentLogs({ deploymentId }: { deploymentId: string }) {
  const [lines, setLines] = useState<LogLine[]>([]);
  const boxRef = useRef<HTMLDivElement>(null);

  // Load historical logs, then subscribe to live updates via SSE.
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
