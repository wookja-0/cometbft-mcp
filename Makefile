BIN     := bin/cometbft-mcp
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMPOSE := docker compose -f localnet/docker-compose.yml

.PHONY: build test lint e2e localnet-up localnet-down localnet-stop localnet-start

build:
	go build -ldflags "-X main.version=$(VERSION)" -o $(BIN) ./cmd/cometbft-mcp

test:
	go test ./...

lint:
	go vet ./...
	go vet -tags localnet ./test/...
	@test -z "$$(gofmt -l .)" || (gofmt -l . && echo "gofmt needed" && exit 1)

# End-to-end over MCP stdio against the localnet (stops/starts val3).
e2e:
	go test -tags localnet -count=1 -v ./test/e2e/

localnet-up:
	$(COMPOSE) up -d --build

localnet-down:
	$(COMPOSE) down -v

# make localnet-stop VAL=val3
localnet-stop:
	$(COMPOSE) stop $(VAL)

localnet-start:
	$(COMPOSE) start $(VAL)
