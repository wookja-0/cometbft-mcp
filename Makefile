BIN     := bin/cometbft-mcp
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMPOSE := docker compose -f localnet/docker-compose.yml
SIMD_IMAGE := cometbft-mcp/simd:v0.54.4

.PHONY: build test lint e2e localnet-image simd-image-name localnet-up localnet-down localnet-stop localnet-start

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

# Builds simd once; delete the image (docker rmi $(SIMD_IMAGE)) to rebuild.
localnet-image:
	@docker image inspect $(SIMD_IMAGE) >/dev/null 2>&1 || docker build -t $(SIMD_IMAGE) localnet

simd-image-name:
	@echo $(SIMD_IMAGE)

localnet-up: localnet-image
	$(COMPOSE) up -d
	localnet/gen-config.sh

localnet-down:
	$(COMPOSE) down -v

# make localnet-stop VAL=val3
localnet-stop:
	$(COMPOSE) stop $(VAL)

localnet-start:
	$(COMPOSE) start $(VAL)
