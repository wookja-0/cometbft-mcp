package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wookja-0/cometbft-mcp/internal/config"
	"github.com/wookja-0/cometbft-mcp/internal/fleet"
	"github.com/wookja-0/cometbft-mcp/internal/safehttp"
)

// Claude Code shows the model only the first 2,048 characters of the server
// instructions and of each tool description.
const clientTextLimit = 2048

const nodesYAML = "nodes:\n  - {name: val-1, role: validator, rpc: 'http://127.0.0.1:1'}\n  - {name: sentry-1, role: sentry, rpc: 'http://127.0.0.1:1'}\n"

func session(t *testing.T, withCosmos bool) (*mcp.ClientSession, *config.Config) {
	t.Helper()
	y := nodesYAML
	if withCosmos {
		y = "cosmos: {rest: 'http://127.0.0.1:1'}\n" + y
	}
	cfg, err := config.Parse([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, &mcp.ServerOptions{Instructions: Instructions(cfg)})
	Register(s, fleet.New(cfg, safehttp.New(safehttp.Options{}), fleet.Options{}), cfg)

	ct, st := mcp.NewInMemoryTransports()
	if _, err := s.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "test"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs, cfg
}

func toolNames(t *testing.T, cs *mcp.ClientSession) string {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	return strings.Join(names, ",")
}

func TestUpgradeToolOnlyWithCosmos(t *testing.T) {
	without, _ := session(t, false)
	if got := toolNames(t, without); got != "consensus_diagnosis,fleet_overview,node_status,peer_health,validator_health" {
		t.Fatalf("without cosmos: %s", got)
	}
	with, _ := session(t, true)
	if got := toolNames(t, with); got != "consensus_diagnosis,fleet_overview,node_status,peer_health,upgrade_readiness,validator_health" {
		t.Fatalf("with cosmos: %s", got)
	}
}

func TestToolSurface(t *testing.T) {
	cs, cfg := session(t, true)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range res.Tools {
		if len(tool.Description) > clientTextLimit {
			t.Errorf("%s description is %d chars", tool.Name, len(tool.Description))
		}
		a := tool.Annotations
		if a == nil || !a.ReadOnlyHint || a.DestructiveHint == nil || *a.DestructiveHint {
			t.Errorf("%s must be annotated read-only and non-destructive", tool.Name)
		}
		if tool.OutputSchema == nil {
			t.Errorf("%s has no output schema", tool.Name)
		}
	}
	if n := len(Instructions(cfg)); n > clientTextLimit {
		t.Errorf("instructions are %d chars", n)
	}
	if !strings.Contains(Instructions(cfg), "val-1 (validator), sentry-1 (sentry)") {
		t.Errorf("instructions do not list nodes: %s", Instructions(cfg))
	}
}

func TestUnreachableNodeIsAReportNotAnError(t *testing.T) {
	cs, _ := session(t, false)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "node_status", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("unexpected tool error: %v", res.Content)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.HasPrefix(text, "2 nodes: 2 critical — ") || strings.Contains(text, "127.0.0.1") {
		t.Fatalf("text = %s", text)
	}
}

func TestBadArgumentIsAToolError(t *testing.T) {
	cs, _ := session(t, false)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "validator_health", Arguments: map[string]any{"node": "sentry-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "not validator") {
		t.Fatalf("result = %+v", res.Content)
	}
}

func TestPrompts(t *testing.T) {
	cs, _ := session(t, true)
	ctx := context.Background()
	list, err := cs.ListPrompts(ctx, nil)
	if err != nil || len(list.Prompts) != 2 {
		t.Fatalf("prompts = %+v, %v", list, err)
	}

	daily, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "daily_check"})
	if err != nil {
		t.Fatal(err)
	}
	text := daily.Messages[0].Content.(*mcp.TextContent).Text
	if !strings.Contains(text, "fleet_overview") || !strings.Contains(text, "upgrade_readiness") {
		t.Fatalf("daily_check = %s", text)
	}

	triage, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "incident_triage", Arguments: map[string]string{"node": "val-1", "symptom": "missed blocks alert"}})
	if err != nil {
		t.Fatal(err)
	}
	text = triage.Messages[0].Content.(*mcp.TextContent).Text
	for _, want := range []string{`"val-1"`, "missed blocks alert", "validator_health", "how close is it to downtime jail", "double-signs"} {
		if !strings.Contains(text, want) {
			t.Errorf("incident_triage lacks %q", want)
		}
	}

	if _, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "incident_triage", Arguments: map[string]string{"node": "nope"}}); err == nil {
		t.Fatal("unknown node should fail")
	}
}

func TestPromptsWithoutCosmosDoNotMentionUpgrades(t *testing.T) {
	cs, _ := session(t, false)
	daily, err := cs.GetPrompt(context.Background(), &mcp.GetPromptParams{Name: "daily_check"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(daily.Messages[0].Content.(*mcp.TextContent).Text, "upgrade_readiness") {
		t.Fatal("daily_check references a tool that is not registered")
	}
}

// Without a config the server still lists its tools (directories and
// clients probe this) and every call returns the setup guidance.
func TestUnconfiguredServerListsToolsAndExplainsSetup(t *testing.T) {
	setupErr := errors.New("no config file at /x/config.yaml")
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, &mcp.ServerOptions{Instructions: UnconfiguredInstructions(setupErr)})
	RegisterUnconfigured(s, setupErr)
	ct, st := mcp.NewInMemoryTransports()
	if _, err := s.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "test"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()

	if got := toolNames(t, cs); got != "consensus_diagnosis,fleet_overview,node_status,peer_health,upgrade_readiness,validator_health" {
		t.Fatalf("tools = %s", got)
	}
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "upgrade_readiness", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "no config file") {
		t.Fatalf("result = %+v", res.Content)
	}
}
