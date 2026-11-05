package safehttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("want *safehttp.Error, got %T (%v)", err, err)
	}
	return e.Code
}

func TestGetSendsAllowlistedRequest(t *testing.T) {
	var gotMethod, gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c := New(Options{})
	body, err := c.Get(context.Background(), mustURL(t, srv.URL+"/rpc/"), CometCommit, url.Values{"height": {"42"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"ok":true}` {
		t.Fatalf("body = %s", body)
	}
	if gotMethod != http.MethodGet || gotPath != "/rpc/commit" || gotQuery != "height=42" {
		t.Fatalf("request = %s %s?%s", gotMethod, gotPath, gotQuery)
	}
}

func TestDeniedPathsAreRejectedBeforeAnyRequest(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1) }))
	defer srv.Close()

	c := New(Options{})
	for _, p := range []string{"/cosmos/tx/v1beta1/txs", "/broadcast_tx_sync", "/broadcast_tx_commit", "/broadcast_evidence", "/unsafe_flush_mempool", "/dial_peers", "/dial_seeds", "/check_tx", "/abci_query", "/genesis", "/dump_consensus_state", "/metrics"} {
		// Constructing an Endpoint by hand is only possible inside this
		// package; it simulates a future allowlist mistake.
		_, err := c.Get(context.Background(), mustURL(t, srv.URL), Endpoint{path: p}, nil)
		if code := codeOf(t, err); code != CodeNotAllowed {
			t.Errorf("%s: code = %s, want %s", p, code, CodeNotAllowed)
		}
	}
	if hits.Load() != 0 {
		t.Fatalf("server was hit %d times", hits.Load())
	}
}

func TestDeniedPrefixWinsOverAllowlist(t *testing.T) {
	allowlist["/broadcast_tx_sync"] = Endpoint{path: "/broadcast_tx_sync"}
	defer delete(allowlist, "/broadcast_tx_sync")

	_, err := New(Options{}).Get(context.Background(), mustURL(t, "http://127.0.0.1:1"), Endpoint{path: "/broadcast_tx_sync"}, nil)
	if code := codeOf(t, err); code != CodeNotAllowed {
		t.Fatalf("code = %s", code)
	}
}

func TestUnknownQueryParameterIsRejected(t *testing.T) {
	_, err := New(Options{}).Get(context.Background(), mustURL(t, "http://127.0.0.1:1"), CometStatus, url.Values{"tx": {"deadbeef"}})
	if code := codeOf(t, err); code != CodeNotAllowed {
		t.Fatalf("code = %s", code)
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	var followed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/broadcast_tx_sync" {
			followed.Store(true)
			return
		}
		http.Redirect(w, r, "/broadcast_tx_sync?tx=0x00", http.StatusFound)
	}))
	defer srv.Close()

	_, err := New(Options{}).Get(context.Background(), mustURL(t, srv.URL), CometStatus, nil)
	if code := codeOf(t, err); code != CodeHTTPStatus {
		t.Fatalf("code = %s", code)
	}
	if followed.Load() {
		t.Fatal("redirect was followed")
	}
}

func TestBodyLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(strings.Repeat("x", 2048)))
	}))
	defer srv.Close()

	_, err := New(Options{MaxBodyBytes: 1024}).Get(context.Background(), mustURL(t, srv.URL), CometStatus, nil)
	if code := codeOf(t, err); code != CodeBodyTooLarge {
		t.Fatalf("code = %s", code)
	}
}

func TestJSONRPCErrorBodyIsReturned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"jsonrpc":"2.0","id":-1,"error":{"code":-32603,"message":"Internal error","data":"height 1 is not available"}}`))
	}))
	defer srv.Close()

	body, err := New(Options{}).Get(context.Background(), mustURL(t, srv.URL), CometCommit, url.Values{"height": {"1"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "not available") {
		t.Fatalf("body = %s", body)
	}
}

func TestErrorsNeverCarryTheNodeAddress(t *testing.T) {
	// Nothing listens on this port; the raw transport error names the address.
	_, err := New(Options{Timeout: time.Second}).Get(context.Background(), mustURL(t, "http://127.0.0.1:1"), CometStatus, nil)
	if code := codeOf(t, err); code != CodeUnreachable {
		t.Fatalf("code = %s", code)
	}
	if strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("error leaks address: %q", err.Error())
	}
	var e *Error
	errors.As(err, &e)
	if e.Raw() == nil || !strings.Contains(e.Raw().Error(), "127.0.0.1") {
		t.Fatalf("raw cause should keep the detail for --reveal-network, got %v", e.Raw())
	}
}

func TestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	_, err := New(Options{Timeout: 50 * time.Millisecond}).Get(context.Background(), mustURL(t, srv.URL), CometStatus, nil)
	if code := codeOf(t, err); code != CodeTimeout {
		t.Fatalf("code = %s", code)
	}
}

func TestConcurrencyCap(t *testing.T) {
	var inFlight, peak atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inFlight.Add(-1)
	}))
	defer srv.Close()

	c := New(Options{MaxConcurrent: 2})
	done := make(chan struct{})
	for range 8 {
		go func() {
			c.Get(context.Background(), mustURL(t, srv.URL), CometStatus, nil)
			done <- struct{}{}
		}()
	}
	for range 8 {
		<-done
	}
	if peak.Load() > 2 {
		t.Fatalf("peak in-flight = %d, want <= 2", peak.Load())
	}
}

func TestPathSegmentIsValidated(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	c := New(Options{})

	if _, err := c.GetSegment(context.Background(), mustURL(t, srv.URL), CosmosSigningInfo, "cosmosvalcons1abc", nil); err != nil {
		t.Fatal(err)
	}
	if got != "/cosmos/slashing/v1beta1/signing_infos/cosmosvalcons1abc" {
		t.Fatalf("path = %s", got)
	}
	got = ""
	for _, seg := range []string{"", "../../tx/v1beta1/txs", "a/b", "abc?x=1", "ABC", "a%2Fb"} {
		_, err := c.GetSegment(context.Background(), mustURL(t, srv.URL), CosmosSigningInfo, seg, nil)
		if code := codeOf(t, err); code != CodeNotAllowed {
			t.Errorf("%q: code = %s", seg, code)
		}
	}
	if _, err := c.GetSegment(context.Background(), mustURL(t, srv.URL), CometStatus, "x", nil); codeOf(t, err) != CodeNotAllowed {
		t.Error("segment on a fixed endpoint must be rejected")
	}
	if got != "" {
		t.Fatalf("server was hit with %s", got)
	}
}
