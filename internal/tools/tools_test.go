package tools

import (
	"context"
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

func session(t *testing.T) (*mcp.ClientSession, *config.Config) {
	t.Helper()
	cfg, err := config.Parse([]byte("nodes:\n  - {name: val-1, role: validator, rpc: 'http://127.0.0.1:1'}\n  - {name: sentry-1, role: sentry, rpc: 'http://127.0.0.1:1'}\n"))
	if err != nil {
		t.Fatal(err)
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, &mcp.ServerOptions{Instructions: Instructions(cfg)})
	Register(s, fleet.New(cfg, safehttp.New(safehttp.Options{}), fleet.Options{}))

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

func TestToolSurface(t *testing.T) {
	cs, cfg := session(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Tools) != 3 {
		t.Fatalf("tools = %d", len(res.Tools))
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
	cs, _ := session(t)
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
	cs, _ := session(t)
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "validator_health", Arguments: map[string]any{"node": "sentry-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError || !strings.Contains(res.Content[0].(*mcp.TextContent).Text, "not validator") {
		t.Fatalf("result = %+v", res.Content)
	}
}
