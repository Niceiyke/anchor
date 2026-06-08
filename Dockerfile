# Multi-stage build for the Anchor control plane (Go API + embedded web UI).
FROM node:24-alpine AS web
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS api
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY pkg ./pkg
COPY internal ./internal
COPY cmd ./cmd
# Cross-compile the agent for linux amd64+arm64 into the embed dir so the
# control plane can serve version-matched binaries at /agent/download.
RUN GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o internal/control/agentbin/anchor-agent-linux-amd64 ./cmd/agent && \
    GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o internal/control/agentbin/anchor-agent-linux-arm64 ./cmd/agent
RUN CGO_ENABLED=0 go build -o /anchor-cp ./cmd/control-plane

FROM alpine:3.20
RUN adduser -D -u 10001 anchor
WORKDIR /app
COPY --from=api /anchor-cp /usr/local/bin/anchor-cp
COPY --from=web /web/dist /app/web
# /data holds the SQLite DB; must be writable by the non-root anchor user.
# Owning it in the image also sets the ownership a fresh named volume inherits.
RUN mkdir -p /data && chown -R anchor:anchor /data
ENV ANCHOR_ADDR=:8080
ENV ANCHOR_DB=/data/anchor.db
ENV ANCHOR_WEB_DIR=/app/web
VOLUME /data
USER anchor
EXPOSE 8080
ENTRYPOINT ["anchor-cp"]
