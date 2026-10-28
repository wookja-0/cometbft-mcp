# cometbft-mcp

> A read-only MCP server for CometBFT node and validator operators.

Ask your agent *"Is my validator signing?"*, *"How close is it to jail?"* or *"Are we ready for the upgrade?"* and get an answer grounded in your own nodes and the chain's own slashing state, judged the way an operator would judge them.

Real output from the bundled local network, with `val3` stopped:

```
node_status        5 nodes: 4 ok, 1 critical — val3: rpc_unreachable
validator_health   val3 [critical]: signed 26/30 blocks (88–117), missed 4 (13.33%),
                   4 missed in a row at the head; 21 more misses until downtime jail
                   (~37s at the current rate)
upgrade_readiness  localnet-1 [ok]: upgrade <untrusted>v2-e2e</untrusted> at height 10000000
                   in ~164d20h (9999969 blocks)
```

(`<untrusted>` marks text that came from the chain — here, a governance proposal — so the agent treats it as data.)

Works with any CometBFT chain (Cosmos Hub, Osmosis, any Cosmos SDK chain and non-SDK CometBFT apps) and any MCP client (Claude Code, Claude Desktop, Cursor, …).

> [!WARNING]
> Early release (v0.2). The tool API may change.

## Security model

Validator operators should not have to trust an LLM with their infrastructure. So this server is built so that it **cannot** do damage, rather than promising it won't:

- **No keys, ever.** The config has no field that can point at `priv_validator_key.json`, a keyring or a mnemonic, and fields that look like secrets are rejected by name. No code path reads the filesystem besides the config file.
- **Read-only by construction.** Every request goes through one HTTP client that only issues `GET`s to a hard-coded allowlist: CometBFT `/status`, `/net_info`, `/validators`, `/commit`, `/abci_info`, `/health`, `/metrics`, and Cosmos SDK slashing, staking, upgrade and gov *queries*. `broadcast_*`, `unsafe_*`, `dial_*`, `check_tx` and `/cosmos/tx/` are explicitly denied, path parameters are pattern-checked, redirects are never followed, and responses are size-capped.
- **Network details are masked.** Peer IPs are reported only as a class (`private-ipv4`, `public-ipv4`, …), node IDs are truncated, and errors never carry an address, so a sentry-protected validator's IP does not end up in an LLM provider's logs. `--reveal-network` turns this off, explicitly.
- **Node text is untrusted.** Monikers, versions and other node-controlled strings are wrapped in `<untrusted>` tags, truncated, and stripped of control characters, so a hostile peer cannot inject instructions into your agent.
- **Gentle on nodes.** A global concurrency cap and bounded block scans; commits are read from sentries or full nodes first, not from the validator.

Details: [docs/security.md](docs/security.md).

## Tools

| Tool | Answers |
|---|---|
| `fleet_overview` | Everything at once: one line per node, one summary per validator, and the upgrade status. The starting point for "is everything ok?". |
| `node_status` | Is each node synced? Height, seconds since the last block, `catching_up`, blocks behind the rest of the fleet, versions, and for validators the consensus address and voting power. |
| `peer_health` | Is each node well connected? Peer count, inbound/outbound, peers on public addresses (a validator behind sentries should have none), peers on another chain. |
| `validator_health` | Is the validator signing? Signed / nil / missed / not-in-set over the last N blocks, consecutive misses at the head, voting power, rank and share of the set. Works while the validator itself is down. With the Cosmos SDK layer: jailed / tombstoned / bonding status, the chain's own missed-block counter, **misses until downtime jail** and **time to jail** at the current rate. |
| `upgrade_readiness` | Is an upgrade coming? The x/upgrade plan with ETA from the measured block time, upgrade proposals in voting, each node's app version, and whether the chain is halted at an upgrade height. *(Cosmos SDK layer only.)* |

Every result has a `level` (`ok` / `warn` / `critical` / `unknown`) and `findings` with stable codes such as `not_signing`, `jail_imminent`, `time_to_jail`, `jailed`, `upgrade_soon`, `behind_fleet` or `validator_public_peers`.

Two prompts package common workflows: **`daily_check`** (sweep, drill into what's flagged, report as an operator briefing) and **`incident_triage`** (evidence-first diagnosis of one node, ending with what to check on the host).

## Install

```bash
go install github.com/wookja-0/cometbft-mcp/cmd/cometbft-mcp@latest
```

## Configure

`~/.config/cometbft-mcp/config.yaml` (or `--config`, or `$COMETBFT_MCP_CONFIG`):

```yaml
# Optional: the Cosmos SDK layer (jail status, slashing margin, upgrades).
# Any sentry or full node's REST API works; the validator's own can stay off.
cosmos:
  rest: http://127.0.0.1:1317

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

`localnet/` runs a 4-validator + 1-sentry [simd](https://github.com/cosmos/cosmos-sdk/tree/main/simapp) network in Docker, with `val0` behind the sentry and genesis tuned so that downtime shows up in seconds (50-block slashing window, 50% min signed, 60 s jail, 30 s governance voting). `make localnet-up` writes `localnet/config.yaml` with the freshly generated validator addresses.

```bash
make localnet-up                       # first run builds simd (a few minutes)
make build
claude mcp add cometbft-local -- $PWD/bin/cometbft-mcp --config $PWD/localnet/config.yaml

make localnet-stop VAL=val3            # then ask: "is val3 signing? how long until it's jailed?"
make localnet-start VAL=val3           # …and watch it recover (or leave it ~40 s and watch it get jailed)
make localnet-down                     # delete everything
```

## How `validator_health` decides

A CometBFT commit lists every validator in the set at that height, in voting-power order, but **absent signatures carry no address**. So the tool looks for the validator's address in each commit, and when it is not there, it fetches the validator set at that height (cached by `validators_hash`) and reads the signature at the validator's index: absent means *missed*, no index means *not in the set*.

It scans canonical commits only, up to `latest - 1`. The commit a node returns for the newest height is its *seen commit*, which holds only the precommits that arrived before +2/3, so a validator behind a sentry would routinely look absent there. Canonical commits are also what downtime slashing counts.

With the Cosmos SDK layer it then reads the chain's own verdict inputs: `x/slashing` params and the validator's signing info, and `x/staking` (matched by consensus key) for jail and bonding status. The jail threshold is computed exactly as `x/slashing` does — `window − round_half_even(window × min_signed_per_window)` — including the grace period before `start_height + window`, during which downtime cannot jail. A future `jailed_until` is treated as jailed even if the staking lookup fails, because jailing resets the miss counter and would otherwise look like a clean record.

## Compatibility

| | |
|---|---|
| CometBFT | v0.34 – v0.39 (tested on v0.39.4 via simd v0.54.4) |
| Cosmos SDK | v0.47 – v0.54 REST (tested on v0.54.4); ed25519 consensus keys |
| Chains | Any CometBFT chain. Forks (celestia-core, sei-tendermint) and ICS consumer chains are untested. |
| Not supported | Tendermint2 (Gno) |

## Roadmap

- **v0.3** — `disk_forecast` from Prometheus, release binaries and a container image, MCP Registry listing
- ICS consumer chains (slashing and jailing live on the provider), secp256k1 consensus keys

## Development

```bash
make test         # unit tests, no network
make lint
make localnet-up
make e2e          # MCP over stdio against the localnet: downtime, a real jail + unjail, an upgrade proposal (~4 min)
```

Contributor notes and the invariants that must never break: [AGENTS.md](AGENTS.md).

## License

Apache-2.0
