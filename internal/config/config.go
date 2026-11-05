// Package config loads the node list. The schema deliberately has no field
// for key material: there is nothing to point at a priv_validator_key.json.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/wookja-0/cometbft-mcp/internal/address"
)

type Role string

const (
	RoleValidator Role = "validator"
	RoleSentry    Role = "sentry"
	RoleFull      Role = "full"
)

type Node struct {
	Name             string `yaml:"name"`
	Role             Role   `yaml:"role"`
	RPC              string `yaml:"rpc"`
	ValidatorAddress string `yaml:"validator_address,omitempty"`

	RPCURL       *url.URL `yaml:"-"`
	ValidatorHex string   `yaml:"-"` // parsed ValidatorAddress, upper-case hex
}

// Cosmos enables the Cosmos SDK layer (jail status, slashing margin,
// upgrades). Chain state is the same on every node, so one REST endpoint —
// a sentry or full node, never needing the validator's own API — suffices.
type Cosmos struct {
	REST string `yaml:"rest"`

	RESTURL *url.URL `yaml:"-"`
}

type Config struct {
	Cosmos *Cosmos `yaml:"cosmos,omitempty"`
	Nodes  []Node  `yaml:"nodes"`
}

// Node returns the node with the given name.
func (c *Config) Node(name string) (*Node, bool) {
	for i := range c.Nodes {
		if c.Nodes[i].Name == name {
			return &c.Nodes[i], true
		}
	}
	return nil, false
}

// Names lists node names in config order.
func (c *Config) Names() []string {
	out := make([]string, len(c.Nodes))
	for i, n := range c.Nodes {
		out[i] = n.Name
	}
	return out
}

// DefaultPath resolves the config location: $COMETBFT_MCP_CONFIG, then
// $XDG_CONFIG_HOME/cometbft-mcp/config.yaml, then ~/.config/cometbft-mcp/config.yaml.
func DefaultPath() string {
	if p := os.Getenv("COMETBFT_MCP_CONFIG"); p != "" {
		return p
	}
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "cometbft-mcp", "config.yaml")
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

// secretish matches field names an operator might add by mistake. They get a
// pointed error instead of a generic "unknown field".
var secretish = regexp.MustCompile(`(?i)(key|mnemonic|seed|priv|secret|password|passphrase|token)`)

func Parse(data []byte) (*Config, error) {
	var raw yaml.Node
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	if err := rejectSecretFields(&raw); err != nil {
		return nil, err
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var c Config
	if err := dec.Decode(&c); err != nil {
		return nil, err
	}
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func rejectSecretFields(n *yaml.Node) error {
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if k := n.Content[i].Value; secretish.MatchString(k) {
				return fmt.Errorf("line %d: field %q looks like key material; cometbft-mcp never reads keys or secrets — remove it", n.Content[i].Line, k)
			}
		}
	}
	for _, c := range n.Content {
		if err := rejectSecretFields(c); err != nil {
			return err
		}
	}
	return nil
}

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,62}$`)

func (c *Config) validate() error {
	if len(c.Nodes) == 0 {
		return errors.New("config has no nodes")
	}
	if c.Cosmos != nil {
		var err error
		if c.Cosmos.RESTURL, err = parseURL(c.Cosmos.REST); err != nil {
			return fmt.Errorf("cosmos.rest: %w", err)
		}
	}
	seen := map[string]bool{}
	for i := range c.Nodes {
		n := &c.Nodes[i]
		where := fmt.Sprintf("nodes[%d]", i)
		if !nameRe.MatchString(n.Name) {
			return fmt.Errorf("%s: name %q must be lowercase letters, digits, '.', '_' or '-'", where, n.Name)
		}
		where = fmt.Sprintf("node %q", n.Name)
		if seen[n.Name] {
			return fmt.Errorf("%s: duplicate name", where)
		}
		seen[n.Name] = true

		switch n.Role {
		case RoleValidator, RoleSentry, RoleFull:
		default:
			return fmt.Errorf("%s: role must be validator, sentry or full", where)
		}

		var err error
		if n.RPCURL, err = parseURL(n.RPC); err != nil {
			return fmt.Errorf("%s: rpc: %w", where, err)
		}
		if n.ValidatorAddress != "" {
			if n.Role != RoleValidator {
				return fmt.Errorf("%s: validator_address is only valid on role: validator", where)
			}
			if n.ValidatorHex, err = address.ParseConsensus(n.ValidatorAddress); err != nil {
				return fmt.Errorf("%s: validator_address: %w", where, err)
			}
		}
	}
	return nil
}

func parseURL(s string) (*url.URL, error) {
	if s == "" {
		return nil, errors.New("required")
	}
	u, err := url.Parse(s)
	if err != nil {
		return nil, errors.New("not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("scheme must be http or https")
	}
	if u.Host == "" {
		return nil, errors.New("missing host")
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("must not contain a query or fragment")
	}
	if strings.Contains(u.Path, "..") {
		return nil, errors.New("path must not contain '..'")
	}
	return u, nil
}
