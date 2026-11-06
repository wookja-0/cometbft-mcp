//go:build mainnet

// Read-only smoke test against real chains' public endpoints:
//
//	go test -tags mainnet -v ./test/mainnet/
//	go test -tags mainnet -v -run 'TestMainnet/osmosis' ./test/mainnet/
//
// Every config in chains/ is one subtest; COMETBFT_MCP_MAINNET_CONFIG runs a
// single config instead. It checks that every tool succeeds at mainnet scale
// (hundreds of validators, paging, pruned nodes, public rate limits) on each
// chain's CometBFT and SDK versions, and logs each summary with its latency.
// COMETBFT_MCP_MAINNET_DUMP=<dir> also writes each structured result there,
// for comparing against the chain's own state.
package mainnet

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wookja-0/cometbft-mcp/internal/config"
	"github.com/wookja-0/cometbft-mcp/internal/fleet"
	"github.com/wookja-0/cometbft-mcp/internal/safehttp"
	"github.com/wookja-0/cometbft-mcp/internal/tools"
)

// noSlashing lists chains without x/slashing and x/staking (ICS consumer
// chains): the Cosmos layer must say so and judge on liveness alone.
var noSlashing = map[string]bool{"stride": true}

func configs(t *testing.T) []string {
	if path := os.Getenv("COMETBFT_MCP_MAINNET_CONFIG"); path != "" {
		return []string{path}
	}
	_, file, _, _ := runtime.Caller(0)
	paths, err := filepath.Glob(filepath.Join(filepath.Dir(file), "chains", "*.yaml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no chain configs: %v", err)
	}
	return paths
}

func session(t *testing.T, cfg *config.Config) *mcp.ClientSession {
	t.Helper()
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

type chain struct {
	t    *testing.T
	cs   *mcp.ClientSession
	dump string
}

func (c *chain) call(tool string, args map[string]any) map[string]any {
	c.t.Helper()
	start := time.Now()
	res, err := c.cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		c.t.Fatalf("%s: %v", tool, err)
	}
	if res.IsError {
		c.t.Fatalf("%s %v: tool error: %v", tool, args, res.Content[0].(*mcp.TextContent).Text)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out map[string]any
	json.Unmarshal(raw, &out)
	c.t.Logf("%-20s %-36v %6.1fs  %s", tool, args, time.Since(start).Seconds(), out["summary"])
	if c.dump != "" {
		name := tool
		if n, ok := args["node"].(string); ok {
			name += "-" + n
		}
		os.WriteFile(filepath.Join(c.dump, name+".json"), raw, 0o644)
	}
	return out
}

func TestMainnet(t *testing.T) {
	for _, path := range configs(t) {
		name := strings.TrimSuffix(filepath.Base(path), ".yaml")
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			cfg, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			c := &chain{t: t, cs: session(t, cfg)}
			if dir := os.Getenv("COMETBFT_MCP_MAINNET_DUMP"); dir != "" {
				c.dump = filepath.Join(dir, name)
				if err := os.MkdirAll(c.dump, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			checkChain(t, c, cfg, noSlashing[name])
		})
	}
}

func checkChain(t *testing.T, c *chain, cfg *config.Config, noSlashing bool) {
	if st := c.call("node_status", nil); st["level"] != "ok" {
		t.Errorf("public nodes should be healthy: %v", st["summary"])
	}
	c.call("peer_health", map[string]any{"node": "rpc"})
	if cd := c.call("consensus_diagnosis", nil); cd["level"] == "unknown" {
		t.Errorf("consensus_diagnosis could not read the round: %v", cd["findings"])
	}
	if cfg.Cosmos != nil {
		up := c.call("upgrade_readiness", nil)
		if up["level"] == "unknown" {
			t.Errorf("upgrade_readiness could not read chain state: %v", up["findings"])
		}
	}

	for _, n := range cfg.Nodes {
		if n.Role != config.RoleValidator {
			continue
		}
		v := c.call("validator_health", map[string]any{"node": n.Name})
		as, _ := v["active_set"].(map[string]any)
		switch n.Name {
		case "top-validator":
			if as == nil || as["in_set"] != true || as["rank"] != float64(1) {
				t.Errorf("top validator should be rank 1 in the active set: %v", as)
			}
		case "bottom-validator":
			// On a full set it can be pushed out by stake at any time.
			if as == nil || as["in_set"] != true {
				t.Logf("note: the bottom validator has left the active set: %v", v["summary"])
			} else if as["rank"] != as["set_size"] {
				t.Logf("note: the bottom validator is no longer last: %v", as)
			}
		}
		if cfg.Cosmos == nil {
			continue
		}
		cv, _ := v["cosmos"].(map[string]any)
		switch {
		case cv == nil:
			t.Errorf("%s: no cosmos view", n.Name)
		case noSlashing:
			if cv["unsupported"] == nil || v["level"] == "unknown" {
				t.Errorf("%s: a chain without x/slashing should be judged on liveness alone: %v %v", n.Name, v["level"], cv)
			}
		case cv["error"] != nil || cv["operator_address"] == nil || cv["max_missed"] == nil:
			t.Errorf("%s: cosmos view: %v", n.Name, cv)
		case n.Name == "jailed" && cv["jailed"] != true:
			t.Logf("note: the jailed validator's state has changed: %v", cv)
		}
	}

	c.call("fleet_overview", nil)
}
