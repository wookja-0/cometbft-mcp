# Contributing to cometbft-mcp

Thanks for helping. Bug reports, chain compatibility reports ("works on X", "breaks on Y"), docs fixes and new checks are all welcome.

## Scope

cometbft-mcp is **read-only by design**: it gives an agent insight into nodes and validators and cannot change anything. Changes that add signing, broadcasting, restarting or reconfiguring — or that read key files — will not be accepted, however convenient. The same goes for anything that weakens network masking or the `<untrusted>` envelope. [SECURITY.md](SECURITY.md) explains why; the invariants are listed in [AGENTS.md](AGENTS.md#security-invariants--never-break).

Good contributions: new judgments that need chain rules (with the rule's source cited), support for more chains or CometBFT/SDK versions, clearer findings, better docs.

For anything larger than a small fix, open an issue first so we can agree on the shape before you write it.

## Setup

- Go (version in [`go.mod`](go.mod))
- Docker, for the local network and e2e tests

```bash
make test           # unit tests, no network
make lint           # go vet + gofmt
make build          # bin/cometbft-mcp
```

To see changes against a real chain:

```bash
make localnet-up    # 4 validators + 1 sentry (simd); writes localnet/config.yaml
make e2e            # MCP over stdio: downtime, jail + unjail, upgrade proposal, chain halt (~5 min)
make localnet-down
go test -tags mainnet -v ./test/mainnet/   # read-only smoke test against Cosmos Hub public endpoints
```

The code map is in [AGENTS.md](AGENTS.md#map).

## Making a change

1. Branch from `main`: one feature or fix per branch.
2. Write the test first. Unit tests use the fake chain in `internal/fleet/fleet_test.go` or fixtures captured from real nodes; behavior that depends on a real chain also gets an e2e test.
3. New judgments go in `internal/analysis` with a stable finding code and a message that states its threshold. If you reimplement a chain rule, match the SDK or CometBFT source exactly and link it in a comment.
4. Keep tool descriptions in the what / when / returns / NOT shape, under 2,048 characters.
5. Update README and docs when behavior changes.
6. `make lint && make test` before pushing.

## Pull requests

- `main` is protected: everything lands through a pull request, squash-merged, with the `test` check passing.
- The squash commit takes the PR title, so title it as a [conventional commit](https://www.conventionalcommits.org/): `feat: …`, `fix: …`, `docs: …`, `ci: …`.
- CI skips the tests when only Markdown, images or `LICENSE` change. A maintainer can add the `e2e` label to run the localnet suite on the PR.
- Describe what changed and how you verified it — especially which chain and versions you tested against.

## Reporting security issues

Please do not open a public issue. Use [GitHub Security Advisories](https://github.com/wookja-0/cometbft-mcp/security/advisories/new) to report privately.

## License

By contributing, you agree that your contributions are licensed under the [Apache-2.0](LICENSE) license.
