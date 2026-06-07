.PHONY: help cp agent agent-linux web run dev test tidy clean

help:
	@echo "Anchor make targets:"
	@echo "  make run         build web + run control plane locally (:8080)"
	@echo "  make dev         run control plane (:8080) for use with 'cd web && npm run dev'"
	@echo "  make cp          build control-plane binary -> bin/anchor-cp"
	@echo "  make agent       build agent binary         -> bin/anchor-agent"
	@echo "  make agent-linux build linux/amd64 agent    -> bin/anchor-agent-linux"
	@echo "  make web         build the web UI           -> web/dist"
	@echo "  make test        run Go tests"

cp:
	go build -o bin/anchor-cp ./cmd/control-plane

agent:
	go build -o bin/anchor-agent ./cmd/agent

agent-linux:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/anchor-agent-linux ./cmd/agent

web:
	cd web && npm install && npm run build

run: web cp
	ANCHOR_WEB_DIR=web/dist ./bin/anchor-cp

dev: cp
	./bin/anchor-cp

test:
	go test ./...

tidy:
	go mod tidy

clean:
	rm -rf bin web/dist web/node_modules
