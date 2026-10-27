package config

import (
	"strings"
	"testing"
)

const valid = `
nodes:
  - name: val-1
    role: validator
    rpc: http://127.0.0.1:26757
    metrics: http://127.0.0.1:26760
    validator_address: cosmosvalcons13x3ggf902z07z2n7mue4enjjruzu3s25368anl
  - name: sentry-1
    role: sentry
    rpc: https://rpc.example.com/cosmos/
`

func TestParseValid(t *testing.T) {
	c, err := Parse([]byte(valid))
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Names(); len(got) != 2 || got[0] != "val-1" || got[1] != "sentry-1" {
		t.Fatalf("names = %v", got)
	}
	v, _ := c.Node("val-1")
	if v.ValidatorHex != "89A28424AF509FE12A7EDF335CCE521F05C8C154" {
		t.Fatalf("validator hex = %q", v.ValidatorHex)
	}
	if v.RPCURL.Host != "127.0.0.1:26757" || v.MetricsURL == nil {
		t.Fatalf("urls = %v %v", v.RPCURL, v.MetricsURL)
	}
	s, _ := c.Node("sentry-1")
	if s.RPCURL.Path != "/cosmos/" {
		t.Fatalf("path = %q", s.RPCURL.Path)
	}
}

func TestKeyMaterialFieldsAreRejectedByName(t *testing.T) {
	for _, field := range []string{"priv_validator_key_file", "node_key", "mnemonic", "keyring_dir", "api_token"} {
		in := "nodes:\n  - name: v\n    role: validator\n    rpc: http://127.0.0.1:26657\n    " + field + ": /x\n"
		_, err := Parse([]byte(in))
		if err == nil || !strings.Contains(err.Error(), "never reads keys") {
			t.Errorf("%s: err = %v", field, err)
		}
	}
}

func TestUnknownFieldsAreRejected(t *testing.T) {
	_, err := Parse([]byte("nodes:\n  - name: v\n    role: full\n    rpc: http://127.0.0.1:26657\n    grpc: localhost:9090\n"))
	if err == nil || !strings.Contains(err.Error(), "grpc") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidation(t *testing.T) {
	cases := map[string]string{
		"no nodes":             "nodes: []\n",
		"bad name":             "nodes:\n  - {name: Val 1, role: full, rpc: 'http://a:1'}\n",
		"duplicate":            "nodes:\n  - {name: a, role: full, rpc: 'http://a:1'}\n  - {name: a, role: full, rpc: 'http://a:2'}\n",
		"bad role":             "nodes:\n  - {name: a, role: archive, rpc: 'http://a:1'}\n",
		"missing rpc":          "nodes:\n  - {name: a, role: full}\n",
		"bad scheme":           "nodes:\n  - {name: a, role: full, rpc: 'tcp://a:26657'}\n",
		"query":                "nodes:\n  - {name: a, role: full, rpc: 'http://a:1/?x=1'}\n",
		"address on sentry":    "nodes:\n  - {name: a, role: sentry, rpc: 'http://a:1', validator_address: 89A28424AF509FE12A7EDF335CCE521F05C8C154}\n",
		"bad address":          "nodes:\n  - {name: a, role: validator, rpc: 'http://a:1', validator_address: cosmos1abc}\n",
		"operator not valcons": "nodes:\n  - {name: a, role: validator, rpc: 'http://a:1', validator_address: cosmosvaloper1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq}\n",
	}
	for name, in := range cases {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
