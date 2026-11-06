package cosmos

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/wookja-0/cometbft-mcp/internal/safehttp"
)

// Fixtures in testdata/ are real responses captured from the localnet
// (simd v0.54.4).
func fixtureClient(t *testing.T, routes map[string]string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, ok := routes[r.URL.Path]
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"code":5,"message":"not found","details":[]}`))
			return
		}
		body, err := os.ReadFile("testdata/" + file)
		if err != nil {
			t.Errorf("fixture %s: %v", file, err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	return NewClient(safehttp.New(safehttp.Options{}), u)
}

func TestSlashingParamsAndSigningInfo(t *testing.T) {
	c := fixtureClient(t, map[string]string{
		"/cosmos/slashing/v1beta1/params":           "slashing_params.json",
		"/cosmos/auth/v1beta1/bech32":               "bech32.json",
		"/cosmos/base/tendermint/v1beta1/node_info": "node_info.json",
	})
	ctx := context.Background()

	p, err := c.SlashingParams(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if p.SignedBlocksWindow != 50 || p.MinSignedPerWindow != "0.500000000000000000" || p.DowntimeJailDuration != "60s" {
		t.Fatalf("params = %+v", p)
	}
	prefix, err := c.Bech32Prefix(ctx)
	if err != nil || prefix != "cosmos" {
		t.Fatalf("prefix = %q, %v", prefix, err)
	}
	ni, err := c.NodeInfo(ctx)
	if err != nil || ni.DefaultNodeInfo.Network != "localnet-1" {
		t.Fatalf("node info = %+v, %v", ni, err)
	}
}

func TestSigningInfoAndValidatorsFromLocalnet(t *testing.T) {
	c := fixtureClient(t, map[string]string{
		"/cosmos/slashing/v1beta1/signing_infos/cosmosvalcons1txjwr6c8c05cmsvmt5fsx4cgvtn5zgly5dlhhg": "signing_info.json",
	})
	info, err := c.SigningInfo(context.Background(), "cosmosvalcons1txjwr6c8c05cmsvmt5fsx4cgvtn5zgly5dlhhg")
	if err != nil {
		t.Fatal(err)
	}
	if info.Tombstoned || info.MissedBlocksCounter != 0 || info.IndexOffset != 7 || info.JailedUntil.Unix() != 0 {
		t.Fatalf("info = %+v", info)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("pagination.key") != "" {
			w.Write([]byte(`{"validators":[],"pagination":{"next_key":null,"total":"0"}}`))
			return
		}
		body, _ := os.ReadFile("testdata/validators_page1.json")
		w.Write(body)
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	vals, err := NewClient(safehttp.New(safehttp.Options{}), u).Validators(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(vals) != 2 || vals[0].ConsensusPubkey.Type != "/cosmos.crypto.ed25519.PubKey" || vals[0].Status != "BOND_STATUS_BONDED" {
		t.Fatalf("vals = %+v", vals)
	}
}

func TestSigningInfoNotFound(t *testing.T) {
	c := fixtureClient(t, nil)
	_, err := c.SigningInfo(context.Background(), "cosmosvalcons1qqqq")
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeNotFound {
		t.Fatalf("err = %v", err)
	}
}

// What Stride (an ICS consumer chain, no x/slashing) answers on stride-1.
func TestAbsentModuleIsUnimplemented(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		w.Write([]byte(`{"code":12,"message":"Not Implemented","details":[]}`))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	_, err := NewClient(safehttp.New(safehttp.Options{}), u).SlashingParams(context.Background())
	if !IsUnimplemented(err) {
		t.Fatalf("err = %v", err)
	}
	if IsUnimplemented(&Error{Code: CodeRESTError}) || IsUnimplemented(nil) {
		t.Fatal("only gRPC Unimplemented means the module is absent")
	}
}

func TestNonRESTEndpointIsExplained(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>"))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	_, err := NewClient(safehttp.New(safehttp.Options{}), u).SlashingParams(context.Background())
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeBadResponse || !strings.Contains(e.Detail, "Cosmos SDK REST") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidatorsPaginates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("pagination.key") == "" {
			w.Write([]byte(`{"validators":[{"operator_address":"cosmosvaloper1a","status":"BOND_STATUS_BONDED"}],"pagination":{"next_key":"Ab+/=","total":"2"}}`))
			return
		}
		if r.URL.Query().Get("pagination.key") != "Ab+/=" {
			t.Errorf("key = %q", r.URL.Query().Get("pagination.key"))
		}
		w.Write([]byte(`{"validators":[{"operator_address":"cosmosvaloper1b","jailed":true,"status":"BOND_STATUS_UNBONDING"}],"pagination":{"next_key":null,"total":"0"}}`))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)

	vals, err := NewClient(safehttp.New(safehttp.Options{}), u).Validators(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(vals) != 2 || !vals[1].Jailed || vals[1].Status != "BOND_STATUS_UNBONDING" {
		t.Fatalf("vals = %+v", vals)
	}
}

func TestUpgradePlanAndProposals(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/cosmos/upgrade/v1beta1/current_plan":
			w.Write([]byte(`{"plan":{"name":"v2","time":"0001-01-01T00:00:00Z","height":"1200","info":"","upgraded_client_state":null}}`))
		case "/cosmos/gov/v1/proposals":
			if r.URL.Query().Get("pagination.reverse") != "true" || r.URL.Query().Has("proposal_status") {
				t.Errorf("query = %s", r.URL.RawQuery)
			}
			w.Write([]byte(`{"proposals":[
				{"id":"3","title":"Upgrade to v3","status":"PROPOSAL_STATUS_VOTING_PERIOD","voting_end_time":"2026-10-28T12:00:00Z",
				 "messages":[{"@type":"/cosmos.upgrade.v1beta1.MsgSoftwareUpgrade","authority":"cosmos10d07y","plan":{"name":"v3","height":"5000","info":""}}]},
				{"id":"4","title":"Community spend","status":"PROPOSAL_STATUS_VOTING_PERIOD","voting_end_time":"2026-10-29T12:00:00Z",
				 "messages":[{"@type":"/cosmos.distribution.v1beta1.MsgCommunityPoolSpend"}]},
				{"id":"2","title":"Old upgrade","status":"PROPOSAL_STATUS_PASSED",
				 "messages":[{"@type":"/cosmos.upgrade.v1beta1.MsgSoftwareUpgrade","plan":{"name":"v1","height":"100"}}]}
			],"pagination":{"next_key":null,"total":"3"}}`))
		}
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	c := NewClient(safehttp.New(safehttp.Options{}), u)

	plan, err := c.CurrentPlan(context.Background())
	if err != nil || plan == nil || plan.Name != "v2" || plan.Height != 1200 {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
	props, err := c.VotingProposals(context.Background())
	if err != nil || len(props) != 2 {
		t.Fatalf("props = %+v, %v", props, err)
	}
	if p := props[0].UpgradePlan(); p == nil || p.Name != "v3" || p.Height != 5000 {
		t.Fatalf("upgrade plan = %+v", p)
	}
	if props[1].UpgradePlan() != nil {
		t.Fatal("non-upgrade proposal reported a plan")
	}
}

func TestNoPlan(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"plan":null}`))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	plan, err := NewClient(safehttp.New(safehttp.Options{}), u).CurrentPlan(context.Background())
	if err != nil || plan != nil {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
}
