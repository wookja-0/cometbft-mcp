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
