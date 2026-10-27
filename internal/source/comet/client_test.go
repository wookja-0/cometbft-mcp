package comet

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
// (simd v0.54.4 / CometBFT v0.39.4).
func fixtureServer(t *testing.T, routes map[string]string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, ok := routes[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
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

func TestStatus(t *testing.T) {
	c := fixtureServer(t, map[string]string{"/status": "status_val0.json"})
	s, err := c.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.NodeInfo.Moniker != "node0" || s.NodeInfo.Network != "localnet-1" {
		t.Fatalf("node info = %+v", s.NodeInfo)
	}
	if s.SyncInfo.LatestBlockHeight <= 0 || s.SyncInfo.LatestBlockTime.IsZero() || s.SyncInfo.CatchingUp {
		t.Fatalf("sync info = %+v", s.SyncInfo)
	}
	if s.ValidatorInfo.Address != "89A28424AF509FE12A7EDF335CCE521F05C8C154" || s.ValidatorInfo.VotingPower != 100 {
		t.Fatalf("validator info = %+v", s.ValidatorInfo)
	}
}

func TestNetInfo(t *testing.T) {
	c := fixtureServer(t, map[string]string{"/net_info": "net_info_val0.json"})
	n, err := c.NetInfo(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if n.NPeers != 1 || len(n.Peers) != 1 {
		t.Fatalf("peers = %d / %d", n.NPeers, len(n.Peers))
	}
	p := n.Peers[0]
	if p.NodeInfo.Moniker != "sentry" || p.RemoteIP != "172.30.0.20" || p.IsOutbound {
		t.Fatalf("peer = %+v", p)
	}
	if p.ConnectionStatus.Duration <= 0 {
		t.Fatalf("duration = %d", p.ConnectionStatus.Duration)
	}
}

func TestCommitAndValidatorsShareOrder(t *testing.T) {
	c := fixtureServer(t, map[string]string{"/commit": "commit_10.json", "/validators": "validators_10.json"})
	cm, err := c.Commit(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	vals, err := c.Validators(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if cm.Header.Height != 10 || cm.Header.ValidatorsHash == "" {
		t.Fatalf("header = %+v", cm.Header)
	}
	if len(cm.Commit.Signatures) != 4 || len(vals) != 4 {
		t.Fatalf("sigs = %d, vals = %d", len(cm.Commit.Signatures), len(vals))
	}
	for i, sig := range cm.Commit.Signatures {
		if sig.BlockIDFlag != FlagCommit || sig.ValidatorAddress != vals[i].Address {
			t.Errorf("sig %d = %+v, validator = %s", i, sig, vals[i].Address)
		}
	}
}

func TestHeightUnavailable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"jsonrpc":"2.0","id":-1,"error":{"code":-32603,"message":"Internal error","data":"height 99999999 must be less than or equal to the current blockchain height 22"}}`))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)

	_, err := NewClient(safehttp.New(safehttp.Options{}), u).Commit(context.Background(), 99999999)
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeHeightUnavailable {
		t.Fatalf("err = %v", err)
	}
}

func TestNonCometEndpointIsExplained(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>not an rpc</html>"))
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)

	_, err := NewClient(safehttp.New(safehttp.Options{}), u).Status(context.Background())
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeBadResponse || !strings.Contains(e.Detail, "CometBFT RPC") {
		t.Fatalf("err = %v", err)
	}
}

func TestValidatorsPaginates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("page") {
		case "1":
			w.Write([]byte(`{"result":{"validators":[{"address":"A","voting_power":"3"},{"address":"B","voting_power":"2"}],"total":"3"}}`))
		case "2":
			w.Write([]byte(`{"result":{"validators":[{"address":"C","voting_power":"1"}],"total":"3"}}`))
		default:
			t.Errorf("unexpected page %q", r.URL.Query().Get("page"))
		}
	}))
	defer srv.Close()
	u, _ := url.Parse(srv.URL)

	vals, err := NewClient(safehttp.New(safehttp.Options{}), u).Validators(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(vals) != 3 || vals[2].Address != "C" || vals[0].VotingPower != 3 {
		t.Fatalf("vals = %+v", vals)
	}
}
