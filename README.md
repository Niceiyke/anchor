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
internal/control     API, agent hub, auth, github app, deploy, exec, SSE
internal/agent       command loop, deploy pipeline, caddy, system stats
internal/store       persistence: SQLite (default) + JSON, behind Store interface
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

## Connecting GitHub

Two options, in **Settings**:

1. **GitHub App (recommended)** — click **Connect GitHub App**. This uses the
   GitHub *manifest flow*: you're sent to GitHub to create a pre-configured app
   in one click, redirected back (we store its private key + secrets), then sent
   to install it on the repos you want. From then on Anchor mints short-lived
   installation tokens for listing repos and cloning, and the push webhook is
   configured automatically. Requires the control plane to be reachable at a
   public URL (GitHub must redirect back and deliver webhooks).
2. **Personal access token (fallback)** — paste a PAT with `repo` scope. For
   auto-deploy, add a repo webhook → `https://anchor.example.com/webhooks/github`
   (content type `application/json`, event `push`, secret from Settings).

## Deploy flow

1. **Applications → Deploy a new app** — pick a repo, target server, branch,
   domain, container port, and toggle auto-deploy.
2. **Deploy now**, or push to the branch. Watch logs stream live.

On push, every app bound to that repo+branch with auto-deploy redeploys.

## Terminal

**Terminal** runs an ad-hoc shell command on any online server (via the agent,
`sh -c`) and streams stdout/stderr live over SSE, ending with the exit code.
Output is live-only (not persisted). Handy for `docker ps`, `df -h`, tailing,
quick fixes.

## Container Logs

**Container Logs** lists every container on a server (`docker ps -a`) and
live-tails any one of them (`docker logs --timestamps -f`), streaming to the
browser over SSE. The follow is cancellable — closing the view or hitting Pause
sends a stop command so the agent kills the underlying `docker logs` process
(no orphaned follows). Container listing uses a request/reply over the agent
channel with an 8s timeout.

## Storage

Defaults to **SQLite** at `anchor.db` (pure-Go `modernc.org/sqlite`, no CGO).
Set `ANCHOR_DB` to a path ending in `.json` to use the simple JSON store
instead. Both implement the same `store.Store` interface.

## Routing & HTTPS

Each VPS runs Caddy on `anchor_net`. On deploy the agent writes
`<domain> { reverse_proxy <app>:<port> }` to `/etc/anchor/caddy/apps/<app>.caddy`
and reloads Caddy, which provisions TLS automatically. Point the domain's DNS at
the VPS and it's live.

> For `docker-compose` apps, attach the public-facing service to the external
> `anchor_net` network (and name it to match the app) so Caddy can reach it.

## Security notes (MVP → harden before public exposure)

- Admin password is hashed with SHA-256 — **swap for bcrypt/argon2**.
- Sessions are in-memory; agent tokens are bearer strings in the store.
- GitHub App private key + secrets are stored in plaintext in the DB —
  **encrypt at rest** (or use a secrets manager) before exposing publicly.
- The Terminal runs arbitrary shell commands on the VPS as the agent user —
  it's behind admin auth, but treat access accordingly.

## Roadmap

- [x] SQLite store (default; JSON still available)
- [x] GitHub App (manifest flow, installation tokens, auto webhook)
- [x] In-UI terminal (live command execution over SSE)
- [x] Live container log viewer (list + cancellable follow)
- [ ] Managed databases (Postgres/Redis as first-class deployable services)
- [ ] Rollbacks + deployment history diffing
- [ ] Multi-user + RBAC
- [ ] Health checks + zero-downtime swaps
```
