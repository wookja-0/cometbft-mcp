# cometbft-mcp container image. The config is mounted read-only at
# /config/config.yaml; the container needs no other host access.
FROM golang:1.27-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/cometbft-mcp ./cmd/cometbft-mcp

FROM gcr.io/distroless/static-debian12:nonroot
LABEL io.modelcontextprotocol.server.name="io.github.wookja-0/cometbft-mcp" \
      org.opencontainers.image.source="https://github.com/wookja-0/cometbft-mcp" \
      org.opencontainers.image.description="Read-only MCP server for CometBFT validator operators" \
      org.opencontainers.image.licenses="Apache-2.0"
COPY --from=build /out/cometbft-mcp /cometbft-mcp
USER nonroot:nonroot
ENTRYPOINT ["/cometbft-mcp", "--config", "/config/config.yaml"]
