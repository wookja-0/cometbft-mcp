package config

import (
	"strings"
	"testing"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestResolvePrecedence(t *testing.T) {
	inline := "nodes: [{name: a, role: full, rpc: 'http://127.0.0.1:1'}]"
	cases := []struct {
		name     string
		flag     string
		explicit bool
		env      map[string]string
		want     Source
	}{
		{"explicit flag wins over everything", "/etc/c.yaml", true, map[string]string{EnvInline: inline, EnvPath: "/p.yaml"}, Source{Path: "/etc/c.yaml"}},
		{"inline beats path env", "/unused", false, map[string]string{EnvInline: inline, EnvPath: "/p.yaml"}, Source{Inline: inline}},
		{"path env", "/unused", false, map[string]string{EnvPath: "/p.yaml"}, Source{Path: "/p.yaml"}},
		{"blank inline is ignored", "/unused", false, map[string]string{EnvInline: "  \n", EnvPath: "/p.yaml"}, Source{Path: "/p.yaml"}},
		{"default under XDG", "/unused", false, map[string]string{"XDG_CONFIG_HOME": "/xdg"}, Source{Path: "/xdg/cometbft-mcp/config.yaml"}},
		{"default under HOME", "/unused", false, map[string]string{"HOME": "/home/op"}, Source{Path: "/home/op/.config/cometbft-mcp/config.yaml"}},
		{"~ in path env", "/unused", false, map[string]string{EnvPath: "~/.config/cometbft-mcp/config.yaml", "HOME": "/home/op"}, Source{Path: "/home/op/.config/cometbft-mcp/config.yaml"}},
		{"~ in explicit flag", "~/c.yaml", true, map[string]string{"HOME": "/home/op"}, Source{Path: "/home/op/c.yaml"}},
	}
	for _, c := range cases {
		if got := Resolve(c.flag, c.explicit, env(c.env)); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

// One-line flow-style YAML, as a web form or a Docker -e flag passes it.
func TestInlineFlowYAMLLoads(t *testing.T) {
	src := Source{Inline: `{cosmos: {rest: "https://cosmos-rest.publicnode.com"}, nodes: [{name: public, role: full, rpc: "https://cosmos-rpc.publicnode.com:443"}]}`}
	c, err := src.Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Cosmos == nil || len(c.Nodes) != 1 || c.Nodes[0].RPCURL.Host != "cosmos-rpc.publicnode.com:443" {
		t.Fatalf("config = %+v", c)
	}
}

func TestSourceStringNeverIncludesInlineContent(t *testing.T) {
	s := Source{Inline: "nodes: [{name: a, role: full, rpc: 'http://10.9.8.7:26657'}]"}
	if got := s.String(); got != "$"+EnvInline || strings.Contains(got, "10.9.8.7") {
		t.Fatalf("String() = %q", got)
	}
}

func TestInlineRejectsSecretFieldsToo(t *testing.T) {
	_, err := Source{Inline: "nodes: [{name: a, role: validator, rpc: 'http://a:1', priv_validator_key_file: /x}]"}.Load()
	if err == nil || !strings.Contains(err.Error(), "never reads keys") {
		t.Fatalf("err = %v", err)
	}
}
