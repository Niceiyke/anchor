.PHONY: help cp agent agent-linux agent-embed web run dev test tidy clean

help:
	@echo "Anchor make targets:"
	@echo "  make run         build web + run control plane locally (:8080)"
	@echo "  make dev         run control plane (:8080) for use with 'cd web && npm run dev'"
	@echo "  make cp          build control-plane binary -> bin/anchor-cp"
	@echo "  make agent       build agent binary         -> bin/anchor-agent"
	@echo "  make agent-linux build linux/amd64 agent    -> bin/anchor-agent-linux"
	@echo "  make agent-embed build linux amd64+arm64 agents into the control plane (for /agent/download)"
	@echo "  make web         build the web UI           -> web/dist"
	@echo "  make test        run Go tests"

cp:
	go build -o bin/anchor-cp ./cmd/control-plane

agent:
	go build -o bin/anchor-agent ./cmd/agent

agent-linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/anchor-agent-linux ./cmd/agent

# Cross-compile the agent for linux amd64+arm64 into the control plane's embed
# dir so it can serve them at /agent/download (one-script install). Run before
# `make cp`/`make run` when you want a self-contained control plane.
agent-embed:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o internal/control/agentbin/anchor-agent-linux-amd64 ./cmd/agent
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o internal/control/agentbin/anchor-agent-linux-arm64 ./cmd/agent

web:
	cd web && npm install && npm run build

run: web agent-embed cp
	ANCHOR_WEB_DIR=web/dist ./bin/anchor-cp

dev: cp
	./bin/anchor-cp

test:
	go test ./...

tidy:
	go mod tidy

clean:
	rm -rf bin web/dist web/node_modules
