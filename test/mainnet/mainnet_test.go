//go:build mainnet

// Read-only smoke test against a real chain's public endpoints:
//
//	go test -tags mainnet -v ./test/mainnet/
//
// Override the chain with COMETBFT_MCP_MAINNET_CONFIG. It checks that every
// tool succeeds at mainnet scale (hundreds of validators, paging, pruned
// nodes, public rate limits) and logs each summary with its latency.
package mainnet

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wookja-0/cometbft-mcp/internal/config"
	"github.com/wookja-0/cometbft-mcp/internal/fleet"
	"github.com/wookja-0/cometbft-mcp/internal/safehttp"
	"github.com/wookja-0/cometbft-mcp/internal/tools"
)

func session(t *testing.T) *mcp.ClientSession {
	t.Helper()
	path := os.Getenv("COMETBFT_MCP_MAINNET_CONFIG")
	if path == "" {
		_, file, _, _ := runtime.Caller(0)
		path = filepath.Join(filepath.Dir(file), "cosmoshub.yaml")
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	conc, _ := strconv.Atoi(os.Getenv("COMETBFT_MCP_MAINNET_CONCURRENCY")) // 0 = default
	f := fleet.New(cfg, safehttp.New(safehttp.Options{Timeout: 20 * time.Second, MaxConcurrent: conc}), fleet.Options{})
	s := mcp.NewServer(&mcp.Implementation{Name: "mainnet", Version: "test"}, nil)
	tools.Register(s, f, cfg)
	ct, st := mcp.NewInMemoryTransports()
	if _, err := s.Connect(context.Background(), st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "test"}, nil).Connect(context.Background(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

func call(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) map[string]any {
	t.Helper()
	start := time.Now()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	if res.IsError {
		t.Fatalf("%s %v: tool error: %v", tool, args, res.Content[0].(*mcp.TextContent).Text)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out map[string]any
	json.Unmarshal(raw, &out)
	t.Logf("%-20s %-28v %6.1fs  %s", tool, args, time.Since(start).Seconds(), out["summary"])
	return out
}

func TestMainnet(t *testing.T) {
	cs := session(t)

	if st := call(t, cs, "node_status", nil); st["level"] != "ok" {
		t.Errorf("public nodes should be healthy: %v", st["summary"])
	}
	call(t, cs, "peer_health", map[string]any{"node": "publicnode"})
	call(t, cs, "consensus_diagnosis", nil)
	if up := call(t, cs, "upgrade_readiness", nil); up["level"] == "unknown" {
		t.Errorf("upgrade_readiness could not read chain state: %v", up["findings"])
	}

	top := call(t, cs, "validator_health", map[string]any{"node": "top-validator"})
	if as, _ := top["active_set"].(map[string]any); as == nil || as["in_set"] != true {
		t.Errorf("top validator should be in the active set: %v", top["active_set"])
	}
	if c, _ := top["cosmos"].(map[string]any); c == nil || c["error"] != nil || c["operator_address"] == nil {
		t.Errorf("top validator cosmos view: %v", top["cosmos"])
	}

	call(t, cs, "validator_health", map[string]any{"node": "rank-100", "window": 500})

	j := call(t, cs, "validator_health", map[string]any{"node": "jailed"})
	if c, _ := j["cosmos"].(map[string]any); c == nil || c["jailed"] != true {
		t.Logf("note: the jailed validator's state has changed: %v", j["cosmos"])
	}

	call(t, cs, "fleet_overview", nil)
}
