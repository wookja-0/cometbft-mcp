package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wookja-0/cometbft-mcp/internal/config"
)

func TestSetupErrorMissingFile(t *testing.T) {
	_, err := config.Load(filepath.Join(t.TempDir(), "missing.yaml"))
	got := setupError(config.Source{Path: "/home/op/.config/cometbft-mcp/config.yaml"}, err).Error()
	if !strings.Contains(got, "no config file at /home/op/.config/cometbft-mcp/config.yaml") || !strings.Contains(got, exampleConfigURL) {
		t.Fatalf("got %q", got)
	}
	// Hosted runners (Glama's Try in Browser) start without a config: the
	// message carries a public-endpoint config that can be pasted as is.
	if !strings.Contains(got, config.EnvInline+" to "+publicExampleConfig) {
		t.Fatalf("no pasteable example: %q", got)
	}
	cfg, err := config.Source{Inline: publicExampleConfig}.Load()
	if err != nil || cfg.Cosmos == nil || len(cfg.Nodes) != 1 {
		t.Fatalf("example config: %+v, %v", cfg, err)
	}
}

// An invalid config's parse error can quote its values (internal node
// addresses); the guidance sent to the model must not.
func TestSetupErrorNeverEchoesConfigContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	os.WriteFile(path, []byte("nodes:\n  - {name: a, role: 10.9.8.7, rpc: 'http://10.9.8.7:26657'}\n"), 0o600)
	_, err := config.Load(path)
	if err == nil {
		t.Fatal("expected an invalid config")
	}
	got := setupError(config.Source{Path: path}, err).Error()
	if strings.Contains(got, "10.9.8.7") || !strings.Contains(got, "cometbft-mcp check --config") {
		t.Fatalf("got %q", got)
	}
}

func TestSetupErrorInlineNeverEchoesContent(t *testing.T) {
	src := config.Source{Inline: "nodes: [{name: a, role: 10.9.8.7, rpc: 'http://10.9.8.7:26657'}]"}
	_, err := src.Load()
	if err == nil {
		t.Fatal("expected an invalid config")
	}
	got := setupError(src, err).Error()
	if strings.Contains(got, "10.9.8.7") || !strings.Contains(got, config.EnvInline) {
		t.Fatalf("got %q", got)
	}
}

// Flags go after the subcommand; anything left over is a mistake, not a
// reason to start serving MCP on a terminal and wait on stdin.
func TestStrayArgumentsAreRejected(t *testing.T) {
	for _, args := range [][]string{{"--config", "x.yaml", "check"}, {"chek"}} {
		err := run(args)
		if err == nil || !strings.Contains(err.Error(), "unexpected argument") {
			t.Fatalf("run(%q) = %v", args, err)
		}
	}
}
