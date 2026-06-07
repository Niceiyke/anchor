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

Point your dashboard domain's DNS at the VPS, then:

```bash
cp .env.example .env        # set admin creds + ANCHOR_DOMAIN
docker compose up -d --build
```

This starts the control plane behind **Caddy** (automatic HTTPS for
`ANCHOR_DOMAIN`). Open `https://<ANCHOR_DOMAIN>` and log in.

### All-in-one: host apps on the *same* VPS

The bundled Caddy is **unified** — it serves the dashboard *and* the apps you
deploy on this host, so there's no second Caddy and no port clash. To also run a
local agent (making this VPS a deploy target), set a shared token in `.env` and
enable the `agent` profile:

```bash
# in .env:
ANCHOR_LOCAL_AGENT_TOKEN=$(openssl rand -hex 32)   # generate once
ANCHOR_LOCAL_SERVER_NAME=this-vps

docker compose --profile agent up -d --build
```

The control plane auto-registers this host as a server (no manual "Add server"),
and the agent runs as a container with the Docker socket mounted — it builds and
runs your app containers on the host daemon and writes their routes into the same
Caddy. Apps reach managed databases by container name over `anchor_net`. Deploy
an app with a domain whose DNS points here and Caddy provisions its cert too.

> Separate VPSes are still supported and unchanged — install the standalone
> agent (below). Use the all-in-one only on the box that also runs the dashboard.

Day-to-day ops (update, logs, backups) live in [`deploy/UPDATE.md`](deploy/UPDATE.md).

**Each *additional* VPS you want to deploy to:**

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
   domain, container port, optional compose file, and toggle auto-deploy.
2. **Deploy now**, or push to the branch. Watch logs stream live.

On push, every app bound to that repo+branch with auto-deploy redeploys.

## Auto-assigned domains

Set a **Base domain** (Settings, or `ANCHOR_BASE_DOMAIN`), e.g. `apps.example.com`.
Apps created without a custom domain then get `<slug>.<base>` automatically
(`blog` → `blog.apps.example.com`), and Caddy issues HTTPS **on demand** on the
first request — gated by an `ask` endpoint (`/tls/check`) so it only mints certs
for domains Anchor manages.

**DNS — two ways:**

- **Cloudflare integration (zero manual DNS):** add a Cloudflare API token in
  **Settings → Cloudflare DNS** (scoped `Zone:DNS:Edit` + `Zone:Read`). Anchor
  then creates an A record for each app domain (and deletes it when the app is
  removed). The record points at the app's **server's public IP** (set per
  server on the Servers page), falling back to the global Settings IP, then
  auto-detect. Hit **Verify** to confirm the token sees your zone. No wildcard
  record needed.
- **Manual:** add a wildcard record yourself —
  `*.apps.example.com  A  <your VPS IP>`.

Either way, Caddy issues the certificate on demand. You can still type a custom
domain per app to override the auto one (point its DNS at the server — or, if
it's in your Cloudflare zone, Anchor creates that record too).

## Managing an app

Open an app (**Applications → name**) for its full control surface:

- **Deploy latest** — build & deploy the current HEAD of the branch.
- **Redeploy** — re-run any past deployment's exact commit (button on each
  deployment in the list). Empty/manual deployments redeploy latest.
- **Rollback** — redeploy the last *successful* commit (shown when available).
- **Stop** — stop & remove the app's container(s) (`compose down` / `rm -f`).
- **Configuration** (collapsible) — edit branch, domain, container port,
  compose file, and auto-deploy. Server and repo are immutable; changes apply
  on the next deploy. (`PATCH /api/apps/{id}`)
- **Environment & secrets** — add, edit, or remove env vars. Each var is 🔒
  **secret** (masked, the safe default) or 🔓 **plain** (shown) — toggle per
  variable, or "Reveal secrets" to peek. **Bulk import from .env** — paste a
  block or drop a `.env` file (comments/blanks ignored, `export` and quotes
  handled). Attached database connection strings are always secret. Variables
  are injected into the container on deploy.
