package config

import (
	"os"
	"path/filepath"
	"strings"
)

// Environment variables that locate the config.
const (
	EnvPath   = "COMETBFT_MCP_CONFIG"      // path to a config file
	EnvInline = "COMETBFT_MCP_CONFIG_YAML" // the config itself, as YAML (flow style fits one line)
)

// Source is where the config comes from: a file, or YAML from EnvInline.
type Source struct {
	Path   string // file path; empty when Inline
	Inline string // YAML from EnvInline
}

// Resolve picks the config source. An explicit --config path wins; then
// inline YAML from $COMETBFT_MCP_CONFIG_YAML; then $COMETBFT_MCP_CONFIG; then
// the default path. getenv is os.Getenv outside tests.
func Resolve(flagPath string, explicit bool, getenv func(string) string) Source {
	if explicit {
		return Source{Path: expandHome(flagPath, getenv)}
	}
	if y := getenv(EnvInline); strings.TrimSpace(y) != "" {
		return Source{Inline: y}
	}
	if p := getenv(EnvPath); p != "" {
		return Source{Path: expandHome(p, getenv)}
	}
	return Source{Path: defaultPath(getenv)}
}

// Load reads and validates the config from its source.
func (s Source) Load() (*Config, error) {
	if s.Inline != "" {
		return Parse([]byte(s.Inline))
	}
	return Load(s.Path)
}

// String names the source for messages. It never includes inline content,
// which may hold internal node addresses.
func (s Source) String() string {
	if s.Inline != "" {
		return "$" + EnvInline
	}
	return s.Path
}

// DefaultPath is where the config lives when nothing else is set.
func DefaultPath() string { return defaultPath(os.Getenv) }

func defaultPath(getenv func(string) string) string {
	dir := getenv("XDG_CONFIG_HOME")
	if dir == "" {
		dir = filepath.Join(home(getenv), ".config")
	}
	return filepath.Join(dir, "cometbft-mcp", "config.yaml")
}

// expandHome turns a leading "~/" into the home directory, since clients
// and directories often pass such paths through without a shell.
func expandHome(p string, getenv func(string) string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		return filepath.Join(home(getenv), strings.TrimPrefix(p, "~"))
	}
	return p
}

func home(getenv func(string) string) string {
	if h := getenv("HOME"); h != "" {
		return h
	}
	h, _ := os.UserHomeDir()
	return h
}
