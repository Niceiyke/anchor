import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { api, type App, type Server, type Repo } from "../api";

export function Apps() {
  const qc = useQueryClient();
  const { data: apps = [] } = useQuery({ queryKey: ["apps"], queryFn: () => api.get<App[]>("/api/apps") });
  const { data: servers = [] } = useQuery({ queryKey: ["servers"], queryFn: () => api.get<Server[]>("/api/servers") });

  // Only fetch repos once GitHub is connected, so we don't hit /api/github/repos
  // (which 428s when unconfigured) on every load.
  const { data: gh } = useQuery({
    queryKey: ["settings"],
    queryFn: () => api.get<{ github_app_configured: boolean; github_token_set: boolean }>("/api/settings"),
  });
  const githubReady = !!(gh?.github_app_configured || gh?.github_token_set);
  const { data: repos = [] } = useQuery({
    queryKey: ["repos"],
    queryFn: () => api.get<Repo[]>("/api/github/repos"),
    enabled: githubReady,
    retry: false,
  });

  const [form, setForm] = useState({
    name: "", server_id: "", repo_full_name: "", repo_url: "", branch: "main",
    domain: "", container_port: 3000, auto_deploy: true, compose_file: "",
  });

  // Discover compose files in the selected repo/branch so the user can pick one
  // when there are several. Falls back to a free-text field if discovery can't
  // run (no GitHub, or a pasted clone URL with no owner/name).
  const { data: composeFiles = [] } = useQuery({
    queryKey: ["compose-files", form.repo_full_name, form.branch],
    queryFn: () => api.get<string[]>(`/api/github/compose-files?repo=${encodeURIComponent(form.repo_full_name)}&branch=${encodeURIComponent(form.branch)}`),
    enabled: githubReady && !!form.repo_full_name && !!form.branch,
    retry: false,
  });

  const create = useMutation({
    mutationFn: () => api.post<App>("/api/apps", { ...form, env_vars: {} }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["apps"] });
      setForm({ ...form, name: "", domain: "", compose_file: "" });
    },
  });

  const remove = useMutation({
    mutationFn: (id: string) => api.del(`/api/apps/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["apps"] }),
  });

  function pickRepo(fullName: string) {
    const r = repos.find((x) => x.full_name === fullName);
    setForm((f) => ({
      ...f,
      repo_full_name: fullName,
      repo_url: r?.clone_url ?? "",
      branch: r?.default_branch ?? "main",
      name: f.name || (fullName.split("/")[1] ?? ""),
      compose_file: "", // reset — a different repo has different files
    }));
  }

  return (
    <>
      <h2>Applications</h2>

      <div className="card">
        <strong>Deploy a new app</strong>
        <label>GitHub repository</label>
        {repos.length > 0 ? (
          <select value={form.repo_full_name} onChange={(e) => pickRepo(e.target.value)}>
            <option value="">Select a repo…</option>
            {repos.map((r) => <option key={r.full_name} value={r.full_name}>{r.full_name}{r.private ? " 🔒" : ""}</option>)}
          </select>
        ) : (
          <div className="muted">{githubReady ? "No repos found for this GitHub account." : "Connect GitHub in Settings to pick a repo — or paste a clone URL below."}</div>
        )}

        <div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 12 }}>
          <div>
            <label>App name</label>
            <input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} />
          </div>
          <div>
            <label>Target server</label>
            <select value={form.server_id} onChange={(e) => setForm({ ...form, server_id: e.target.value })}>
              <option value="">Select…</option>
              {servers.map((s) => <option key={s.id} value={s.id}>{s.name}{s.online ? "" : " (offline)"}</option>)}
            </select>
          </div>
          <div>
            <label>Clone URL</label>
            <input value={form.repo_url} onChange={(e) => setForm({ ...form, repo_url: e.target.value })} placeholder="https://github.com/owner/repo.git" />
          </div>
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
        </div>

        <label style={{ marginTop: 12 }}>Compose file <span className="muted">(optional — leave on auto-detect for a single compose/Dockerfile)</span></label>
        {composeFiles.length > 0 ? (
          <select value={form.compose_file} onChange={(e) => setForm({ ...form, compose_file: e.target.value })}>
            <option value="">Auto-detect</option>
            {composeFiles.map((f) => <option key={f} value={f}>{f}</option>)}
          </select>
        ) : (
          <input
            value={form.compose_file}
            onChange={(e) => setForm({ ...form, compose_file: e.target.value })}
            placeholder="auto-detect — or e.g. docker-compose.prod.yml"
          />
        )}

        <label style={{ display: "flex", alignItems: "center", gap: 8, marginTop: 12 }}>
          <input type="checkbox" style={{ width: "auto" }} checked={form.auto_deploy} onChange={(e) => setForm({ ...form, auto_deploy: e.target.checked })} />
          Auto-deploy on push
        </label>
        <button className="btn" style={{ marginTop: 12 }} disabled={!form.name || !form.server_id || !form.repo_url || create.isPending} onClick={() => create.mutate()}>
          Create app
        </button>
        {create.isError && <div className="error">{(create.error as Error).message}</div>}
      </div>

      <table className="card" style={{ display: "table" }}>
        <thead><tr><th>Name</th><th>Repo</th><th>Branch</th><th>Domain</th><th>Auto</th><th></th></tr></thead>
        <tbody>
          {apps.map((a) => (
            <tr key={a.id}>
              <td><Link to="/apps/$appId" params={{ appId: a.id }}>{a.name}</Link></td>
              <td className="muted">{a.repo_full_name || a.repo_url}</td>
              <td>{a.branch}</td>
              <td className="muted">{a.domain ? <a href={`https://${a.domain}`} target="_blank" rel="noreferrer">{a.domain} ↗</a> : "—"}</td>
              <td>{a.auto_deploy ? "✓" : "—"}</td>
              <td style={{ textAlign: "right" }}>
                <button
                  className="btn secondary"
                  style={{ padding: "2px 8px", fontSize: 12 }}
                  disabled={remove.isPending}
                  onClick={() => { if (confirm(`Delete app "${a.name}"? Its container(s) will be stopped and removed.`)) remove.mutate(a.id); }}
                >
                  Delete
                </button>
              </td>
            </tr>
          ))}
          {apps.length === 0 && <tr><td colSpan={6} className="muted">No apps yet.</td></tr>}
        </tbody>
      </table>
    </>
  );
}
