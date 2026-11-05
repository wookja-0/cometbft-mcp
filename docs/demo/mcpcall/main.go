// Command mcpcall calls one cometbft-mcp tool over MCP stdio and prints its
// summary, styled for the README demo (docs/demo/demo.sh). The output is the
// server's real result; only colors and line wrapping are added.
//
//	mcpcall -bin ./bin/cometbft-mcp -config localnet/config.yaml validator_health '{"node":"val3"}'
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	reset  = "\033[0m"
	bold   = "\033[1m"
	dim    = "\033[2m"
	red    = "\033[31m"
	green  = "\033[32m"
	yellow = "\033[33m"
	cyan   = "\033[36m"
	purple = "\033[35m"
)

func main() {
	bin := flag.String("bin", "bin/cometbft-mcp", "cometbft-mcp binary")
	cfg := flag.String("config", "localnet/config.yaml", "config file")
	width := flag.Int("width", 92, "wrap width")
	flag.Parse()
	if flag.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "usage: mcpcall [flags] <tool> [json-args]")
		os.Exit(2)
	}
	tool := flag.Arg(0)
	args := map[string]any{}
	if flag.NArg() > 1 {
		if err := json.Unmarshal([]byte(flag.Arg(1)), &args); err != nil {
			fmt.Fprintln(os.Stderr, "args:", err)
			os.Exit(2)
		}
	}

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "demo", Version: "1"}, nil)
	s, err := client.Connect(ctx, &mcp.CommandTransport{Command: exec.Command(*bin, "--config", *cfg)}, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "connect:", err)
		os.Exit(1)
	}
	defer s.Close()

	fmt.Printf("  %s●%s %scometbft%s %s›%s %s%s%s%s\n", purple, reset, bold, reset, dim, reset, cyan, tool, reset, formatArgs(args))
	res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		fmt.Fprintln(os.Stderr, "call:", err)
		os.Exit(1)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	summary, _, _ := strings.Cut(text, "\n")
	for i, line := range wrap(summary, *width) {
		prefix := "    "
		if i == 0 {
			prefix = "  " + dim + "⎿" + reset + " "
		}
		fmt.Println(prefix + colorize(line))
	}
}

func formatArgs(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s%s=%s%v", dim, k, reset, args[k]))
	}
	return " " + strings.Join(parts, " ")
}

func wrap(s string, width int) []string {
	var lines []string
	line := ""
	for _, w := range strings.Fields(s) {
		if line != "" && len([]rune(line))+1+len([]rune(w)) > width {
			lines = append(lines, line)
			line = w
			continue
		}
		if line != "" {
			line += " "
		}
		line += w
	}
	return append(lines, line)
}

var (
	levelRe = regexp.MustCompile(`\[(ok|warn|critical|unknown)\]`)
	codeRe  = regexp.MustCompile(`\b(no_quorum|rpc_unreachable|not_signing|jailed|JAILED|stale_head)\b`)
	untrRe  = regexp.MustCompile(`</?untrusted>`)
)

func colorize(line string) string {
	line = untrRe.ReplaceAllStringFunc(line, func(t string) string { return dim + t + reset })
	line = levelRe.ReplaceAllStringFunc(line, func(t string) string {
		c := map[string]string{"[ok]": green, "[warn]": yellow, "[critical]": red, "[unknown]": yellow}[t]
		return c + bold + t + reset
	})
	return codeRe.ReplaceAllString(line, bold+"$1"+reset)
}
