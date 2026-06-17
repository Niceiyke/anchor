// Thin fetch wrapper. The session token lives in a cookie (set by the API on
// login), so requests just need credentials: "include".

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

export function csrfToken(): string | null {
  const m = document.cookie.match(/(?:^|;\s*)anchor_csrf=([^;]*)/);
  return m ? m[1] : null;
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {};
  const csrf = csrfToken();
  if (csrf && method !== "GET" && method !== "HEAD") {
    headers["X-CSRF-Token"] = csrf;
  }
  if (body) {
    headers["Content-Type"] = "application/json";
  }
  const res = await fetch(path, {
    method,
    credentials: "include",
    headers,
    body: body ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    const text = await res.text();
    throw new ApiError(res.status, text || res.statusText);
  }
  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
}

export const api = {
  get: <T>(p: string) => request<T>("GET", p),
  post: <T>(p: string, b?: unknown) => request<T>("POST", p, b),
  put: <T>(p: string, b?: unknown) => request<T>("PUT", p, b),
  patch: <T>(p: string, b?: unknown) => request<T>("PATCH", p, b),
  del: <T>(p: string) => request<T>("DELETE", p),
};

// ---- Types (mirror the Go store models) ----

export interface Stats {
  cpu_percent: number;
  mem_used: number;
  mem_total: number;
  disk_used: number;
  disk_total: number;
  containers: number;
}

export interface Server {
  id: string;
  name: string;
  agent_token?: string;
  public_ip?: string;
  online: boolean;
  last_seen: string;
  stats?: Stats;
  created_at: string;
}

export interface Route {
  domain: string;
  service?: string;
  port?: number;
  health_path?: string;
}

export interface App {
  id: string;
  name: string;
  server_id: string;
  repo_full_name: string;
  repo_url: string;
  branch: string;
  domain: string;
  container_port: number;
  auto_deploy: boolean;
  env_vars: Record<string, string>;
  env_secret?: Record<string, boolean>;
  container_name?: string;
  compose_file?: string;
  service?: string;
  routes?: Route[];
  last_good_sha?: string;
  health_path?: string;
  health_timeout_secs?: number;
  auto_rollback?: boolean;
  created_at: string;
}

export interface LogLine {
  stream: string;
  line: string;
  at: string;
}

export interface Deployment {
  id: string;
  app_id: string;
  commit_sha: string;
  branch: string;
  phase: string;
  stack_type: string;
  message: string;
  logs?: LogLine[];
  created_at: string;
  updated_at: string;
}

export interface Repo {
  full_name: string;
  clone_url: string;
  default_branch: string;
  private: boolean;
}

export interface Database {
  id: string;
  name: string;
  server_id: string;
  engine: string;
  version: string;
  status: string;
  message: string;
  container: string;
  host: string;
  port: number;
  host_port: number;
  username: string;
  password: string;
  db_name: string;
  conn_uri: string;
  created_at: string;
}
