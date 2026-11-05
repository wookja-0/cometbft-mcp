## What and why

<!-- What changes, and the operator problem it solves. Link the issue if there is one. -->

## How it was verified

<!-- Which chain(s) and CometBFT / SDK versions, if behavior against real nodes changed. -->

- [ ] `make lint && make test`
- [ ] e2e on the localnet (add the `e2e` label) and/or `go test -tags mainnet ./test/mainnet/`, if chain parsing or judgments changed

## Checklist

- [ ] PR title is a conventional commit (`feat: …`, `fix: …`, `docs: …`) — it becomes the squash commit
- [ ] Tests added or updated
- [ ] README / docs updated if behavior changed
- [ ] Keeps the [security invariants](https://github.com/wookja-0/cometbft-mcp/blob/main/AGENTS.md#security-invariants--never-break): read-only, no key material, node text through `sanitize.Untrusted`, network details masked
