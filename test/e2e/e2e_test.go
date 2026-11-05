//go:build localnet

// End-to-end tests against the docker-compose localnet, over real MCP stdio.
//
//	make localnet-up && make e2e
//
// The tests change chain state: they stop and restart validators, get val2
// jailed and unjailed, and pass an upgrade proposal for a far-future height.
// Reset with `make localnet-down && make localnet-up`. Takes ~4 minutes.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..")
}

func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	root := repoRoot()
	bin := filepath.Join(t.TempDir(), "cometbft-mcp")
	build := exec.Command("go", "build", "-o", bin, "./cmd/cometbft-mcp")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	cmd := exec.Command(bin, "--config", filepath.Join(root, "localnet", "config.yaml"))
	cmd.Stderr = os.Stderr

	client := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "test"}, nil)
	s, err := client.Connect(context.Background(), &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func call(t *testing.T, s *mcp.ClientSession, tool string, args map[string]any) map[string]any {
	t.Helper()
	res, err := s.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	if res.IsError {
		t.Fatalf("%s: tool error: %v", tool, res.Content)
	}
	raw, _ := json.Marshal(res.StructuredContent)
	var out map[string]any
	json.Unmarshal(raw, &out)
	return out
}

// poll calls tool until done returns true or the timeout passes.
func poll(t *testing.T, s *mcp.ClientSession, timeout time.Duration, tool string, args map[string]any, done func(map[string]any) bool) map[string]any {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		r := call(t, s, tool, args)
		if done(r) {
			return r
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: condition not met within %s; last: %v", tool, timeout, r["summary"])
		}
		time.Sleep(3 * time.Second)
	}
}

