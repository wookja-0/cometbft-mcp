package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wookja-0/cometbft-mcp/internal/config"
)

func registerPrompts(s *mcp.Server, cfg *config.Config) {
	s.AddPrompt(&mcp.Prompt{
		Name:        "daily_check",
		Title:       "Daily validator check",
		Description: "Routine health check of every node and validator, reported as a short operator briefing.",
	}, func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return userPrompt("Daily validator check", dailyCheck(cfg)), nil
	})

	s.AddPrompt(&mcp.Prompt{
		Name:        "incident_triage",
		Title:       "Incident triage",
		Description: "Structured diagnosis of a node or validator problem: gather evidence with the tools, rank likely causes, and list what the operator should check on the host.",
		Arguments: []*mcp.PromptArgument{
			{Name: "node", Description: "Node name from the config: " + strings.Join(cfg.Names(), ", "), Required: true},
			{Name: "symptom", Description: "What was observed, e.g. \"missed blocks alert\" or \"node stuck at height 1234\"."},
		},
	}, func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		name := req.Params.Arguments["node"]
		n, ok := cfg.Node(name)
		if !ok {
			return nil, fmt.Errorf("unknown node %q; configured nodes: %s", name, strings.Join(cfg.Names(), ", "))
		}
		return userPrompt("Incident triage for "+n.Name, incidentTriage(cfg, n, req.Params.Arguments["symptom"])), nil
	})
}

func userPrompt(desc, text string) *mcp.GetPromptResult {
	return &mcp.GetPromptResult{
		Description: desc,
		Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: text}}},
	}
}

func dailyCheck(cfg *config.Config) string {
	var b strings.Builder
	b.WriteString(`Run a daily health check of my CometBFT fleet.

1. Call fleet_overview.
2. For every validator that is not "ok", call validator_health on it (default window) to get exact counts`)
	if cfg.Cosmos != nil {
		b.WriteString(" and its jail margin")
	}
	b.WriteString(`.
3. For every node with sync or peer issues, call node_status or peer_health on that node.
`)
	if cfg.Cosmos != nil {
		b.WriteString("4. If fleet_overview shows a scheduled upgrade or upgrade proposals, call upgrade_readiness.\n")
	}
	b.WriteString(`
Report in this shape:
- First line: overall status (OK / WARN / CRITICAL) and the single most important thing.
- "Needs action" — critical items first. For each: node, finding code, the numbers from the tool results, and the concrete next step.
- "Watch" — warnings that need no action yet.
- "Healthy" — one line listing everything that is ok.

Quote heights, counts and times from the tool results; never estimate them. If everything is ok, say so in two lines.`)
	return b.String()
}

func incidentTriage(cfg *config.Config, n *config.Node, symptom string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Triage an incident on node %q (role: %s).\n", n.Name, n.Role)
	if symptom != "" {
		fmt.Fprintf(&b, "Reported symptom (operator's words): %s\n", symptom)
	}
	b.WriteString("\nGather evidence in this order, stopping to reason after each step:\n")
	fmt.Fprintf(&b, "1. node_status for %q and for the whole fleet (omit node) — is it reachable, synced, behind the others? Is the whole chain stalled? If no node has a new block, call consensus_diagnosis next: it shows who is not voting.\n", n.Name)
	fmt.Fprintf(&b, "2. peer_health for %q — isolated, too few peers, wrong chain, unexpected public peers?\n", n.Name)
	step := 3
	if n.Role == config.RoleValidator {
		fmt.Fprintf(&b, "%d. validator_health for %q — when did it last sign, is it missing blocks right now", step, n.Name)
		if cfg.Cosmos != nil {
			b.WriteString(", how close is it to downtime jail, is it already jailed or tombstoned")
		}
		b.WriteString("?\n")
		step++
	}
	if cfg.Cosmos != nil {
		fmt.Fprintf(&b, "%d. upgrade_readiness — is the chain halted at an upgrade height, or do versions differ across nodes?\n", step)
	}
	b.WriteString(`
Then answer with:
- Impact: what is broken right now and how urgent it is (for a validator: is it losing rewards, close to jail, jailed).
- Most likely causes, ranked, each with the tool evidence that supports or weakens it.
- What the operator should check on the host — the tools cannot see it: process and service state, recent logs (e.g. journalctl), disk space, clock sync, the priv_validator_state / remote signer, firewall and sentry connectivity.
- What would resolve it, with the riskiest steps called out. Never suggest running the same validator key on two machines: that double-signs and gets the validator tombstoned.

Separate facts from the tools from your hypotheses. Do not guess numbers.`)
	return b.String()
}
