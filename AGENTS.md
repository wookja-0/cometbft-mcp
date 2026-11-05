# AGENTS.md — working on cometbft-mcp

A Go MCP server that gives operators read-only insight into CometBFT nodes and validators.

Human contributors: start with [CONTRIBUTING.md](CONTRIBUTING.md). This file is the same ground in more detail.

## Commands

```bash
make test          # unit tests (no network)
make lint          # go vet + gofmt
make build         # bin/cometbft-mcp
make localnet-up   # 4 validators + 1 sentry in Docker (localnet/)
make e2e           # MCP over stdio against the localnet (downtime, jail + unjail, upgrade proposal, chain halt; mutates chain state)
go test -tags mainnet -v ./test/mainnet/   # read-only smoke test on Cosmos Hub public endpoints (~1 min)
```

## Map

| Path | What |
|---|---|
| `cmd/cometbft-mcp/` | flags, `check` / `version` subcommands, stdio server |
| `internal/safehttp/` | **the security boundary**: allowlisted GET-only client |
| `internal/source/comet/` | CometBFT RPC decoding (own structs, no cometbft dependency); fixtures in `testdata/` are real localnet responses |
| `internal/source/cosmos/` | Cosmos SDK REST decoding: slashing, staking, upgrade, gov (no cosmos-sdk dependency) |
| `internal/analysis/` | judgments as pure functions (no I/O, `now` passed in) |
| `internal/fleet/` | gathers data across nodes, builds reports, applies masking |
| `internal/sanitize/` | `<untrusted>` envelope, IP/node-ID masking |
| `internal/config/` | YAML config, strict fields, secret-field rejection |
| `internal/address/` | hex / bech32 valcons consensus addresses |
| `internal/tools/` | MCP tool registration and descriptions |
| `localnet/` | simd Dockerfile, genesis/topology setup, compose; `gen-config.sh` writes the (gitignored) MCP config from the running nodes |
| `docs/logo/` | brand assets: `icon.svg` (rounded) and `icon-square.svg`, PNG exports, `logo-dark.png` / `logo-light.png` (README header, matched to GitHub's dark and light backgrounds), `social-preview.png` (GitHub social preview, 1280×640) |
| `docs/demo/` | README demo: `demo.sh` (live scenario), `mcpcall` (calls one tool over stdio), `frame.py` (window frame); see the header of `demo.sh` |
| `test/e2e/` | build tag `localnet` |
| `test/mainnet/` | build tag `mainnet`: smoke test at mainnet scale; run it after changing anything that parses chain data |

## Security invariants — never break

- Network access only through `safehttp`. New endpoints are added to its allowlist in review; never construct requests elsewhere.
- No write-capable endpoint, ever: no `broadcast_*`, `unsafe_*`, `dial_*`, `check_tx`, `/cosmos/tx/`, and no tool that signs, restarts or reconfigures. (The e2e tests send unjail and governance transactions with `docker exec` — test tooling, never the server.)
- No key material: no config field, flag or code path that reads key files, keyrings or mnemonics.
- Every node-controlled string reaches the model through `sanitize.Untrusted`.
- Peer IPs, node IDs, listen addresses and transport errors go through `sanitize.Network` / `fleet.nodeError`; reveal only behind `--reveal-network`.
- Structured values from a node (addresses, IDs, ports, enums, durations) pass through only after a format check; never put a decode error's text (`err.Error()` from `encoding/json`, `strconv`, `time`) into output — it quotes the input.
- `TestReportsNeverLeakNetworkDetailsWhenMasked`, `TestHostileNodeTextStaysEnveloped` and the `safehttp` deny tests must keep passing.

## Conventions

- Test first. Unit tests use the fake chain in `internal/fleet/fleet_test.go` or fixtures; anything that depends on real node behavior gets an e2e test.
- Judgments go in `analysis` with a stable finding `code`, a level, and a message that states the threshold it used.
- Chain rules that `analysis` reimplements (e.g. `MaxMissed`) must match the SDK source exactly and cite it in a comment.
- Tools that need optional config (e.g. `upgrade_readiness` needs `cosmos.rest`) are registered only when it is present, and prompts must not mention unregistered tools. Exception: with no usable config, every tool is listed (each call returns the setup guidance) so directories show the full surface.
- Tool descriptions say what / when / returns / NOT, and stay under 2,048 characters (`internal/tools/tools_test.go`).
- Conventional commits.

## Workflow

- `main` is protected: changes land through a pull request, squash-merged, with the `test` check passing. No direct pushes, force pushes or deletions — for anyone, admins included.
- One feature or fix per branch and PR (`feat/…`, `fix/…`, `docs/…`, `ci/…`). The squash commit takes the PR title, so write it as a conventional commit.
- CI skips `test` when only Markdown, images or `LICENSE` change; the skipped check still counts as passing. Label a PR `e2e` to run the localnet suite on it.

## Releasing

Bump `version` and the image tag in `server.json` (and the image tag in README), then push a `vX.Y.Z` tag. `.github/workflows/release.yml` checks they agree, tests, builds binaries and the multi-arch image with provenance attestations, creates the GitHub release, and publishes `server.json` to the MCP Registry over GitHub OIDC.
