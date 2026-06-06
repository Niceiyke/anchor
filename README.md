# ⚓ Anchor

A self-hosted control plane for managing your VPSes: deploy apps from GitHub,
auto-deploy on push, run commands, view logs, and watch system status — built
with **Go**, **React + TanStack**, and **Caddy**.

Think a personal, hackable [Coolify](https://coolify.io)/[Dokploy](https://dokploy.com).

## Architecture

```
                         ┌─────────────────────────────────────┐
   Browser  ──TanStack──▶│  Control Plane (Go)                  │
                         │  • REST API + single-user auth       │
   GitHub  ──webhook────▶│  • Agent hub (command dispatch)      │
                         │  • Live deploy logs (SSE)            │
                         │  • JSON store (→ SQLite later)       │
                         └───────────────▲─────────────────────┘
                                         │  agent dials OUT (NAT-friendly)
                         ┌───────────────┴───────────────┐
                         │            │                  │
                  ┌──────┴─────┐ ┌────┴──────┐    ┌──────┴─────┐
                  │   VPS 1    │ │   VPS 2   │ …  │   VPS N    │
                  │  agent     │ │  agent    │    │  agent     │
                  │  docker    │ │  docker   │    │  docker    │
                  │  caddy     │ │  caddy    │    │  caddy     │
                  └────────────┘ └───────────┘    └────────────┘
```

**Agent connection model:** each VPS runs a small Go agent that *dials out* to
the control plane — a long-lived `GET /agent/v1/stream` to receive commands and
`POST /agent/v1/events` to report logs/status/stats. No inbound ports needed on
the VPS; works behind NAT/firewalls. Pure Go stdlib, no WebSocket dependency.

**Builds run on the target VPS:** the agent does `git clone` → detect stack →
`docker build`/`docker compose up` → write a Caddy route. No registry required.

**Stack detection:** `docker-compose.yml` / `compose.yaml` (preferred, for
multi-service Go + Caddy + DB stacks) → otherwise a root `Dockerfile`.

## Repository layout

```
cmd/control-plane    control plane entrypoint
cmd/agent            VPS agent entrypoint
internal/control     API, agent hub, auth, github, deploy orchestration, SSE
internal/agent       command loop, deploy pipeline, caddy, system stats
internal/store       persistence (JSON file behind a Store interface)
pkg/protocol         shared command/event message types
web/                 React + TanStack Router + Query dashboard
deploy/              Caddyfiles for control plane + per-VPS app router
scripts/             install-agent.sh
```

## Quickstart (local dev)

**1. Run the control plane + web UI**

```bash
make cp                 # build the API
ANCHOR_ADMIN_USER=admin ANCHOR_ADMIN_PASS=secret ./bin/anchor-cp   # :8080
# in another terminal, the hot-reloading UI (proxies /api to :8080):
cd web && npm install && npm run dev          # http://localhost:5173
```

Log in with the admin creds you set.

**2. Add a server** (Servers → Add server). Copy the generated `anchor-agent`
command. Run a local agent to test the handshake:

```bash
make agent
ANCHOR_URL=http://localhost:8080 ANCHOR_TOKEN=<token> \
  ANCHOR_WORKDIR=/tmp/anchor-apps ./bin/anchor-agent
```

The server flips to **online** and live CPU/mem/disk/container stats appear.

## Production deploy

**Control plane** (run on any host/VPS — needs a public domain for HTTPS + the
GitHub webhook):

```bash
cp .env.example .env        # set admin creds + ANCHOR_DOMAIN
docker compose up -d --build
```

This starts the control plane behind Caddy (automatic HTTPS).

**Each VPS you want to deploy to:**

```bash
make agent-linux            # cross-compile -> bin/anchor-agent-linux
scp bin/anchor-agent-linux  user@vps:/tmp/anchor-agent
scp scripts/install-agent.sh deploy/agent-caddy/Caddyfile user@vps:/tmp/
# on the VPS:
sudo ANCHOR_URL=https://anchor.example.com ANCHOR_TOKEN=<token from dashboard> \
  ANCHOR_AGENT_BIN=/tmp/anchor-agent /tmp/install-agent.sh
```

The installer sets up the `anchor_net` Docker network, a Caddy container that
routes app domains, and a `systemd` service for the agent.

## Deploy flow

1. **Settings → GitHub token** — paste a PAT with `repo` scope (MVP; a GitHub
   App can replace this later).
2. **Applications → Deploy a new app** — pick a repo, target server, branch,
   domain, container port, and toggle auto-deploy.
3. **Deploy now**, or push to the branch. Watch logs stream live.

**Auto-deploy on push:** add a GitHub webhook → `https://anchor.example.com/webhooks/github`,
content type `application/json`, secret = the value shown in Settings, event =
`push`. On push, every app bound to that repo+branch with auto-deploy redeploys.

## Routing & HTTPS

Each VPS runs Caddy on `anchor_net`. On deploy the agent writes
`<domain> { reverse_proxy <app>:<port> }` to `/etc/anchor/caddy/apps/<app>.caddy`
and reloads Caddy, which provisions TLS automatically. Point the domain's DNS at
the VPS and it's live.

> For `docker-compose` apps, attach the public-facing service to the external
> `anchor_net` network (and name it to match the app) so Caddy can reach it.

## Security notes (MVP → harden before public exposure)

- Admin password is hashed with SHA-256 — **swap for bcrypt/argon2**.
- GitHub access uses a stored PAT — migrate to a **GitHub App** with
  short-lived installation tokens.
- Sessions are in-memory; agent tokens are bearer strings in the JSON store.
- The JSON store is single-node; move to **SQLite/Postgres** behind the existing
  `store.Store` interface for durability and concurrency.

## Roadmap

- [ ] SQLite store implementation (interface already in place)
- [ ] GitHub App (replace PAT, auto-manage webhooks)
- [ ] Managed databases (Postgres/Redis as first-class deployable services)
- [ ] Rollbacks + deployment history diffing
- [ ] In-UI terminal (run commands) and live container log viewer
- [ ] Multi-user + RBAC
- [ ] Health checks + zero-downtime swaps
```
