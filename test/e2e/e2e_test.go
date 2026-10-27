//go:build localnet

// End-to-end tests against the docker-compose localnet, over real MCP stdio.
//
//	make localnet-up && make e2e
//
// TestValidatorDowntime stops and restarts a validator container; it takes
// about a minute.
package e2e

import (
	"context"
	"encoding/json"
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
	if strings.Join(names, ",") != "node_status,peer_health,validator_health" {
		t.Fatalf("tools = %v", names)
	}
}

func TestHealthyLocalnet(t *testing.T) {
	s := connect(t)

	st := call(t, s, "node_status", nil)
	if st["level"] != "ok" || !strings.HasPrefix(st["summary"].(string), "5 nodes: 5 ok") {
		t.Fatalf("node_status: %v", st["summary"])
	}

	pe := call(t, s, "peer_health", map[string]any{"node": "val0"})
	val0 := pe["nodes"].([]any)[0].(map[string]any)
	if val0["total"].(float64) != 1 || val0["public_peers"].(float64) != 0 || val0["level"] != "ok" {
		t.Fatalf("val0 should have exactly the sentry as a private peer: %v", val0)
	}

	// Earlier downtime tests can leave misses in the window, so check that
	// every validator is signing now rather than that the window is clean.
	for _, v := range []string{"val0", "val1", "val2", "val3"} {
		r := call(t, s, "validator_health", map[string]any{"node": v, "window": 50})
		in := r["active_set"].(map[string]any)
		if r["miss_streak"].(float64) != 0 || r["level"] == "critical" || in["in_set"] != true || r["source_node"] != "sentry" {
			t.Fatalf("%s: %v", v, r["summary"])
		}
	}
}

func TestValidatorDowntime(t *testing.T) {
	s := connect(t)
	compose := func(args ...string) {
		cmd := exec.Command("docker", append([]string{"compose", "-f", filepath.Join(repoRoot(), "localnet", "docker-compose.yml")}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("docker compose %v: %v\n%s", args, err, out)
		}
	}

	compose("stop", "val3")
	stopped := true
	t.Cleanup(func() {
		if stopped {
			compose("start", "val3")
		}
	})
	time.Sleep(15 * time.Second) // 3 of 4 validators keep the chain going (> 2/3 power)

	r := call(t, s, "validator_health", map[string]any{"node": "val3", "window": 30})
	t.Logf("val3 down: %s", r["summary"])
	if r["level"] != "critical" || r["miss_streak"].(float64) < 3 || r["source_node"] != "sentry" {
		t.Fatalf("val3 down: %v", r)
	}

	st := call(t, s, "node_status", map[string]any{"node": "val3"})
	n := st["nodes"].([]any)[0].(map[string]any)
	if n["level"] != "critical" || n["error"].(map[string]any)["code"] == "" {
		t.Fatalf("val3 status: %v", n)
	}
	if strings.Contains(st["summary"].(string), "127.0.0.1") {
		t.Fatalf("summary leaks the RPC address: %s", st["summary"])
	}

	compose("start", "val3")
	stopped = false
	deadline := time.Now().Add(90 * time.Second)
	for {
		r = call(t, s, "validator_health", map[string]any{"node": "val3", "window": 30})
		if r["miss_streak"].(float64) == 0 && r["level"] != "critical" {
			t.Logf("val3 recovered: %s", r["summary"])
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("val3 did not recover: %s", r["summary"])
		}
		time.Sleep(3 * time.Second)
	}
}
