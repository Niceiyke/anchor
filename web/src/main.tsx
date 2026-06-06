import React from "react";
import ReactDOM from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  createRouter,
  createRoute,
  createRootRoute,
  RouterProvider,
  redirect,
  Outlet,
} from "@tanstack/react-router";
import { api } from "./api";
import { Layout } from "./components/Layout";
import { Login } from "./pages/Login";
import { Servers } from "./pages/Servers";
import { Apps } from "./pages/Apps";
import { AppDetail } from "./pages/AppDetail";
import { Terminal } from "./pages/Terminal";
import { Logs } from "./pages/Logs";
import { Settings } from "./pages/Settings";
import "./index.css";

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } },
});

async function ensureAuth() {
  try {
    await api.get("/api/me");
  } catch {
    throw redirect({ to: "/login" });
  }
}

const rootRoute = createRootRoute({ component: Outlet });

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/login",
  component: Login,
});

const appLayoutRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "app-layout",
  beforeLoad: ensureAuth,
  component: Layout,
});

const serversRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: "/",
  component: Servers,
});

const appsRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: "/apps",
  component: Apps,
});

const appDetailRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: "/apps/$appId",
  component: AppDetail,
});

const terminalRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: "/terminal",
  component: Terminal,
});

const logsRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: "/logs",
  component: Logs,
});

const settingsRoute = createRoute({
  getParentRoute: () => appLayoutRoute,
  path: "/settings",
  component: Settings,
});

const routeTree = rootRoute.addChildren([
  loginRoute,
  appLayoutRoute.addChildren([serversRoute, appsRoute, appDetailRoute, terminalRoute, logsRoute, settingsRoute]),
]);

const router = createRouter({ routeTree, context: { queryClient } });

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </React.StrictMode>
);