- **Status badge** — a live running/stopped indicator (on the app header and
  the Apps list) derived from the agent's container list. Works for both
  Dockerfile (`<name>`) and Compose (`<name>-<service>-N`) apps.
- **Danger zone → Delete app** — removes the app *and* stops/removes its
  container(s) on the server. Also available as a row action on the Apps list.

## Terminal

**Terminal** runs an ad-hoc shell command on any online server (via the agent,
`sh -c`) and streams stdout/stderr live over SSE, ending with the exit code.
Output is live-only (not persisted). Handy for `docker ps`, `df -h`, tailing,
quick fixes.

## Containers

**Containers** lists every container on a server (`docker ps -a`) and
live-tails any one of them (`docker logs --timestamps -f`), streaming to the
browser over SSE. The follow is cancellable — closing the view or hitting Pause
sends a stop command so the agent kills the underlying `docker logs` process
(no orphaned follows). Container listing uses a request/reply over the agent
channel with an 8s timeout.

Per-container lifecycle controls (**Start / Stop / Restart / Remove**) and
cleanup actions are available on the same page:

- **Prune exited** — remove stopped containers (`docker container prune`).
- **Prune images** — remove dangling images (`docker image prune`; the API also
  supports `?all=true` for all unused images).
- **System prune** — `docker system prune`: stopped containers, unused networks,
  dangling images, and build cache. **Volumes are never pruned**, so managed
  database data is safe.

## Managed databases

**Databases** provisions **PostgreSQL** or **Redis** as a container on a target
server, with a persistent named volume and generated credentials. The agent
runs the image on the `anchor_net` network, so apps on the same server reach it
by its container hostname — no host port required (you can optionally expose
one). The control plane generates the connection string, e.g.:

```
postgres://anchor:<pw>@anchor-db-mydb:5432/mydb?sslmode=disable
redis://:<pw>@anchor-db-cache:6379
```

Status (`provisioning → running`, or `unreachable` if the server's agent drops)
is reported back by the agent. Deleting a database removes the container and, by
default, its data volume (`?keep_volume=true` to preserve it).

**Attach to an app:** on an app's page, the **Environment** section lets you
one-click attach any database on the *same server* — it injects the connection
string as an env var (`DATABASE_URL` / `REDIS_URL` by default, or a custom name).
You can also add/remove env vars by hand. Changes apply on the next deploy
(env vars become the compose `.env` / `docker run -e` flags).

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

## Security

- **Admin password** is hashed with **bcrypt**. Existing sha256 installs are
  upgraded transparently on next login. Change it in **Settings → Change admin
  password** (or seed via `ANCHOR_ADMIN_PASS` on first run).
- **Sessions** are persisted in the store (survive restarts/redeploys) and
  expire after 7 days; expired ones are purged periodically.
- **Secrets at rest** — encrypted with **AES-256-GCM**: GitHub App private key,
  client secret, webhook secret and PAT; the Cloudflare API token; agent tokens
  (deterministic so they're still matchable on connect); managed-database
  passwords; and **app environment variable values**. The key comes from
  `ANCHOR_SECRET_KEY` (recommended; keep it stable and outside the data volume)
  or an auto-generated key file in the data dir. Existing plaintext values are
  read through unchanged and encrypted on next write. Losing the key means
  re-entering those secrets.
- The Terminal runs arbitrary shell commands on the VPS as the agent user —
  it's behind admin auth, but treat access accordingly.

## Roadmap

- [x] SQLite store (default; JSON still available)
- [x] GitHub App (manifest flow, installation tokens, auto webhook)
- [x] In-UI terminal (live command execution over SSE)
- [x] Live container log viewer (list + cancellable follow)
- [x] Managed databases (Postgres/Redis, persistent volumes, conn strings)
- [x] One-click attach DB connection string into an app's env vars
- [ ] Rollbacks + deployment history diffing
- [ ] Multi-user + RBAC
- [ ] Health checks + zero-downtime swaps
```
