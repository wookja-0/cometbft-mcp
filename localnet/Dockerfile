# simd (Cosmos SDK simapp) for the local test network.
FROM golang:1.26-bookworm AS build
ARG SDK_VERSION=v0.54.4
RUN git clone --depth 1 --branch ${SDK_VERSION} https://github.com/cosmos/cosmos-sdk /src
WORKDIR /src/simapp
RUN go build -o /out/simd ./simd

FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends jq ca-certificates \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/simd /usr/local/bin/simd
