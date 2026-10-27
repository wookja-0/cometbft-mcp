# AGENTS.md — working on cometbft-mcp

A Go MCP server that gives operators read-only insight into CometBFT nodes and validators.

## Commands

```bash
make test          # unit tests (no network)
make lint          # go vet + gofmt
make build         # bin/cometbft-mcp
make localnet-up   # 4 validators + 1 sentry in Docker (localnet/)
make e2e           # MCP over stdio against the localnet (stops/starts val3)
```

## Map

| Path | What |
|---|---|
| `cmd/cometbft-mcp/` | flags, `check` / `version` subcommands, stdio server |
| `internal/safehttp/` | **the security boundary**: allowlisted GET-only client |
| `internal/source/comet/` | CometBFT RPC decoding (own structs, no cometbft dependency); fixtures in `testdata/` are real localnet responses |
| `internal/analysis/` | judgments as pure functions (no I/O, `now` passed in) |
| `internal/fleet/` | gathers data across nodes, builds reports, applies masking |
| `internal/sanitize/` | `<untrusted>` envelope, IP/node-ID masking |
| `internal/config/` | YAML config, strict fields, secret-field rejection |
| `internal/address/` | hex / bech32 valcons consensus addresses |
| `internal/tools/` | MCP tool registration and descriptions |
| `localnet/` | simd Dockerfile, genesis/topology setup, compose, MCP config |
| `test/e2e/` | build tag `localnet` |

## Security invariants — never break

- Network access only through `safehttp`. New endpoints are added to its allowlist in review; never construct requests elsewhere.
- No write-capable endpoint, ever: no `broadcast_*`, `unsafe_*`, `dial_*`, `check_tx`, and no tool that signs, restarts or reconfigures.
- No key material: no config field, flag or code path that reads key files, keyrings or mnemonics.
- Every node-controlled string reaches the model through `sanitize.Untrusted`.
- Peer IPs, node IDs, listen addresses and transport errors go through `sanitize.Network` / `fleet.nodeError`; reveal only behind `--reveal-network`.
- `TestReportsNeverLeakNetworkDetailsWhenMasked` and the `safehttp` deny tests must keep passing.

## Conventions

- Test first. Unit tests use the fake chain in `internal/fleet/fleet_test.go` or fixtures; anything that depends on real node behavior gets an e2e test.
- Judgments go in `analysis` with a stable finding `code`, a level, and a message that states the threshold it used.
- Tool descriptions say what / when / returns / NOT, and stay under 2,048 characters (`internal/tools/tools_test.go`).
- Conventional commits.
