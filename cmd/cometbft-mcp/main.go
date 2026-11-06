// Command cometbft-mcp is a read-only MCP server for CometBFT node and
// validator operators.
//
//	cometbft-mcp [--config path] [--reveal-network]   serve MCP over stdio
//	cometbft-mcp check [--config path]                  print node_status once
//	cometbft-mcp version
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wookja-0/cometbft-mcp/internal/config"
	"github.com/wookja-0/cometbft-mcp/internal/fleet"
	"github.com/wookja-0/cometbft-mcp/internal/safehttp"
	"github.com/wookja-0/cometbft-mcp/internal/tools"
)

// version is set by -ldflags "-X main.version=…" in release builds; for
// `go install …@vX.Y.Z` it falls back to the module version.
var version = "dev"

func init() {
	if version != "dev" {
		return
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		version = strings.TrimPrefix(bi.Main.Version, "v")
	}
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "cometbft-mcp:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := "serve"
	if len(args) > 0 && (args[0] == "check" || args[0] == "version") {
		cmd, args = args[0], args[1:]
	}
	if cmd == "version" {
		fmt.Println(version)
		return nil
	}

	fs := flag.NewFlagSet("cometbft-mcp", flag.ContinueOnError)
	configPath := fs.String("config", config.DefaultPath(), "path to the node config (or set $COMETBFT_MCP_CONFIG to a path, or $COMETBFT_MCP_CONFIG_YAML to the YAML itself)")
	reveal := fs.Bool("reveal-network", false, "show raw peer IPs, node IDs and transport errors to the model")
	timeout := fs.Duration("timeout", 10*time.Second, "per-request RPC timeout")
	concurrency := fs.Int("concurrency", 4, "maximum in-flight requests across all nodes; raise it (e.g. 8) for remote or public RPCs")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *concurrency < 1 || *concurrency > 32 {
		return fmt.Errorf("--concurrency must be between 1 and 32")
	}
	explicit := false
	fs.Visit(func(f *flag.Flag) { explicit = explicit || f.Name == "config" })
	src := config.Resolve(*configPath, explicit, os.Getenv)
	cfg, cfgErr := src.Load()
	if cfgErr != nil && cmd == "check" {
		return fmt.Errorf("config %s: %w", src, cfgErr)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// stdout carries the MCP protocol; logs go to stderr.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	impl := &mcp.Implementation{Name: "cometbft-mcp", Title: "CometBFT validator ops", Version: version}

	// Without a usable config, still serve: the tools stay listed and each
	// call explains the setup, instead of the client seeing a dead process.
	if cfgErr != nil {
		setupErr := setupError(src, cfgErr)
		logger.Warn("serving without a config", "source", src.String(), "err", cfgErr)
		s := mcp.NewServer(impl, &mcp.ServerOptions{Instructions: tools.UnconfiguredInstructions(setupErr), Logger: logger})
		tools.RegisterUnconfigured(s, setupErr)
		return s.Run(ctx, &mcp.StdioTransport{})
	}

	f := fleet.New(cfg, safehttp.New(safehttp.Options{Timeout: *timeout, MaxConcurrent: *concurrency}), fleet.Options{RevealNetwork: *reveal})

	if cmd == "check" {
		r, err := f.Status(ctx, "")
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}

	s := mcp.NewServer(impl, &mcp.ServerOptions{Instructions: tools.Instructions(cfg), Logger: logger})
	tools.Register(s, f, cfg)
	return s.Run(ctx, &mcp.StdioTransport{})
}

const exampleConfigURL = "https://github.com/wookja-0/cometbft-mcp/blob/main/config.example.yaml"

// publicExampleConfig watches Cosmos Hub through public endpoints. It is only
// suggested in the setup message, never used unless the user sets it.
const publicExampleConfig = `{cosmos: {rest: "https://cosmos-rest.publicnode.com"}, nodes: [{name: public, role: full, rpc: "https://cosmos-rpc.publicnode.com:443"}]}`

// setupError tells the user (through the model) how to fix the config. It
// never includes the parse error itself: YAML errors quote config values,
// such as internal node addresses, which must not reach the model.
func setupError(src config.Source, err error) error {
	switch {
	case src.Inline != "":
		return fmt.Errorf("the YAML in $%s is invalid. Run `cometbft-mcp check` with the same environment to see why (format: %s), fix it, then restart the MCP server", config.EnvInline, exampleConfigURL)
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("no config file at %s. Create it from %s with your nodes' CometBFT RPC URLs (and optionally a Cosmos REST URL), or put the YAML in $%s, then restart the MCP server. To try it on Cosmos Hub's public endpoints first, set $%s to %s", src.Path, exampleConfigURL, config.EnvInline, config.EnvInline, publicExampleConfig)
	}
	return fmt.Errorf("the config file at %s is invalid. Run `cometbft-mcp check --config %s` in a terminal to see why, fix it, then restart the MCP server", src.Path, src.Path)
}
