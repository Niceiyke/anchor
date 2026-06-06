#!/usr/bin/env bash
# Install the Anchor agent on a VPS.
#
# Prerequisites: Docker installed, and the `anchor-agent` binary present next to
# this script (build it with `make agent` and scp both files over).
#
# Usage:
#   sudo ANCHOR_URL=https://anchor.example.com ANCHOR_TOKEN=<token> ./install-agent.sh
#
set -euo pipefail

: "${ANCHOR_URL:?set ANCHOR_URL to your control plane URL}"
: "${ANCHOR_TOKEN:?set ANCHOR_TOKEN to the agent token from the dashboard}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_SRC="${ANCHOR_AGENT_BIN:-$SCRIPT_DIR/anchor-agent}"

if [ ! -x "$BIN_SRC" ]; then
  echo "error: agent binary not found at $BIN_SRC (build with 'make agent')" >&2
  exit 1
fi
if ! command -v docker >/dev/null 2>&1; then
  echo "error: docker is required but not installed" >&2
  exit 1
fi

echo ">> Installing agent binary"
install -m 0755 "$BIN_SRC" /usr/local/bin/anchor-agent

echo ">> Creating directories"
mkdir -p /var/lib/anchor/apps /etc/anchor/caddy/apps

echo ">> Ensuring anchor_net docker network"
docker network inspect anchor_net >/dev/null 2>&1 || docker network create anchor_net

echo ">> Starting per-VPS Caddy (app router)"
install -m 0644 "$SCRIPT_DIR/../deploy/agent-caddy/Caddyfile" /etc/anchor/caddy/Caddyfile 2>/dev/null || \
  cp "$SCRIPT_DIR/Caddyfile" /etc/anchor/caddy/Caddyfile 2>/dev/null || true
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
echo ">> Done. Check status with: systemctl status anchor-agent"
