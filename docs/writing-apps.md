# Writing apps for Anchor

How to write a **Dockerfile** or **docker-compose** file that deploys cleanly on
Anchor — routes correctly, passes the health gate, receives its environment, and
tears down without leaking data.

> TL;DR: serve plain HTTP on `0.0.0.0`, `EXPOSE` the one real port, add a
> `HEALTHCHECK`, and for Compose add `env_file: [.env]` and set the **Service**
> to publish.

---

## How Anchor runs your app

- It builds **on the target VPS** — `docker compose -p <app> up -d --build`
  (Compose) or `docker build` + `docker run` (single Dockerfile). No registry
  required.
- **Caddy terminates TLS** and reverse-proxies `https://<domain>` →
  `<service-alias>:<port>` over the shared `anchor_net` Docker network. Your app
  serves **plain HTTP** internally; Caddy handles certificates automatically.
- After start, Anchor **health-gates** the deploy (and can auto-roll-back), then
  writes the Caddy route.

Anchor decides which service/port to publish in this order:

1. the **Service** you set on the app (most robust — no guessing);
2. the single service that listens on the configured **container port**;
3. the only service, when the project has just one.

For the **upstream port**: your configured port wins if the chosen container
exposes it; otherwise, if the container exposes exactly one port, Anchor routes
to that (and logs the adjustment). So exposing the real port means you rarely
have to repeat it.

---

## Dockerfile

```dockerfile
# Multi-stage: build fat, ship thin — faster builds on the VPS, smaller images.
FROM node:22-alpine AS build
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM node:22-alpine
WORKDIR /app
COPY --from=build /app/dist ./dist
COPY --from=build /app/node_modules ./node_modules
COPY package*.json ./

# 1) Declare the ONE port you serve on — Anchor auto-detects it.
EXPOSE 3000

# 2) Bind 0.0.0.0, never 127.0.0.1 (Caddy reaches you over the network).
ENV HOST=0.0.0.0 PORT=3000

# 3) Ship a health tool + HEALTHCHECK — the strongest health signal.
RUN apk add --no-cache wget
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=5 \
  CMD wget -qO- http://localhost:3000/healthz || exit 1

CMD ["node", "dist/server.js"]
```

Rules:

1. **`EXPOSE` the single real port.** Anchor auto-detects the upstream from it,
   so a stale container-port self-corrects. Expose exactly one so detection is
   unambiguous.
2. **Listen on `0.0.0.0:<port>`** — `127.0.0.1` is unreachable from Caddy (502).
3. **Add a `HEALTHCHECK`** and keep `wget` or `curl` in the final image. Anchor
   reads Docker's health status; without a tool in the image the HTTP probe is
   skipped and you fall back to coarse "is it running" gating.
4. **Serve HTTP, not HTTPS** — don't terminate TLS or force-redirect to https
   inside the container; Caddy does that. An in-container https redirect causes
   redirect loops.
5. **`.dockerignore`** (`.git`, `node_modules`, build caches). Builds run on the
   VPS, so this directly speeds deploys.
6. **Don't bake secrets** into the image — pass them as Anchor env vars
   (injected as `-e` for Dockerfile apps).

---

## Single-service compose

```yaml
services:
  web:
    build: .
    expose:
      - "3000"          # internal only; Caddy reaches it on anchor_net
    environment:
      - HOST=0.0.0.0
    env_file:
      - .env            # REQUIRED to receive Anchor's env vars (see below)
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "wget", "-qO-", "http://localhost:3000/healthz"]
      interval: 10s
      timeout: 3s
      retries: 5
      start_period: 20s
```

Set **Service = `web`** on the app to remove all port-guessing.

---

## ⚠️ Compose environment variables

For Compose apps, Anchor writes your env vars to a `.env` next to the compose
file **for `${VAR}` interpolation only** — Compose does **not** inject them into
containers automatically. A service receives them only if it opts in:

```yaml
services:
  web:
    env_file: [.env]                     # easiest: pull everything Anchor set
  api:
    environment:
      - DATABASE_URL=${DATABASE_URL}     # or reference specific keys
```

Single-Dockerfile apps don't need this — they get every var as `-e` automatically.

---

## Multi-service compose (web + api + worker + managed DB)

```yaml
networks:
  default: {}
  anchor_net:               # declare Anchor's shared net as external so ANY
    external: true          # service (not just routed ones) can reach managed
                            # databases / other apps by hostname
services:
  web:
    build: ./web
    expose: ["3000"]
    env_file: [.env]
    networks: [default, anchor_net]
    restart: unless-stopped
    depends_on:
      api: { condition: service_healthy }

  api:
    build: ./api
    expose: ["8080"]
    env_file: [.env]
    networks: [default, anchor_net]
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "curl", "-fsS", "http://localhost:8080/healthz"]
      interval: 10s
      timeout: 3s
      retries: 5

  worker:                   # no expose, no domain — internal only
    build: ./worker
    env_file: [.env]
    networks: [default, anchor_net]
    restart: unless-stopped
```

In Anchor:

- Primary **domain** → set **Service = `web`**.
- Add a **route**: `service: api`, `domain: api.example.com` (port auto-detected
  from `expose: 8080`, or set it; an optional per-route health path is supported).
- `worker` has no route → not publicly exposed, but **still health-gated** (it
  must stay running, or the deploy fails).

**Why declare `anchor_net` as external:** Anchor auto-attaches only the *routed*
services to `anchor_net`. A `worker` (or any non-routed service) that needs the
managed database or another app won't reach it unless it's on `anchor_net`.
Declaring it external and listing it per service guarantees connectivity and
stable hostnames. Services within the same project always reach each other by
service name on the project's `default` network regardless.

---

## Databases & volumes

- Prefer an **Anchor-managed database** (Databases page) over rolling your own in
  compose: it lives in its own volume, survives app deletion, and gives you a
  connection string to attach as an env var. Reach it by its container hostname
  over `anchor_net`.
- For your own data, use **named volumes**:

  ```yaml
      volumes:
        - app_data:/var/lib/app
  volumes:
    app_data: {}
  ```

- On app **delete**, Anchor removes the project's volumes by default
  (`compose down -v`); tick **Keep volumes** to preserve them. **Stop** never
  deletes volumes. The Apps-list quick-delete keeps volumes.
- **Avoid host bind-mounts** (`./data:/data`) for persistent data — the path may
  not exist on the VPS and isn't managed.

---

## Checklist & common pitfalls

| Symptom | Cause / fix |
|---|---|
| 502, or "can't tell which service" | `expose`/`EXPOSE` exactly one port on the web service; set **Service** |
| Caddy hits the wrong port | Expose the real (single) port so auto-detect works; or set the container port |
| Env vars missing (Compose) | add `env_file: [.env]` or reference `${VAR}` |
| Deploy fails the health gate | add a `HEALTHCHECK` + `wget`/`curl` in the image; bind `0.0.0.0`; raise the health timeout |
| Redirect loop / cert errors | serve **HTTP** inside the container; don't force-https |
| Running badge wrong | **don't** set `container_name:` — Anchor keys off compose labels and the `<app>-<service>-N` naming |
| Worker can't reach the DB | put it on `anchor_net` (external network block above) |
| Slow deploys | add `.dockerignore`; use multi-stage builds |

---

See also: [Routing & HTTPS](../README.md#routing--https) ·
[Health-gated deploys](../README.md#health-gated-deploys--auto-rollback) ·
[Managed databases](../README.md#managed-databases) in the README.
