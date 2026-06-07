import { Link, Outlet, useNavigate } from "@tanstack/react-router";
import { api } from "../api";

export function Layout() {
  const navigate = useNavigate();
  async function logout() {
    await api.post("/api/logout").catch(() => {});
    navigate({ to: "/login" });
  }
  return (
    <div className="layout">
      <aside className="sidebar">
        <h1>⚓ Anchor</h1>
        <nav>
          <Link to="/" activeOptions={{ exact: true }}>Servers</Link>
          <Link to="/apps">Applications</Link>
          <Link to="/databases">Databases</Link>
          <Link to="/logs">Containers</Link>
          <Link to="/terminal">Terminal</Link>
          <Link to="/settings">Settings</Link>
        </nav>
        <div style={{ marginTop: 24 }}>
          <button className="btn secondary" onClick={logout}>Log out</button>
        </div>
      </aside>
      <main className="main">
        <Outlet />
      </main>
    </div>
  );
}
