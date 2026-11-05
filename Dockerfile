# cometbft-mcp container image. The config is mounted read-only at
# /config/config.yaml; the container needs no other host access.
# Base images are pinned by digest; Dependabot (docker) keeps them current.

# Cross-compile on the build host instead of under emulation.
FROM --platform=$BUILDPLATFORM golang:1.27-bookworm@sha256:d9b6a1beb9474adc2a1ce842573639b44b2a0dd49b6669f51d3821baf9ec37c9 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG TARGETOS TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/cometbft-mcp ./cmd/cometbft-mcp

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
LABEL io.modelcontextprotocol.server.name="io.github.wookja-0/cometbft-mcp" \
      org.opencontainers.image.source="https://github.com/wookja-0/cometbft-mcp" \
      org.opencontainers.image.description="Read-only MCP server for CometBFT validator operators" \
      org.opencontainers.image.licenses="Apache-2.0"
COPY --from=build /out/cometbft-mcp /cometbft-mcp
USER nonroot:nonroot
ENTRYPOINT ["/cometbft-mcp", "--config", "/config/config.yaml"]