func compose(t *testing.T, args ...string) string {
	t.Helper()
	cmd := exec.Command("docker", append([]string{"compose", "-f", filepath.Join(repoRoot(), "localnet", "docker-compose.yml")}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// simdTx signs and broadcasts a transaction inside a validator's container
// with its test keyring. This is test tooling: cometbft-mcp itself cannot sign.
func simdTx(t *testing.T, node int, args ...string) {
	t.Helper()
	home := fmt.Sprintf("/data/node%d/simd", node)
	full := append([]string{"exec", "-T", fmt.Sprintf("val%d", node), "simd", "tx"}, args...)
	full = append(full, "--from", fmt.Sprintf("node%d", node), "--keyring-backend", "test", "--home", home,
		"--chain-id", "localnet-1", "--gas", "400000", "--yes", "--output", "json")
	out := compose(t, full...)
	var res struct {
		Code   int    `json:"code"`
		RawLog string `json:"raw_log"`
	}
	if err := json.Unmarshal([]byte(out[strings.Index(out, "{"):]), &res); err != nil || res.Code != 0 {
		t.Fatalf("tx %v: code %d %s (%v)\n%s", args, res.Code, res.RawLog, err, out)
	}
}

func cosmosOf(t *testing.T, r map[string]any) map[string]any {
	t.Helper()
	c, ok := r["cosmos"].(map[string]any)
	if !ok {
		t.Fatalf("no cosmos section: %v", r)
	}
	if e, ok := c["error"]; ok {
		t.Fatalf("cosmos error: %v", e)
	}
	return c
}

func TestToolsAreReadOnly(t *testing.T) {
	s := connect(t)
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s is not annotated read-only", tool.Name)
		}
	}
	if strings.Join(names, ",") != "consensus_diagnosis,fleet_overview,node_status,peer_health,upgrade_readiness,validator_health" {
		t.Fatalf("tools = %v", names)
	}
}

func TestHealthyLocalnet(t *testing.T) {
	s := connect(t)

	ov := call(t, s, "fleet_overview", nil)
	t.Logf("fleet_overview: %s", ov["summary"])
	if ov["level"] == "critical" || len(ov["validators"].([]any)) != 4 {
		t.Fatalf("fleet_overview: %v", ov["summary"])
	}

	pe := call(t, s, "peer_health", map[string]any{"node": "val0"})
	val0 := pe["nodes"].([]any)[0].(map[string]any)
	if val0["total"].(float64) != 1 || val0["public_peers"].(float64) != 0 || val0["level"] != "ok" {
		t.Fatalf("val0 should have exactly the sentry as a private peer: %v", val0)
	}

	for _, v := range []string{"val0", "val1", "val2", "val3"} {
		r := call(t, s, "validator_health", map[string]any{"node": v, "window": 50})
		c := cosmosOf(t, r)
		if r["miss_streak"].(float64) != 0 || r["level"] == "critical" || r["source_node"] != "sentry" {
			t.Fatalf("%s: %v", v, r["summary"])
		}
		// Staking lookup matched the validator by its consensus key.
		if c["jailed"] != false || c["status"] != "BOND_STATUS_BONDED" || !strings.HasPrefix(c["operator_address"].(string), "cosmosvaloper1") {
			t.Fatalf("%s cosmos: %v", v, c)
		}
		if c["max_missed"].(float64) != 25 || c["misses_until_jail"].(float64) != 25-c["missed_in_window"].(float64) {
			t.Fatalf("%s slashing margin: %v", v, c)
		}
	}
}

func TestUpgradeReadiness(t *testing.T) {
	s := connect(t)
	before := call(t, s, "upgrade_readiness", nil)
	if before["plan"] != nil {
		t.Skipf("a plan is already scheduled (%v); reset the localnet to rerun", before["summary"])
	}

	proposal := `{
  "messages": [{
    "@type": "/cosmos.upgrade.v1beta1.MsgSoftwareUpgrade",
    "authority": "cosmos10d07y265gmmuvt4z0w9aw880jnsr700j6zn9kn",
    "plan": {"name": "v2-e2e", "height": "10000000", "info": ""}
  }],
  "deposit": "10000000stake",
  "title": "Upgrade to v2-e2e",
  "summary": "e2e test upgrade"
}`
	compose(t, "exec", "-T", "val0", "sh", "-c", "cat > /tmp/proposal.json <<'EOF'\n"+proposal+"\nEOF")
	simdTx(t, 0, "gov", "submit-proposal", "/tmp/proposal.json")

	r := poll(t, s, 30*time.Second, "upgrade_readiness", nil, func(r map[string]any) bool {
		return len(r["upgrade_proposals_in_voting"].([]any)) > 0
	})
	p := r["upgrade_proposals_in_voting"].([]any)[0].(map[string]any)
	t.Logf("in voting: %s", r["summary"])
	if p["upgrade_name"] != "<untrusted>v2-e2e</untrusted>" || p["upgrade_height"].(float64) != 10000000 {
		t.Fatalf("proposal = %v", p)
	}

	for i := range 4 {
		simdTx(t, i, "gov", "vote", fmt.Sprint(p["id"]), "yes")
	}
	r = poll(t, s, 90*time.Second, "upgrade_readiness", nil, func(r map[string]any) bool { return r["plan"] != nil })
	t.Logf("scheduled: %s", r["summary"])
	plan := r["plan"].(map[string]any)
	if plan["name"] != "<untrusted>v2-e2e</untrusted>" || plan["height"].(float64) != 10000000 || plan["eta"] == nil || r["avg_block_seconds"].(float64) <= 0 {
		t.Fatalf("plan = %v", plan)
	}
	if len(r["nodes"].([]any)) != 5 {
		t.Fatalf("nodes = %v", r["nodes"])
	}
}

func TestValidatorDowntime(t *testing.T) {
	s := connect(t)
	compose(t, "stop", "val3")
	stopped := true
	t.Cleanup(func() {
		if stopped {
			compose(t, "start", "val3")
		}
	})
	// 3 of 4 validators keep the chain going (> 2/3 power); wait for ≥ 3 misses.
	time.Sleep(15 * time.Second)

	r := call(t, s, "validator_health", map[string]any{"node": "val3", "window": 30})
	t.Logf("val3 down: %s", r["summary"])
	c := cosmosOf(t, r)
	if r["level"] != "critical" || r["miss_streak"].(float64) < 3 || r["source_node"] != "sentry" {
		t.Fatalf("val3 down: %v", r)
	}
	// On a fresh chain the validator is still in its first signing window,
	// where x/slashing cannot jail it yet; the estimate only applies after.
	if _, grace := c["grace_until_height"]; grace {
		if c["time_to_jail"] != nil {
			t.Fatalf("time_to_jail during the grace window: %v", c)
		}
	} else if c["time_to_jail"] == nil || c["misses_until_jail"].(float64) >= 25 {
		t.Fatalf("val3 jail estimate: %v", c)
	}

	st := call(t, s, "node_status", map[string]any{"node": "val3"})
	n := st["nodes"].([]any)[0].(map[string]any)
	if n["level"] != "critical" || n["error"].(map[string]any)["code"] == "" {
		t.Fatalf("val3 status: %v", n)
	}
	if strings.Contains(st["summary"].(string), "127.0.0.1") {
		t.Fatalf("summary leaks the RPC address: %s", st["summary"])
	}

	compose(t, "start", "val3")
	stopped = false
	r = poll(t, s, 90*time.Second, "validator_health", map[string]any{"node": "val3", "window": 30}, func(r map[string]any) bool {
		return r["miss_streak"].(float64) == 0 && r["level"] != "critical"
	})
	t.Logf("val3 recovered: %s", r["summary"])
}

func TestJailAndUnjail(t *testing.T) {
	s := connect(t)
	args := map[string]any{"node": "val2", "window": 30}

	compose(t, "stop", "val2")
	stopped := true
	t.Cleanup(func() {
		if stopped {
			compose(t, "start", "val2")
		}
	})

	// > 25 misses in the 50-block window jails it.
	r := poll(t, s, 2*time.Minute, "validator_health", args, func(r map[string]any) bool {
		return cosmosOf(t, r)["jailed"] == true
	})
	t.Logf("val2 jailed: %s", r["summary"])
	c := cosmosOf(t, r)
	if c["jailed_until"] == nil || c["status"] == "BOND_STATUS_BONDED" || !strings.HasSuffix(r["summary"].(string), "; JAILED") {
		t.Fatalf("jailed view: %v", c)
	}
	ov := call(t, s, "fleet_overview", nil)
	if ov["level"] != "critical" || !strings.Contains(ov["summary"].(string), "val2") {
		t.Fatalf("overview: %s", ov["summary"])
	}

	compose(t, "start", "val2")
	stopped = false
	until, _ := time.Parse(time.RFC3339, c["jailed_until"].(string))
	time.Sleep(time.Until(until) + 5*time.Second)
	// Wait for the restarted node to catch up so it can sign once unjailed.
	// (It reports no_voting_power while jailed; that is expected.)
	poll(t, s, time.Minute, "node_status", map[string]any{"node": "val2"}, func(r map[string]any) bool {
		n := r["nodes"].([]any)[0].(map[string]any)
		behind, ok := n["behind_fleet_blocks"].(float64)
		return n["error"] == nil && n["catching_up"] == false && ok && behind < 5
	})
	simdTx(t, 2, "slashing", "unjail")

	r = poll(t, s, time.Minute, "validator_health", args, func(r map[string]any) bool {
		c := cosmosOf(t, r)
		in, _ := r["active_set"].(map[string]any)
		// Unjailing takes effect in the validator set two blocks later; wait
		// until it is back in the set and signing.
		return c["jailed"] == false && c["status"] == "BOND_STATUS_BONDED" &&
			in != nil && in["in_set"] == true && r["miss_streak"].(float64) == 0 && r["last_signed_height"] != nil
	})
	t.Logf("val2 unjailed: %s", r["summary"])
}

func TestChainHalt(t *testing.T) {
	s := connect(t)
	// Two of four equal validators offline leaves 50% of power: no quorum.
	compose(t, "stop", "val2", "val3")
	stopped := true
	t.Cleanup(func() {
		if stopped {
			compose(t, "start", "val2", "val3")
		}
	})

	r := poll(t, s, 2*time.Minute, "consensus_diagnosis", nil, func(r map[string]any) bool {
		return r["level"] == "critical"
	})
	t.Logf("halted: %s", r["summary"])
	fs := r["findings"].([]any)[0].(map[string]any)
	pv := r["prevotes"].(map[string]any)
	if fs["code"] != "no_quorum" || pv["quorum"] != false || pv["missing"].(float64) != 2 {
		t.Fatalf("diagnosis: %v", r)
	}
	if !strings.Contains(r["summary"].(string), "not voting: val2, val3") && !strings.Contains(r["summary"].(string), "not voting: val3, val2") {
		t.Fatalf("summary should name val2 and val3: %s", r["summary"])
	}

	ov := call(t, s, "fleet_overview", nil)
	if ov["level"] != "critical" || !strings.Contains(ov["summary"].(string), "no_quorum") {
		t.Fatalf("overview: %s", ov["summary"])
	}

	compose(t, "start", "val2", "val3")
	stopped = false
	r = poll(t, s, 2*time.Minute, "consensus_diagnosis", nil, func(r map[string]any) bool {
		return r["level"] == "ok"
	})
	t.Logf("recovered: %s", r["summary"])
}
