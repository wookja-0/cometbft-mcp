# Security

cometbft-mcp sits between an LLM and production validator infrastructure. The design goal is that a confused or manipulated agent, a hostile peer, or a bug in a higher layer cannot cause harm, because the capability to do harm is absent rather than guarded.

## Threats and controls

| Threat | Control | Where |
|---|---|---|
| Validator key theft, double-signing | No config field for key paths; secret-looking fields rejected by name; no filesystem reads besides the config | `internal/config` |
| Agent changes node or chain state | One HTTP client; `GET` only; hard-coded path allowlist that code outside the package cannot extend; `broadcast_*`, `unsafe_*`, `dial_*`, `check_tx` denied even if added to the allowlist; per-endpoint query-parameter allowlist; no redirects | `internal/safehttp` |
| Validator IP exposed to the LLM provider | Peer IPs reduced to an address class, node IDs truncated, listen addresses masked, transport errors (which name the address) replaced by codes; opt-out only via `--reveal-network` | `internal/sanitize`, `internal/fleet` |
| Prompt injection through monikers, versions, app names | Wrapped in `<untrusted>…</untrusted>`; embedded envelope tags neutralized; control and format characters removed; truncated to 128 runes | `internal/sanitize` |
| Load on production nodes | Global in-flight cap (4); bounded scans (≤ 500 blocks); commits read from sentries/full nodes first; response size cap (8 MiB); per-request timeout | `internal/safehttp`, `internal/fleet` |
| Exposed RPC ports | Docs steer to localhost, SSH tunnels or private networks; nothing in the server needs a public port | README |

## Out of scope

- **The MCP client and LLM provider.** Masked output still describes your topology (node names, roles, heights, peer counts). Get your organization's approval before pointing this at production.
- **`structuredContent` consumers.** The `<untrusted>` envelope is inside the JSON strings; a client that renders them is responsible for its own presentation.
- **A compromised binary.** Install from source or verify release checksums. Running the container image (planned, v0.3) adds filesystem isolation on top of the code-level guarantees.

## Reporting

Please report vulnerabilities privately through GitHub Security Advisories on this repository rather than in a public issue.
