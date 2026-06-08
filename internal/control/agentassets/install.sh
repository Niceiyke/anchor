#!/usr/bin/env bash
# Anchor agent installer — served by the control plane at /install.sh.
#
# One-line install (token shown once in the dashboard, Servers → Add server):
#   curl -fsSL https://anchor.example.com/install.sh | sudo bash -s -- --token=<token>
#
# The control plane URL is baked into this script when it is served; override it
# (and the token) with flags or environment variables:
#   ANCHOR_URL=...  ANCHOR_TOKEN=...   or   --url=...  --token=...
set -euo pipefail

# The control plane substitutes its own public URL as the default below.
ANCHOR_URL="${ANCHOR_URL:-__ANCHOR_URL__}"
ANCHOR_TOKEN="${ANCHOR_TOKEN:-}"

while [ $# -gt 0 ]; do
  case "$1" in
    --token=*) ANCHOR_TOKEN="${1#*=}" ;;
    --token)   ANCHOR_TOKEN="${2:-}"; shift ;;
    --url=*)   ANCHOR_URL="${1#*=}" ;;
    --url)     ANCHOR_URL="${2:-}"; shift ;;
    -h|--help) echo "usage: install.sh --token=<agent-token> [--url=<control-plane-url>]"; exit 0 ;;
    *) echo "error: unknown argument: $1" >&2; exit 1 ;;
  esac
  shift
done

ANCHOR_URL="${ANCHOR_URL%/}"
[ -n "$ANCHOR_URL" ]   || { echo "error: control plane URL not set (--url)" >&2; exit 1; }
[ -n "$ANCHOR_TOKEN" ] || { echo "error: agent token not set (--token)" >&2; exit 1; }
[ "$(id -u)" -eq 0 ]   || { echo "error: run as root (use sudo)" >&2; exit 1; }

case "$(uname -m)" in
  x86_64|amd64)  ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "error: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

if ! command -v docker >/dev/null 2>&1; then
  echo ">> Installing Docker"
  curl -fsSL https://get.docker.com | sh
fi
systemctl enable --now docker >/dev/null 2>&1 || true

echo ">> Downloading agent binary (linux/$ARCH)"
curl -fSL "$ANCHOR_URL/agent/download?arch=$ARCH" -o /usr/local/bin/anchor-agent
chmod 0755 /usr/local/bin/anchor-agent

echo ">> Creating directories"
mkdir -p /var/lib/anchor/apps /etc/anchor/caddy/apps

echo ">> Ensuring anchor_net docker network"
docker network inspect anchor_net >/dev/null 2>&1 || docker network create anchor_net

echo ">> Installing per-VPS Caddy (app router)"
curl -fsSL "$ANCHOR_URL/agent/caddyfile" -o /etc/anchor/caddy/Caddyfile
if ! docker ps --format '{{.Names}}' | grep -q '^caddy$'; then
  docker run -d --name caddy --restart unless-stopped \
    --network anchor_net \
    -p 80:80 -p 443:443 \
    -v /etc/anchor/caddy:/etc/caddy \
    -v anchor_caddy_data:/data -v anchor_caddy_config:/config \
    caddy:2-alpine
else
  echo "   caddy already running"
fi

echo ">> Installing systemd service"
cat >/etc/systemd/system/anchor-agent.service <<EOF
[Unit]
Description=Anchor Agent
After=network-online.target docker.service
Wants=network-online.target

[Service]
Environment=ANCHOR_URL=${ANCHOR_URL}
Environment=ANCHOR_TOKEN=${ANCHOR_TOKEN}
Environment=ANCHOR_WORKDIR=/var/lib/anchor/apps
Environment=ANCHOR_CADDY_DIR=/etc/anchor/caddy/apps
ExecStart=/usr/local/bin/anchor-agent
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now anchor-agent
echo ">> Done. The agent should appear online in the dashboard within a few seconds."
echo "   Check status with: systemctl status anchor-agent"
