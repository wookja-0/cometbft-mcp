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
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/wookja-0/cometbft-mcp/internal/config"
	"github.com/wookja-0/cometbft-mcp/internal/fleet"
	"github.com/wookja-0/cometbft-mcp/internal/safehttp"
	"github.com/wookja-0/cometbft-mcp/internal/tools"
)

var version = "dev" // set by -ldflags "-X main.version=…"

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
	configPath := fs.String("config", config.DefaultPath(), "path to the node config (env COMETBFT_MCP_CONFIG)")
	reveal := fs.Bool("reveal-network", false, "show raw peer IPs, node IDs and transport errors to the model")
	timeout := fs.Duration("timeout", 10*time.Second, "per-request RPC timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("config %s: %w", *configPath, err)
	}
	f := fleet.New(cfg, safehttp.New(safehttp.Options{Timeout: *timeout}), fleet.Options{RevealNetwork: *reveal})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if cmd == "check" {
		r, err := f.Status(ctx, "")
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}

	// stdout carries the MCP protocol; logs go to stderr.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	s := mcp.NewServer(&mcp.Implementation{Name: "cometbft-mcp", Title: "CometBFT validator ops", Version: version}, &mcp.ServerOptions{
		Instructions: tools.Instructions(cfg),
		Logger:       logger,
	})
	tools.Register(s, f, cfg)
	return s.Run(ctx, &mcp.StdioTransport{})
}
