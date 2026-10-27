# cometbft-mcp

> A read-only MCP server for CometBFT node and validator operators.

Ask your agent *"Is my validator signing?"* or *"Why is val3 behind?"* and get an answer grounded in your own nodes: sync status, peer topology and signing liveness, judged the way an operator would judge them.

Real output from the bundled local network, with `val3` stopped:

```
node_status       5 nodes: 4 ok, 1 critical — val3: rpc_unreachable
validator_health  val3 [critical]: signed 25/30 blocks (447–476), missed 5 (16.67%),
                  5 missed in a row at the head
```

Works with any CometBFT chain (Cosmos Hub, Osmosis, any Cosmos SDK chain and non-SDK CometBFT apps) and any MCP client (Claude Code, Claude Desktop, Cursor, …).

> [!WARNING]
> Early release (v0.1). The tool API may change.

## Security model

Validator operators should not have to trust an LLM with their infrastructure. So this server is built so that it **cannot** do damage, rather than promising it won't:

- **No keys, ever.** The config has no field that can point at `priv_validator_key.json`, a keyring or a mnemonic, and fields that look like secrets are rejected by name. No code path reads the filesystem besides the config file.
- **Read-only by construction.** Every request goes through one HTTP client that only issues `GET`s to a hard-coded allowlist (`/status`, `/net_info`, `/validators`, `/commit`, `/abci_info`, `/health`, `/metrics`). `broadcast_*`, `unsafe_*`, `dial_*` and `check_tx` are explicitly denied, redirects are never followed, and responses are size-capped.
- **Network details are masked.** Peer IPs are reported only as a class (`private-ipv4`, `public-ipv4`, …), node IDs are truncated, and errors never carry an address, so a sentry-protected validator's IP does not end up in an LLM provider's logs. `--reveal-network` turns this off, explicitly.
- **Node text is untrusted.** Monikers, versions and other node-controlled strings are wrapped in `<untrusted>` tags, truncated, and stripped of control characters, so a hostile peer cannot inject instructions into your agent.
- **Gentle on nodes.** A global concurrency cap and bounded block scans; commits are read from sentries or full nodes first, not from the validator.

Details: [docs/security.md](docs/security.md).

## Tools

| Tool | Answers |
|---|---|
| `node_status` | Is each node synced? Height, seconds since the last block, `catching_up`, blocks behind the rest of the fleet, versions, and for validators the consensus address and voting power. |
| `peer_health` | Is each node well connected? Peer count, inbound/outbound, peers on public addresses (a validator behind sentries should have none), peers on another chain. |
| `validator_health` | Is the validator signing? Signed / nil / missed / not-in-set over the last N blocks, consecutive misses at the head, voting power, rank and share of the set. Works while the validator itself is down. |

Every result has a `level` (`ok` / `warn` / `critical` / `unknown`) and `findings` with stable codes such as `not_signing`, `left_active_set`, `behind_fleet` or `validator_public_peers`.

## Install

```bash
go install github.com/wookja-0/cometbft-mcp/cmd/cometbft-mcp@latest
```

## Configure

`~/.config/cometbft-mcp/config.yaml` (or `--config`, or `$COMETBFT_MCP_CONFIG`):

```yaml
nodes:
  - name: sentry-1
    role: sentry                     # validator | sentry | full
    rpc: http://127.0.0.1:26657      # CometBFT RPC; use an SSH tunnel, don't expose it
  - name: val-1
    role: validator
    rpc: http://127.0.0.1:36657
    # Lets validator_health work while this node is down. Hex or bech32 valcons.
    validator_address: cosmosvalcons1…
```

Check it from the command line:

```bash
cometbft-mcp check
```

Reach nodes over `localhost`, an SSH tunnel (`ssh -N -L 36657:127.0.0.1:26657 val-1`) or a private network. Do not open RPC ports to the internet for this.

## Connect a client

**Claude Code**

```bash
claude mcp add cometbft -- cometbft-mcp --config ~/.config/cometbft-mcp/config.yaml
```

**Claude Desktop / Cursor** (`mcpServers` in the client's config):

```json
{
  "mcpServers": {
    "cometbft": { "command": "cometbft-mcp", "args": ["--config", "/Users/you/.config/cometbft-mcp/config.yaml"] }
  }
}
```

## Try it on a local network

`localnet/` runs a 4-validator + 1-sentry [simd](https://github.com/cosmos/cosmos-sdk/tree/main/simapp) network in Docker, with `val0` behind the sentry and genesis tuned so that downtime shows up in seconds (50-block slashing window, 1 s blocks).

```bash
make localnet-up                       # first run builds simd (a few minutes)
make build
claude mcp add cometbft-local -- $PWD/bin/cometbft-mcp --config $PWD/localnet/config.yaml

make localnet-stop VAL=val3            # then ask: "is val3 signing?"
make localnet-start VAL=val3           # …and watch it recover
make localnet-down                     # delete everything
```

## How `validator_health` decides

A CometBFT commit lists every validator in the set at that height, in voting-power order, but **absent signatures carry no address**. So the tool looks for the validator's address in each commit, and when it is not there, it fetches the validator set at that height (cached by `validators_hash`) and reads the signature at the validator's index: absent means *missed*, no index means *not in the set*.

It scans canonical commits only, up to `latest - 1`. The commit a node returns for the newest height is its *seen commit*, which holds only the precommits that arrived before +2/3, so a validator behind a sentry would routinely look absent there. Canonical commits are also what downtime slashing counts.

## Compatibility

| | |
|---|---|
| CometBFT | v0.34 – v0.39 (tested on v0.39.4 via simd v0.54.4) |
| Chains | Any CometBFT chain. Forks (celestia-core, sei-tendermint) and ICS consumer chains are untested. |
| Not supported | Tendermint2 (Gno) |

## Roadmap

- **v0.2** — Cosmos SDK layer: jail status, exact margin to the downtime-slashing threshold, `upgrade_readiness`; `fleet_overview`; `daily_check` and `incident_triage` prompts
- **v0.3** — `disk_forecast` from Prometheus, release binaries and a container image, MCP Registry listing

## Development

```bash
make test         # unit tests, no network
make lint
make localnet-up
make e2e          # MCP over stdio against the localnet; stops and restarts val3
```

Contributor notes and the invariants that must never break: [AGENTS.md](AGENTS.md).

## License

Apache-2.0
