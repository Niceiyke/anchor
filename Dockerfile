# Multi-stage build for the Anchor control plane (Go API + embedded web UI).
FROM node:24-alpine AS web
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS api
WORKDIR /src
COPY go.mod ./
COPY pkg ./pkg
COPY internal ./internal
COPY cmd ./cmd
RUN CGO_ENABLED=0 go build -o /anchor-cp ./cmd/control-plane

FROM alpine:3.20
RUN adduser -D -u 10001 anchor
WORKDIR /app
COPY --from=api /anchor-cp /usr/local/bin/anchor-cp
COPY --from=web /web/dist /app/web
ENV ANCHOR_ADDR=:8080
ENV ANCHOR_DB=/data/anchor.db
ENV ANCHOR_WEB_DIR=/app/web
VOLUME /data
USER anchor
EXPOSE 8080
ENTRYPOINT ["anchor-cp"]
