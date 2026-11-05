# Security

cometbft-mcp sits between an LLM and production validator infrastructure. The design goal is that a confused or manipulated agent, a hostile peer, or a bug in a higher layer cannot cause harm, because the capability to do harm is absent rather than guarded.

## Threats and controls

| Threat | Control | Where |
|---|---|---|
| Validator key theft, double-signing | No config field for key paths; secret-looking fields rejected by name; no filesystem reads besides the config | `internal/config` |
| Agent changes node or chain state | One HTTP client; `GET` only; hard-coded path allowlist (CometBFT reads, Cosmos SDK slashing/staking/upgrade/gov queries) that code outside the package cannot extend; `broadcast_*`, `unsafe_*`, `dial_*`, `check_tx`, `/dump_consensus_state` (lists peer addresses), `/cosmos/tx/` denied even if added to the allowlist; per-endpoint query-parameter allowlist; path parameters (consensus addresses) must match `^[a-z0-9]{1,90}$`, so no traversal or injection; no redirects | `internal/safehttp` |
| Validator IP exposed to the LLM provider | Peer IPs reduced to an address class, node IDs truncated, listen addresses masked, transport errors (which name the address) replaced by codes; opt-out only via `--reveal-network` | `internal/sanitize`, `internal/fleet` |
| REST endpoint on the wrong chain | The REST node's chain ID is checked against the nodes' before any slashing or upgrade judgment (`rest_wrong_chain`) | `internal/fleet` |
| Prompt injection through anything a node or REST server returns — monikers, versions, upgrade plans, proposal titles, RPC and gateway error messages, malformed fields | Free text is wrapped in `<untrusted>…</untrusted>` (embedded tags neutralized, control and format characters removed, truncated to 128 runes). Structured values (addresses, chain IDs, node IDs, ports, bond status, durations) pass through only if they match their format, and are wrapped otherwise — in `--reveal-network` mode too. Decode errors are never echoed, because Go's errors quote the offending input. `TestHostileNodeTextStaysEnveloped` feeds a hostile node through every tool. | `internal/sanitize`, `internal/source`, `internal/fleet` |
| Load on production nodes | Global in-flight cap (4); bounded scans (≤ 500 blocks); commits read from sentries/full nodes first; response size cap (8 MiB); per-request timeout | `internal/safehttp`, `internal/fleet` |
| Exposed RPC ports | Docs steer to localhost, SSH tunnels or private networks; nothing in the server needs a public port | README |

## Out of scope

- **The MCP client and LLM provider.** Masked output still describes your topology (node names, roles, heights, peer counts). Get your organization's approval before pointing this at production.
- **`structuredContent` consumers.** The `<untrusted>` envelope is inside the JSON strings; a client that renders them is responsible for its own presentation.
- **A compromised binary.** Release archives and images carry GitHub build-provenance attestations (`gh attestation verify`); all CI actions are pinned by commit SHA. Running the container image adds filesystem isolation on top of the code-level guarantees: it sees only the read-only config mount.

## Reporting

Please report vulnerabilities privately through GitHub Security Advisories on this repository rather than in a public issue.
