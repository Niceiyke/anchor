# Anchor — ops cheatsheet

Day-to-day commands for the VPS running the all-in-one stack
(`docker compose --profile agent`). Run them from the repo directory.

## Update to the latest version

```bash
cd ~/anchor
git pull
docker compose --profile agent up -d --build
```

Only the changed images rebuild; containers restart in place. Your data
persists in the named volumes (see below), so updates are non-destructive.

> Drop `--profile agent` if this host runs the dashboard only (no local apps).

## Status & logs

```bash
docker compose ps                      # control-plane, anchor-caddy, agent
docker compose logs -f control-plane   # API / deploy orchestration
docker compose logs -f caddy           # TLS cert provisioning + routing
docker compose logs -f agent           # builds, deploys, container ops
```

## Restart / stop

```bash
docker compose restart control-plane   # restart one service
docker compose --profile agent down    # stop everything (keeps volumes/data)
docker compose --profile agent up -d   # start again
```

## The volumes (your state lives here)

| Volume         | Holds                                                        |
|----------------|-------------------------------------------------------------|
| `anchor-data`  | SQLite DB: servers, apps, deployments, databases, **GitHub App private key**, settings |
| `caddy-data`   | Caddy's TLS certificates / ACME account                     |
| `caddy-config` | Caddy autosave config                                        |
| `caddy-apps`   | Per-app reverse-proxy route snippets                         |
| `agent-work`   | Cloned repos / build workdir for the local agent            |

`docker compose down` does **not** delete volumes. `down -v` **does** — avoid it
unless you mean to wipe everything.

## Backup & restore

Back up the control-plane DB and Caddy certs (the two that matter most):

```bash
mkdir -p ~/anchor-backups
# control plane state (includes the GitHub App key — treat as a secret)
docker run --rm -v anchor_anchor-data:/data -v ~/anchor-backups:/out alpine \
  tar czf /out/anchor-data-$(date +%F).tar.gz -C /data .
# caddy certificates
docker run --rm -v anchor_caddy-data:/data -v ~/anchor-backups:/out alpine \
  tar czf /out/caddy-data-$(date +%F).tar.gz -C /data .
```

> Volume names are prefixed with the compose project (the repo dir name, usually
> `anchor`), e.g. `anchor_anchor-data`. Confirm with `docker volume ls`.

Restore into a fresh stack (while it's stopped):

```bash
docker compose --profile agent down
docker run --rm -v anchor_anchor-data:/data -v ~/anchor-backups:/in alpine \
  sh -c 'rm -rf /data/* && tar xzf /in/anchor-data-YYYY-MM-DD.tar.gz -C /data'
docker compose --profile agent up -d
```

## Rotate the admin password

`ANCHOR_ADMIN_*` only seed the admin on **first run**. To change it later, edit
the value in the DB or wipe `anchor-data` and re-seed (you'd re-add servers).
A proper "change password" flow is on the roadmap.

## Common checks

```bash
# is the local host registered + online?  -> Servers page, or:
docker compose logs control-plane | grep -i bootstrap
# what routes has Caddy loaded?
docker exec anchor-caddy cat /etc/caddy/Caddyfile
docker exec anchor-caddy ls /etc/caddy/apps
# free disk (deploys build images on the host)
df -h /var/lib/docker
docker system df          # reclaim with: docker system prune
```
